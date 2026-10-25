/* Adapter: gives the game page the same `claude.use("db" | "room" | "user")` API it
   uses as a Claude artifact, backed by this Go server instead.
   - db: a local mirror of every document this browser may read, kept live over
     Server-Sent Events; writes go to POST /api.
   - room: instant messages through the same channel.
   - user: an anonymous id per browser, plus host role when opened with ?host=KEY. */
(function(){
"use strict";
const store={get(k){try{return localStorage.getItem(k)}catch(e){return null}},
  set(k,v){try{localStorage.setItem(k,v)}catch(e){}},del(k){try{localStorage.removeItem(k)}catch(e){}}};
const params=new URLSearchParams(location.search);
if(params.has("host")){store.set("afeehy-host",params.get("host"));params.delete("host");
  history.replaceState(null,"",location.pathname+(params.toString()?"?"+params:"")+location.hash);}

const mirror=new Map(),revs=new Map(),docL=new Map(),colL=new Map(),roomL=new Map(),connL=new Set();
let me=null,token=store.get("afeehy-token"),connected=false,readyRes;
const ready=new Promise(r=>readyRes=r);
const parentOf=p=>p.slice(0,p.lastIndexOf("/")),lastOf=p=>p.slice(p.lastIndexOf("/")+1);
const copy=v=>v===undefined?undefined:JSON.parse(JSON.stringify(v));
const meta={fromCache:false,hasPendingWrites:false};
function docSnap(path){const d=mirror.get(path);return {id:lastOf(path),exists:d!==undefined,data:()=>copy(d),metadata:meta};}
function colSnap(col,filters){const docs=[];
  for(const [p,d] of mirror)if(parentOf(p)===col&&filters.every(([f,,v])=>d[f]===v))docs.push(docSnap(p));
  docs.sort((a,b)=>a.id<b.id?-1:a.id>b.id?1:0);
  return {docs,size:docs.length,empty:!docs.length,docChanges:()=>[],metadata:meta};}
function safe(fn,arg){try{fn(arg)}catch(e){console.error(e)}}
function notify(path){(docL.get(path)||[]).forEach(fn=>safe(fn,docSnap(path)));
  const col=parentOf(path);(colL.get(col)||[]).forEach(l=>safe(l.fn,colSnap(col,l.filters)));}
function listen(map,key,v){if(!map.has(key))map.set(key,new Set());map.get(key).add(v);return ()=>{const s=map.get(key);if(s)s.delete(v);};}
function apply(path,data,rev){if(rev<(revs.get(path)||0))return;revs.set(path,rev);
  if(data==null)mirror.delete(path);else mirror.set(path,data);notify(path);}

async function api(body){
  let r;try{r=await fetch("api",{method:"POST",headers:{"Content-Type":"application/json","X-Token":token||""},body:JSON.stringify(body)});}
  catch(e){throw {code:"unavailable",message:"Can't reach the game server."};}
  const j=await r.json().catch(()=>({error:"Bad response from the game server."}));
  if(j.code==="revoked")expired();
  if(!r.ok||j.error)throw {code:j.code||"invalid_argument",message:j.error||("HTTP "+r.status)};
  return j;}
/* The server no longer knows this browser's session (its data file was reset).
   Forget the token and start over: the page reloads to the join screen. */
let expiring=false;
function expired(){if(expiring)return;expiring=true;store.del("afeehy-token");setTimeout(()=>location.reload(),300);}

function docRef(path){return {id:lastOf(path),path,
  get:async()=>{await ready;return docSnap(path);},
  set:async data=>{const j=await api({op:"set",path,data});apply(path,j.data,j.rev);return copy(j.data);},
  update:async data=>{const j=await api({op:"update",path,data});apply(path,j.data,j.rev);return copy(j.data);},
  delete:async()=>{const j=await api({op:"delete",path});apply(path,null,j.rev);},
  onSnapshot(next){const off=listen(docL,path,next);ready.then(()=>safe(next,docSnap(path)));return off;},
  collection:sub=>colRef(path+"/"+sub,[])};}
function colRef(path,filters){const q={path,
  doc:id=>docRef(path+"/"+(id||Math.random().toString(36).slice(2,12))),
  where:(f,op,v)=>{if(op!=="=="&&op!=="eq")throw new TypeError("Only == filters are supported here.");return colRef(path,[...filters,[f,op,v]]);},
  orderBy:()=>q,limit:()=>q,
  get:async()=>{await ready;return colSnap(path,filters);},
  add:async data=>{const r=q.doc();await r.set(data);return r;},
  onSnapshot(next){const l={fn:next,filters};const off=listen(colL,path,l);ready.then(()=>safe(next,colSnap(path,filters)));return off;}};
  return q;}
const db={doc:docRef,collection:p=>colRef(p,[])};

const room={
  emit:async(topic,data)=>{await api({op:"emit",topic,data});},
  on:(topic,fn)=>listen(roomL,topic,fn),
  onConnection(fn){connL.add(fn);return ()=>connL.delete(fn);},
  connected:()=>connected};

const user={
  me:async()=>({id:me.id,name:"",avatarUrl:"",color:"",email:null,isOwner:me.role==="host",canEdit:me.role==="host"}),
  id:async()=>me.id,isOwner:()=>me.role==="host",canEdit:()=>me.role==="host",can:()=>true,name:async()=>""};

function setConn(c){if(c===connected)return;connected=c;connL.forEach(fn=>safe(fn,c));}
function connect(){
  const es=new EventSource("events?token="+encodeURIComponent(token));
  es.addEventListener("snapshot",e=>{const j=JSON.parse(e.data);
    const touched=new Set(mirror.keys());mirror.clear();revs.clear();
    for(const d of j.docs){mirror.set(d.path,d.data);revs.set(d.path,d.rev);touched.add(d.path);}
    setConn(true);readyRes();touched.forEach(notify);});
  es.addEventListener("doc",e=>{const d=JSON.parse(e.data);apply(d.path,d.data,d.rev);});
  es.addEventListener("room",e=>{const m=JSON.parse(e.data);
    const msg={topic:m.topic,data:m.data,by:m.by,peer:m.by,isMe:!!me&&m.by===me.id,sameTab:false,kind:"viewer",guest:false};
    (roomL.get(m.topic)||[]).forEach(fn=>safe(fn,msg));});
  es.onerror=()=>{setConn(false);
    // a plain server restart keeps every session, so normally we just reconnect;
    // when the stream is refused outright, first check the session still exists
    if(es.readyState===EventSource.CLOSED){es.close();setTimeout(resume,2000);}};
}
async function resume(){
  try{const j=await hello("");if(j.needCode||j.token!==token){expired();return;}}
  catch(e){} // server unreachable: keep trying
  if(!expiring)connect();
}

const hello=code=>api({op:"hello",hostKey:store.get("afeehy-host")||"",eventCode:code||""});

/* Event code screen: shown only to new browsers when the server sets EVENT_CODE. */
function askCode(){return new Promise(resolve=>{
  const show=()=>{
    const el=(tag,css,text)=>{const e=document.createElement(tag);if(css)e.setAttribute("style",css);if(text)e.textContent=text;return e;};
    const ov=el("div","position:fixed;inset:0;z-index:50;display:grid;place-items:center;padding:16px;background:var(--bg,#EEF0F6)");
    ov.setAttribute("role","dialog");ov.setAttribute("aria-modal","true");ov.setAttribute("aria-labelledby","code-title");
    const card=el("form","width:100%;max-width:440px;display:flex;flex-direction:column;gap:14px;padding:24px;border-radius:14px;"+
      "background:var(--surface,#fff);border:1px solid var(--line,#D3D8E6);color:var(--ink,#121831);font-family:var(--f-body,system-ui,sans-serif)");
    const eyebrow=el("div","font:600 11px/1 var(--f-mono,monospace);letter-spacing:.12em;text-transform:uppercase;color:var(--muted,#58607D)","Afeehy Hour");
    const title=el("h1","margin:0;font:700 22px/1.25 var(--f-display,system-ui,sans-serif)","Enter the event code");title.id="code-title";
    const help=el("p","margin:0;color:var(--muted,#58607D)","You'll find it in the company chat, next to the game link.");
    const label=el("label","display:flex;flex-direction:column;gap:6px;font-weight:600","Event code");
    const input=el("input","padding:14px 16px;border-radius:12px;border:2px solid var(--line,#D3D8E6);background:var(--bg,#EEF0F6);color:inherit;font:600 18px var(--f-mono,monospace);letter-spacing:.08em;text-transform:uppercase");
    input.id="event-code";input.autocomplete="off";input.maxLength=40;input.required=true;input.setAttribute("autocapitalize","characters");
    label.append(input);
    const msg=el("p","margin:0;min-height:1.4em;color:var(--bad,#C23B3B);font-weight:600");msg.setAttribute("role","alert");
    const btn=el("button","padding:12px 18px;border-radius:12px;border:0;background:var(--accent,#2D47F5);color:var(--accent-ink,#fff);font:700 16px inherit;cursor:pointer","Continue");
    btn.type="submit";
    card.append(eyebrow,title,help,label,msg,btn);ov.append(card);document.body.append(ov);input.focus();
    card.addEventListener("submit",async e=>{e.preventDefault();btn.disabled=true;msg.textContent="";
      try{const j=await hello(input.value.trim());
        if(j.needCode){msg.textContent="That code didn't work. Check the company chat and try again.";btn.disabled=false;input.select();return;}
        ov.remove();resolve(j);}
      catch(err){msg.textContent=err.code==="resource_exhausted"?"Too many tries. Wait a minute, then try again.":(err.message||"Can't reach the game server.");btn.disabled=false;}});
  };
  if(document.body)show();else document.addEventListener("DOMContentLoaded",show,{once:true});
});}

let bootError="";
const booted=(async()=>{
  let j=await hello("");
  if(j.needCode)j=await askCode();
  if(j.hostKeyRejected){store.del("afeehy-host");console.warn("Host key rejected; continuing as a player.");}
  token=j.token;store.set("afeehy-token",token);me=j;connect();await ready;return true;
})().catch(e=>{console.error(e);bootError=(e&&e.message)||"Can't reach the game server.";return false;});

window.claude={use:async name=>{const ok=await booted;if(!ok)return null;
  return ({db,room,user})[name]||null;},
  error:()=>bootError};
})();
