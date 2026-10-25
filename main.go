// Afeehy Hour live game server.
//
// One binary, standard library only. It serves the game page and keeps a small
// shared document store that clients mirror over Server-Sent Events:
//
//	POST /api      {op: hello|set|update|delete|emit, ...}   (header X-Token)
//	GET  /events   ?token=...  → "snapshot", then "doc" and "room" events
//
// Access rules mirror the original artifact: hosts (who know HOST_KEY) read and
// write everything; players read shared state, write only their own player,
// answer and vote documents, and may only broadcast on the "answer" topic.
package main

import (
	"crypto/rand"
	"crypto/subtle"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"
)

var indexHTML, pageCSP = buildPage()

//go:embed seed/bank.json
var seedBank []byte

//go:embed seed/claims.json
var seedClaims []byte

const (
	maxBody        = 1 << 20 // request body limit
	maxHostDoc     = 512 << 10
	maxEmit        = 8 << 10
	clientBuffer   = 512
	opsPerSecond   = 20 // per player session
	codeTriesPerIP = 10 // wrong event codes or host keys allowed per IP per minute
	heartbeatEvery = 15 * time.Second
)

var segRe = regexp.MustCompile(`^[A-Za-z0-9_.:@+~-]{1,200}$`)

// seqPaths are the shared-state documents whose "seq" the server assigns, so
// two hosts writing at the same moment can never produce the same sequence
// number and leave clients disagreeing about which state is newest.
var seqPaths = map[string]bool{"game/state": true, "debate/state": true, "meta/live": true}

type Doc struct {
	Data map[string]any `json:"data"`
	Rev  int64          `json:"rev"`
}

type Session struct {
	ID    string `json:"id"`
	Token string `json:"token"`
	Role  string `json:"role"` // "host" | "player"

	window time.Time
	ops    int
}

type client struct {
	s  *Session
	ch chan []byte
}

type Server struct {
	mu       sync.Mutex
	docs     map[string]*Doc
	rev      int64
	sessions map[string]*Session // by token
	clients  map[*client]struct{}
	hostKey  string
	dataFile string
	dirty    bool

	eventCode   string // optional; players must enter it to join
	maxSessions int
	codeFails   map[string]*attempts // wrong event codes and host keys, per client IP
	trustProxy  bool                 // take the client IP from proxy headers
}

type attempts struct {
	since time.Time
	n     int
}

type persisted struct {
	Rev      int64               `json:"rev"`
	Docs     map[string]*Doc     `json:"docs"`
	Sessions map[string]*Session `json:"sessions"`
}

func main() {
	port := env("PORT", "8080")
	hostKey := os.Getenv("HOST_KEY")
	if hostKey == "" {
		hostKey = randHex(8)
		log.Printf("HOST_KEY not set; generated one for this run: %s", hostKey)
	}
	s := &Server{
		docs:     map[string]*Doc{},
		sessions: map[string]*Session{},
		clients:  map[*client]struct{}{},
		hostKey:  hostKey,
		dataFile: env("DATA_FILE", "afeehy-data.json"),

		eventCode:   strings.TrimSpace(os.Getenv("EVENT_CODE")),
		maxSessions: 2000,
		codeFails:   map[string]*attempts{},
		trustProxy:  os.Getenv("TRUST_PROXY") != "",
	}
	s.load()
	s.seed()
	go s.saveLoop()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.handleIndex)
	mux.Handle("GET /fonts/", fontHandler())
	mux.HandleFunc("POST /api", s.handleAPI)
	mux.HandleFunc("GET /events", s.handleEvents)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) })

	log.Printf("Afeehy Hour running on :%s", port)
	log.Printf("Player link: http://localhost:%s/", port)
	log.Printf("Host link:   http://localhost:%s/?host=%s   (keep this one private)", port, hostKey)
	if s.eventCode != "" {
		log.Printf("Event code:  %s   (players must enter it to join)", s.eventCode)
	} else {
		log.Printf("Event code:  off (set EVENT_CODE to require one)")
	}
	if s.trustProxy {
		log.Printf("Proxy:       trusting CF-Connecting-IP / X-Forwarded-For for rate limits (TRUST_PROXY is set)")
	} else {
		log.Printf("Proxy:       rate limits use the connection address (set TRUST_PROXY=1 behind cloudflared, Render, Fly or a company proxy)")
	}
	srv := &http.Server{Addr: ":" + port, Handler: secure(pageCSP, mux),
		ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 120 * time.Second}
	log.Fatal(srv.ListenAndServe())
}

// ---------- HTTP ----------

func (s *Server) handleIndex(w http.ResponseWriter, _ *http.Request) {
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", "no-cache")
	w.Write(indexHTML)
}

type apiReq struct {
	Op        string          `json:"op"`
	Path      string          `json:"path"`
	Data      json.RawMessage `json:"data"`
	Topic     string          `json:"topic"`
	HostKey   string          `json:"hostKey"`
	EventCode string          `json:"eventCode"`
}

type apiErr struct {
	code, msg string
	status    int
}

func (e apiErr) Error() string { return e.msg }

func bad(msg string) error    { return apiErr{"invalid_argument", msg, 400} }
func denied(msg string) error { return apiErr{"invalid_argument", msg, 403} }

func (s *Server) handleAPI(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)
	var req apiReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, bad("bad request body"))
		return
	}
	if req.Op == "hello" {
		res, err := s.hello(r.Header.Get("X-Token"), req.HostKey, req.EventCode, s.clientIP(r))
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, res)
		return
	}
	s.mu.Lock()
	sess := s.sessions[r.Header.Get("X-Token")]
	s.mu.Unlock()
	if sess == nil {
		writeErr(w, apiErr{"revoked", "unknown session; reload the page", 401})
		return
	}
	res, err := s.do(sess, req)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, res)
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	sess := s.sessions[r.URL.Query().Get("token")]
	s.mu.Unlock()
	if sess == nil {
		http.Error(w, "unknown session", http.StatusUnauthorized)
		return
	}
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")

	c := &client{s: sess, ch: make(chan []byte, clientBuffer)}
	s.mu.Lock()
	snap := s.snapshotFor(sess)
	s.clients[c] = struct{}{}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.clients, c)
		s.mu.Unlock()
	}()

	w.Write(snap)
	fl.Flush()
	hb := time.NewTicker(heartbeatEvery)
	defer hb.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case msg, open := <-c.ch:
			if !open {
				return // dropped as too slow; the browser reconnects and gets a fresh snapshot
			}
			w.Write(msg)
			fl.Flush()
		case <-hb.C:
			w.Write([]byte(": ping\n\n"))
			fl.Flush()
		}
	}
}

// ---------- operations ----------

func (s *Server) hello(token, hostKey, code, ip string) (map[string]any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	hostOK := hostKey != "" && subtle.ConstantTimeCompare([]byte(hostKey), []byte(s.hostKey)) == 1
	if hostKey != "" && !hostOK {
		// A wrong host key counts like a wrong event code, so the key can't be guessed at network speed.
		if !s.codeAllowed(ip) {
			return nil, apiErr{"resource_exhausted", "too many wrong codes; wait a minute", 429}
		}
		s.codeFails[ip].n++
	}
	sess := s.sessions[token]
	if sess == nil {
		// New browsers need the event code (when one is set), unless they hold the host key.
		if s.eventCode != "" && !hostOK {
			if code == "" {
				return map[string]any{"needCode": true}, nil
			}
			if !s.codeAllowed(ip) {
				return nil, apiErr{"resource_exhausted", "too many wrong codes; wait a minute", 429}
			}
			if subtle.ConstantTimeCompare([]byte(strings.ToUpper(strings.TrimSpace(code))), []byte(strings.ToUpper(s.eventCode))) != 1 {
				s.codeFails[ip].n++
				return map[string]any{"needCode": true, "wrongCode": true}, nil
			}
		}
		if len(s.sessions) >= s.maxSessions {
			return nil, apiErr{"quota_exceeded", "the game is full", 503}
		}
		sess = &Session{ID: "u_" + randHex(10), Token: randHex(24), Role: "player"}
		s.sessions[sess.Token] = sess
		s.dirty = true
	}
	if hostOK && sess.Role != "host" {
		sess.Role, s.dirty = "host", true
	}
	return map[string]any{"id": sess.ID, "token": sess.Token, "role": sess.Role,
		"hostKeyRejected": hostKey != "" && !hostOK}, nil
}

// codeAllowed reports whether this IP may try another event code or host key. Caller holds s.mu.
func (s *Server) codeAllowed(ip string) bool {
	if len(s.codeFails) > 10000 { // forget stale entries so the map can't grow without bound
		for k, a := range s.codeFails {
			if time.Since(a.since) > time.Minute {
				delete(s.codeFails, k)
			}
		}
	}
	a := s.codeFails[ip]
	if a == nil || time.Since(a.since) > time.Minute {
		a = &attempts{since: time.Now()}
		s.codeFails[ip] = a
	}
	return a.n < codeTriesPerIP
}

// clientIP is the address rate limits are keyed on. Proxy headers are only
// believed when TRUST_PROXY is set: otherwise anyone could forge a fresh
// X-Forwarded-For per request and sidestep the per-IP limits.
func (s *Server) clientIP(r *http.Request) string {
	if s.trustProxy {
		if ip := r.Header.Get("CF-Connecting-IP"); ip != "" {
			return ip
		}
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			return strings.TrimSpace(strings.Split(xff, ",")[0])
		}
	}
	host := r.RemoteAddr
	if i := strings.LastIndex(host, ":"); i > 0 {
		host = host[:i]
	}
	return host
}

func (s *Server) do(sess *Session, req apiReq) (any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sess.Role != "host" && !s.allow(sess) {
		return nil, apiErr{"resource_exhausted", "too many requests; slow down", 429}
	}
	switch req.Op {
	case "set", "update":
		if err := checkPath(req.Path); err != nil {
			return nil, err
		}
		if !canWrite(sess, req.Path) {
			return nil, denied("not allowed to write " + req.Path)
		}
		var data map[string]any
		if err := json.Unmarshal(req.Data, &data); err != nil || data == nil {
			return nil, bad("data must be a JSON object")
		}
		if req.Op == "update" {
			cur := s.docs[req.Path]
			if cur == nil {
				return nil, bad("document does not exist")
			}
			data = merge(clone(cur.Data), data)
		}
		if sess.Role == "host" {
			if len(req.Data) > maxHostDoc {
				return nil, bad("document too large")
			}
			if seqPaths[req.Path] {
				data["seq"] = float64(s.rev + 1) // put() will advance rev to exactly this
			}
		} else {
			var err error
			if data, err = validatePlayerDoc(req.Path, data); err != nil {
				return nil, err
			}
		}
		rev := s.put(req.Path, data)
		return map[string]any{"rev": rev, "data": data}, nil

	case "delete":
		if err := checkPath(req.Path); err != nil {
			return nil, err
		}
		if !canWrite(sess, req.Path) {
			return nil, denied("not allowed to delete " + req.Path)
		}
		return map[string]any{"rev": s.put(req.Path, nil)}, nil

	case "emit":
		if len(req.Data) > maxEmit {
			return nil, bad("message too large")
		}
		var data any
		if len(req.Data) > 0 {
			if err := json.Unmarshal(req.Data, &data); err != nil {
				return nil, bad("bad message")
			}
		}
		switch {
		case sess.Role == "host":
			if !map[string]bool{"state": true, "debate": true, "meta": true, "tick": true}[req.Topic] {
				return nil, bad("unknown topic")
			}
		case req.Topic == "answer":
			m, ok := data.(map[string]any)
			if !ok {
				return nil, bad("bad answer")
			}
			ask, _ := m["askId"].(string)
			ch, okc := m["choice"].(float64)
			if ask == "" || len(ask) > 40 || !okc || ch != float64(int(ch)) || ch < 0 || ch > 3 {
				return nil, bad("bad answer")
			}
			data = map[string]any{"askId": ask, "choice": ch}
		default:
			return nil, denied("players can only send answers")
		}
		msg := event("room", map[string]any{"topic": req.Topic, "data": data, "by": sess.ID})
		for c := range s.clients {
			if req.Topic == "answer" && c.s.Role != "host" {
				continue // answers are for the timekeeper only; other players must not see them
			}
			s.send(c, msg)
		}
		return map[string]any{"ok": true}, nil
	}
	return nil, bad("unknown op")
}

// put stores (or deletes, when data is nil) a document and fans the change out
// to every connected client allowed to read it. Caller holds s.mu.
func (s *Server) put(path string, data map[string]any) int64 {
	s.rev++
	if data == nil {
		delete(s.docs, path)
	} else {
		s.docs[path] = &Doc{Data: data, Rev: s.rev}
	}
	s.dirty = true
	var payload any
	if data != nil {
		payload = data
	}
	msg := event("doc", map[string]any{"path": path, "data": payload, "rev": s.rev})
	for c := range s.clients {
		if canRead(c.s, path) {
			s.send(c, msg)
		}
	}
	return s.rev
}

func (s *Server) send(c *client, msg []byte) {
	select {
	case c.ch <- msg:
	default: // client can't keep up: drop it, it will reconnect and resync
		delete(s.clients, c)
		close(c.ch)
	}
}

func (s *Server) snapshotFor(sess *Session) []byte {
	type entry struct {
		Path string         `json:"path"`
		Data map[string]any `json:"data"`
		Rev  int64          `json:"rev"`
	}
	docs := make([]entry, 0, len(s.docs))
	for p, d := range s.docs {
		if canRead(sess, p) {
			docs = append(docs, entry{p, d.Data, d.Rev})
		}
	}
	return event("snapshot", map[string]any{"docs": docs, "rev": s.rev})
}

func (s *Server) allow(sess *Session) bool {
	now := time.Now()
	if now.Sub(sess.window) > time.Second {
		sess.window, sess.ops = now, 0
	}
	sess.ops++
	return sess.ops <= opsPerSecond
}

// ---------- access rules ----------

func canRead(s *Session, path string) bool {
	if s.Role == "host" {
		return true
	}
	seg := strings.Split(path, "/")
	switch seg[0] {
	case "hostdata":
		return false
	case "answers":
		return len(seg) == 2 && seg[1] == s.ID
	}
	return true
}

func canWrite(s *Session, path string) bool {
	if s.Role == "host" {
		return true
	}
	seg := strings.Split(path, "/")
	if len(seg) != 2 {
		return false
	}
	switch seg[0] {
	case "players", "answers", "votes":
		return seg[1] == s.ID
	}
	return false
}

func checkPath(p string) error {
	seg := strings.Split(p, "/")
	if len(seg) < 2 || len(seg) > 16 || len(seg)%2 != 0 || len(p) > 1000 {
		return bad("bad document path")
	}
	for _, x := range seg {
		if !segRe.MatchString(x) || x == "." || x == ".." {
			return bad("bad document path")
		}
	}
	return nil
}

// validatePlayerDoc keeps only the fields the game uses, with sane types and sizes.
func validatePlayerDoc(path string, in map[string]any) (map[string]any, error) {
	str := func(k string, max int, required bool) (string, error) {
		v, ok := in[k].(string)
		v = cleanText(v)
		if (!ok || v == "") && required {
			return "", bad(k + " is required")
		}
		if len([]rune(v)) > max {
			return "", bad(k + " is too long")
		}
		return v, nil
	}
	at, _ := in["at"].(float64)
	switch strings.Split(path, "/")[0] {
	case "players":
		name, err := str("name", 32, true)
		if err != nil {
			return nil, err
		}
		return map[string]any{"name": name, "at": at}, nil
	case "answers":
		ask, err := str("askId", 40, true)
		if err != nil {
			return nil, err
		}
		name, _ := str("name", 32, false)
		ch, ok := in["choice"].(float64)
		if !ok || ch != float64(int(ch)) || ch < 0 || ch > 3 {
			return nil, bad("choice must be 0-3")
		}
		return map[string]any{"askId": ask, "choice": ch, "name": name, "at": at}, nil
	case "votes":
		cid, err := str("claimId", 60, true)
		if err != nil {
			return nil, err
		}
		name, _ := str("name", 32, false)
		pick, _ := in["pick"].(string)
		if pick != "myth" && pick != "fact" {
			return nil, bad("pick must be myth or fact")
		}
		return map[string]any{"claimId": cid, "pick": pick, "name": name, "at": at}, nil
	}
	return nil, denied("not allowed")
}

// cleanText trims a player-entered string, drops control and invisible
// formatting characters (so a name can't look blank or contain line breaks)
// and collapses runs of whitespace to one space.
func cleanText(v string) string {
	var b strings.Builder
	for _, r := range v {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			continue
		}
		b.WriteRune(r)
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

// ---------- persistence ----------

func (s *Server) seed() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for path, raw := range map[string][]byte{"hostdata/bank": seedBank, "hostdata/claims": seedClaims} {
		var data map[string]any
		if err := json.Unmarshal(raw, &data); err != nil {
			log.Fatalf("seed %s: %v", path, err)
		}
		if cur := s.docs[path]; cur != nil {
			// Banks saved by versions before v7 had duplicate question ids, which made
			// a third of the questions unselectable. Replace such a bank unless the
			// host has added questions of their own, in which case only warn.
			if dups, custom := duplicateIDs(cur.Data, "questions"); dups {
				if custom {
					log.Printf("WARNING: %s has duplicate question ids; remove the duplicates or delete %s to reseed", path, s.dataFile)
				} else {
					log.Printf("%s has duplicate question ids (pre-v7 seed); replacing it with the built-in bank", path)
					s.put(path, data)
				}
			}
			continue
		}
		s.put(path, data)
	}
}

// duplicateIDs reports whether the list under key has repeated ids, and whether
// it contains host-added entries (ids starting with "c", e.g. "cus…"/"cc…").
func duplicateIDs(doc map[string]any, key string) (dups, custom bool) {
	list, _ := doc[key].([]any)
	seen := map[string]bool{}
	for _, it := range list {
		m, _ := it.(map[string]any)
		id, _ := m["id"].(string)
		if seen[id] {
			dups = true
		}
		seen[id] = true
		if strings.HasPrefix(id, "c") {
			custom = true
		}
	}
	return dups, custom
}

func (s *Server) load() {
	b, err := os.ReadFile(s.dataFile)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		log.Fatalf("read %s: %v", s.dataFile, err)
	}
	var p persisted
	if err := json.Unmarshal(b, &p); err != nil {
		log.Fatalf("parse %s: %v", s.dataFile, err)
	}
	s.rev, s.docs, s.sessions = p.Rev, p.Docs, p.Sessions
	if s.docs == nil {
		s.docs = map[string]*Doc{}
	}
	if s.sessions == nil {
		s.sessions = map[string]*Session{}
	}
	log.Printf("loaded %d documents and %d sessions from %s", len(s.docs), len(s.sessions), s.dataFile)
}

func (s *Server) saveLoop() {
	for range time.Tick(time.Second) {
		s.mu.Lock()
		if !s.dirty {
			s.mu.Unlock()
			continue
		}
		b, err := json.Marshal(persisted{Rev: s.rev, Docs: s.docs, Sessions: s.sessions})
		s.dirty = false
		s.mu.Unlock()
		if err != nil {
			log.Printf("save: %v", err)
			continue
		}
		tmp := s.dataFile + ".tmp"
		if err := os.WriteFile(tmp, b, 0o600); err == nil {
			err = os.Rename(tmp, s.dataFile)
		}
		if err != nil {
			log.Printf("save: %v", err)
		}
	}
}

// ---------- helpers ----------

func event(name string, v any) []byte {
	b, _ := json.Marshal(v)
	return []byte(fmt.Sprintf("event: %s\ndata: %s\n\n", name, b))
}

func merge(dst, src map[string]any) map[string]any {
	for k, v := range src {
		if sm, ok := v.(map[string]any); ok {
			if dm, ok := dst[k].(map[string]any); ok {
				dst[k] = merge(dm, sm)
				continue
			}
		}
		dst[k] = v
	}
	return dst
}

func clone(m map[string]any) map[string]any {
	b, _ := json.Marshal(m)
	var out map[string]any
	json.Unmarshal(b, &out)
	return out
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, err error) {
	e, ok := err.(apiErr)
	if !ok {
		e = apiErr{"unavailable", err.Error(), 500}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(e.status)
	json.NewEncoder(w).Encode(map[string]string{"error": e.msg, "code": e.code})
}

func randHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
