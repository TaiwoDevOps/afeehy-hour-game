# Afeehy Hour

The live game for Afeehy Hour, hosted by the mobile team. It has two activities on one public link:

- **Fastest Finger:** a host-run quiz. The fastest correct answer wins, and every answer is timed on the host's clock.
- **Myth or Fact:** a phone-myths debate with speaker timers and a room vote.

Players need **no account**. They open the link, enter the event code and type a name. The host opens the same link with a secret key and gets the controls.

It's one Go binary with **no dependencies** (standard library only). The page, fonts, the 61 quiz questions (everyday tech, mobile dev & code, phones) and the 11 debate claims are embedded in it, so the page makes **no third-party requests**. Strict security headers are on by default. **For Security/DevOps review, see `docs/SECURITY.md`.**

---

## 1. Run it (2 minutes)

You need Go 1.22 or newer.

```bash
# macOS / Linux
HOST_KEY=pick-a-secret EVENT_CODE=AFEEHY26 go run .

# Windows PowerShell
$env:HOST_KEY="pick-a-secret"; $env:EVENT_CODE="AFEEHY26"; go run .
```

`EVENT_CODE` is optional but recommended. New players must enter it before they can join (it isn't case-sensitive). Post it in the company chat **separately from the link**, so a forwarded link alone isn't enough. Hosts don't need it.

It prints two links:

```
Player link: http://localhost:8080/
Host link:   http://localhost:8080/?host=pick-a-secret   (keep this one private)
```

Open the **host link** once. The page remembers you as host on that browser and removes the key from the address bar. Co-hosts open the same host link on their own devices.

## 2. Make it public

### Setup a Linux Ubuntu VM on Digital Ocean called Droplet

> The idea here is replicable across any cloud infra or hosting platform.

The game keeps one long-lived streaming connection per browser (`/events`). **Not every tunnel passes that stream through.** Tested on 16 Oct 2026 from a network that blocks UDP 7844:

What we have done:

> Creating a server and setting up users and ssh into the server

1. first we got a droplet (Linux server running Ubuntu VPS) which provides us an IP address
2. by default, we got a root user and for that we setup a ssh on our local and exported it to the remote
3. this allowed us connect to the server from our local using the ssh (public key, root user and ip address and password)
4. since we could ssh into our server remotely, we were able to adduser "eg. newUser" with sudo access -aG usermod
5. to make the server a lot more protected against hackers and their bots, we had to revoke login access for root using password and ssh
6. but to do that, we needed to create a hidden directory on our remote server from our root access for the new user
7. copied our local machine pub key inside and then locked down the ownership and permissions
8. after confirming that the new user has sudo rights, we disabled the remote login and and authentication for root

> Next: Setting up ports and Firewalls on the Ip address

## 3. Running the session

1. Everyone opens the player link and types a name. They wait in a waiting room. **Nothing is live until you choose.**
2. On your host screen, open the tab you want first (Fastest Finger or Myth or Fact) and press **Open … for players**. Use **Move players here** to switch later.
3. **Quiz:** Select a question (only you see it, with the answer), then Start, then Stop. You also have Reset timer, +15 s / +30 s, End quiz and Reset scores.
4. **Debate:** Select a claim, then Show claim to players. Start each speaker's 60 s, then Open voting, Close voting, Reveal the truth.
5. Whoever starts a quiz question is its **timekeeper**. Keep that tab open and in front until you stop the question.

**After testing:** press **Clear players**. It removes every player, answer, vote and score, resets both activities and closes the live section, so every tester goes back to the name screen and then the waiting room. To wipe everything, including questions you added, stop the server and delete `afeehy-data.json`. The built-in questions are re-added on the next start, and any browser still open reloads itself to the join screen.

**Testing alone:** each separate browser or private window counts as a different player.

## 4. Configuration

| Env var       | Default                  | Meaning                                                                                                                                                                                                                           |
| ------------- | ------------------------ | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `HOST_KEY`    | random, printed at start | secret that turns a browser into a host                                                                                                                                                                                           |
| `EVENT_CODE`  | off                      | code new players must enter to join (10 wrong tries per minute per IP)                                                                                                                                                            |
| `PORT`        | `8080`                   | HTTP port                                                                                                                                                                                                                         |
| `DATA_FILE`   | `afeehy-data.json`       | where state is saved (every second, atomically)                                                                                                                                                                                   |
| `TRUST_PROXY` | off                      | set to `1` behind cloudflared, Render, Fly or a company proxy, so rate limits key on the real client IP (from `CF-Connecting-IP` / `X-Forwarded-For`). Leave off when browsers connect directly, or those headers could be forged |

## 5. How it fits together

```
main.go         server: page, POST /api, GET /events (Server-Sent Events), access rules, persistence
web/game.html   the game page (same page as the Claude artifact version)
page.go         assembles the page, bundled fonts, security headers (CSP by script hash)
web/shim.js     adapter giving the page its db / room / user API over this server, plus the event-code screen
web/fonts/      bundled fonts (Unbounded, Figtree, JetBrains Mono; OFL licences included)
seed/*.json     starting quiz questions and debate claims
docs/REQUIREMENTS.md   technical + UX requirements and the test checklist
docs/SECURITY.md       one-page brief for Security / DevOps
docs/DEPLOY-AWS.md     step-by-step deployment: local dry run with HTTPS, then an EC2 instance
deploy/                docker-compose.yml + Caddyfile used by that guide (copy the app files into deploy/app/)
```

- Each browser gets an anonymous id plus a secret token, stored in `localStorage`. Ids are public (they appear in the leaderboard data). Tokens are not.
- Players can write only their own name, answer and vote, which are validated and trimmed on the server. They can broadcast only quiz answers, and those reach host browsers only. Everything else is host-only.
- Answer keys, debate verdicts and timing records are never sent to player browsers. Wrong host keys and wrong event codes are both limited to 10 per IP per minute.
