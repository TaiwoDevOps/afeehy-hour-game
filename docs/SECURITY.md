# Afeehy Hour game: security brief

**For:** Security and DevOps review
**From:** Mobile team (host of today's Afeehy Hour)
**Ask:** approve a one-hour internal game session, and ideally a company subdomain to host it on.

## What it is
A small web page for a one-hour, company-wide social session: a live quiz and a "Myth or Fact" vote about phones. Colleagues open a link in their browser, enter an event code and a display name, and tap answers. The session is a few dozen people for one hour. It runs today and is then shut down and deleted.

## What runs on participants' laptops
- **Only a web page in the browser.** Nothing is downloaded, installed or run outside the browser.
- **No login and no personal data.** Participants type a display name (max 32 characters, a nickname is fine). There's no email, no password, and no other personal or company data.
- **No permissions requested.** Camera, microphone, location, clipboard-read, payment, USB, Bluetooth and screen capture are all explicitly disabled through `Permissions-Policy`.
- **No third parties.** The page loads nothing from any other site: no CDNs, no analytics, no trackers, no ads. Fonts are bundled and served by the app (OFL licensed). This was verified in a browser test that recorded zero outside requests.
- **Browser storage:** one random anonymous ID and session token in `localStorage`, used to keep the player's score across a page reload. There are no cookies.

## Data held on the server
| Data | Example | Purpose |
|---|---|---|
| Display name per anonymous ID | "Ada" | leaderboard |
| Quiz answers (choice A–D) and votes (Myth/Fact) | `choice: 2` | scoring |
| Scores and game state | points, current question | running the game |
| Question bank and claims | built-in trivia | content (visible to hosts only) |

Everything is stored in one JSON file next to the server. **Retention:** the file is deleted right after the event.

## Access control
- **Players** can change only their own name, answer and vote. The server validates the type and size of each and refuses everything else (HTTP 403).
- **Hosts** (the mobile team) use a secret host key. Answer keys and timing records are never sent to player browsers, and players' live answers are delivered to host browsers only.
- **Event code:** a new browser can't join without the code, which is posted only in the company chat. Wrong codes and wrong host keys are rate-limited together to 10 per minute per IP. Proxy headers (`CF-Connecting-IP`, `X-Forwarded-For`) are used for that only when `TRUST_PROXY` is set, so they can't be forged from the outside. The session cap is 2,000.
- **Rate limits:** players are limited to 20 requests per second, request bodies to 1 MB and live messages to 8 KB.

## Browser hardening (sent on every response)
- `Content-Security-Policy`: `default-src 'none'`. Scripts are allowed **only by SHA-256 hash** of the app's own two inline scripts. Fonts, images and connections are same-origin only. `frame-ancestors 'none'`, `form-action 'none'`, `base-uri 'none'`, `object-src 'none'`.
- `X-Frame-Options: DENY`, `X-Content-Type-Options: nosniff`, `Referrer-Policy: no-referrer`, `Cross-Origin-Opener-Policy` and `Cross-Origin-Resource-Policy: same-origin`, plus `Strict-Transport-Security` over HTTPS.
- All user-entered text is rendered as plain text (`textContent`). The app never uses `innerHTML`, `eval` or dynamically loaded code.

## Server
- A single Go binary using **only the standard library**, with no third-party dependencies. The server is about 760 lines of Go, and the page is plain HTML and JavaScript with no frameworks. All of it is available for review.
- A Dockerfile is included (Alpine base, runs as an unprivileged user, one exposed port `8080`, data on a `/data` volume). It needs **no outbound internet access** and no database. Set `TRUST_PROXY=1` when it runs behind your proxy.
- Endpoints: `GET /` (page), `GET /fonts/*`, `POST /api`, `GET /events` (Server-Sent Events), `GET /healthz`.

## Hosting options
1. **Preferred: company subdomain** (for example `afeehy.<company-domain>`), deployed by DevOps from the Docker image behind your existing TLS. That gives no new domain reputation questions and no tunnels.
2. **Fallback: a managed host** (Render or Fly.io), which gives a normal HTTPS URL on the provider's servers.
3. **Last resort: a Cloudflare quick tunnel** from a mobile-team member's **personal** machine, never a company device. Traffic is HTTPS end to end to Cloudflare. We know `*.trycloudflare.com` addresses have poor reputation and may be blocked by the web filter. If they are, we'll use option 1 or 2 rather than ask for an exception.

## Teardown
Right after the session: stop the server, delete `afeehy-data.json`, and remove the deployment or tunnel. The link stops working immediately.

## Known limits (accepted for a social game)
- Anyone holding both the link and the event code can join under any display name.
- Anyone with the host key becomes a host. It's shared only within the mobile team, and can be revoked by restarting with a new key.
- Quiz response times include each player's network delay.
