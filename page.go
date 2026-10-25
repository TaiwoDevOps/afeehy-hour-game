package main

import (
	"crypto/sha256"
	"embed"
	"encoding/base64"
	"fmt"
	"io/fs"
	"net/http"
	"regexp"
	"strings"
)

//go:embed web/game.html
var gameHTML []byte

//go:embed web/shim.js
var shimJS []byte

//go:embed web/fonts/*.woff2
var fontFiles embed.FS

// Fonts are served from this server (no Google Fonts), so the page makes no
// third-party requests at all.
var fontFamilies = []struct{ family, file string }{
	{"Unbounded", "unbounded"},
	{"Figtree", "figtree"},
	{"JetBrains Mono", "jetbrains-mono"},
}

const (
	rangeLatin    = "U+0000-00FF,U+0131,U+0152-0153,U+02BB-02BC,U+02C6,U+02DA,U+02DC,U+0304,U+0308,U+0329,U+2000-206F,U+20AC,U+2122,U+2191,U+2193,U+2212,U+2215,U+FEFF,U+FFFD"
	rangeLatinExt = "U+0100-02BA,U+02BD-02C5,U+02C7-02CC,U+02CE-02D7,U+02DD-02FF,U+0304,U+0308,U+0329,U+1D00-1DBF,U+1E00-1E9F,U+1EF2-1EFF,U+2020,U+20A0-20AB,U+20AD-20C0,U+2113,U+2C60-2C7F,U+A720-A7FF"
)

var (
	googleFontLinks = regexp.MustCompile(`<link[^>]+fonts\.(googleapis|gstatic)\.com[^>]*>\s*`)
	inlineScripts   = regexp.MustCompile(`(?s)<script>(.*?)</script>`)
)

// buildPage wraps the game page (written as a fragment) in a full document with
// the adapter script first, and returns it with a Content-Security-Policy that
// allows only these exact inline scripts (by hash) and same-origin requests.
func buildPage() (page []byte, csp string) {
	var faces strings.Builder
	for _, f := range fontFamilies {
		for _, r := range []struct{ subset, rng string }{{"latin-ext", rangeLatinExt}, {"latin", rangeLatin}} {
			fmt.Fprintf(&faces, `@font-face{font-family:%q;font-style:normal;font-display:swap;font-weight:100 900;`+
				`src:url(fonts/%s-%s-wght-normal.woff2) format("woff2");unicode-range:%s}`, f.family, f.file, r.subset, r.rng)
		}
	}
	game := googleFontLinks.ReplaceAllString(string(gameHTML), "")
	html := `<!doctype html><html lang="en"><head><meta charset="utf-8">` +
		`<meta name="viewport" content="width=device-width,initial-scale=1,viewport-fit=cover">` +
		`<meta name="referrer" content="no-referrer">` +
		`<style>:root{color-scheme:light;padding-top:env(safe-area-inset-top,0px);padding-bottom:env(safe-area-inset-bottom,0px)}` +
		`body{margin:0;font:14px/1.4 system-ui,sans-serif}img{max-width:100%}[hidden]{display:none!important}` +
		faces.String() + `</style>` +
		"<script>" + string(shimJS) + "</script>" + game + "</html>"

	var hashes []string
	for _, m := range inlineScripts.FindAllStringSubmatch(html, -1) {
		sum := sha256.Sum256([]byte(m[1]))
		hashes = append(hashes, "'sha256-"+base64.StdEncoding.EncodeToString(sum[:])+"'")
	}
	csp = "default-src 'none'; script-src " + strings.Join(hashes, " ") +
		"; style-src 'self' 'unsafe-inline'; font-src 'self'; img-src 'self' data:; connect-src 'self'" +
		"; base-uri 'none'; form-action 'none'; frame-ancestors 'none'; object-src 'none'"
	return []byte(html), csp
}

func fontHandler() http.Handler {
	sub, err := fs.Sub(fontFiles, "web/fonts")
	if err != nil {
		panic(err)
	}
	files := http.StripPrefix("/fonts/", http.FileServer(http.FS(sub)))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/") { // no directory listings
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "public, max-age=86400")
		files.ServeHTTP(w, r)
	})
}

// secure adds browser security headers to every response.
func secure(csp string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", csp)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		h.Set("Cross-Origin-Resource-Policy", "same-origin")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=(), usb=(), "+
			"bluetooth=(), serial=(), hid=(), display-capture=(), clipboard-read=()")
		if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
			h.Set("Strict-Transport-Security", "max-age=31536000")
		}
		next.ServeHTTP(w, r)
	})
}
