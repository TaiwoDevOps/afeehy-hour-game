# Afeehy Hour — Technical & UX Requirements

**Scope:** the live Afeehy Hour web app (one public link) with two activities, **Fastest Finger** (quiz) and **Myth or Fact** (debate), served by a small Go server.
**Purpose:** review and test reference. Each requirement has an ID so test results and bugs can point at it.
**Status:** describes the build as shipped (**version 7**). Known gaps are listed in §7. They are not bugs to rediscover.

### Changelog
- **v7.1:** Content refresh for the event. "Science" became **Everyday tech** (passwords, Wi-Fi, phishing, OTPs, the cloud) and "Mobile dev" became **Mobile dev & code** with eight plain-language data-structure questions (stacks, queues, binary search, Big O). Category key `science` → `tech`.
- **v7:** Review fixes. The 20 "Phones & devices" questions had ids that clashed with "Mobile dev" (`dev01`–`dev20`), which made them unselectable; they are now `dvc01`–`dvc20` and a saved bank with the clash is replaced on start. Wrong host keys are rate-limited like wrong event codes (ACC-9), proxy headers are trusted only with `TRUST_PROXY` set, and `answer` broadcasts reach hosts only (ACC-5/6). The server now assigns `seq` for shared state, so co-hosts can't collide (GL-7). A browser whose session the server no longer knows reloads to the join screen instead of showing "Reconnecting" forever (LIM-3). Keyboard shortcuts ignore Ctrl/Cmd/Alt combinations (UX-3). **Clear players** also resets both activities and closes the live section (QZ-15). Long names no longer widen the page (UX-1), the host's question bank stacks at phone width, add forms keep focus while typing, and the connection status is announced (UX-4). Player names can't be blank-looking or contain line breaks (ACC-4).
- **v6:** Security hardening for use on company laptops. Added an optional **event code** to join (ACC-9), **bundled fonts** so the page makes no third-party requests (ACC-10), and **strict security headers**, including a CSP that allows scripts only by hash (ACC-11). Added `docs/SECURITY.md` for Security and DevOps.
- **v5:** Moved off Claude artifacts to a **standalone Go server**, so anyone can play from a public link with **no account**. Hosts are identified by a secret host link. **Nothing is live by default:** players wait in a waiting room until the host opens the first activity. Fixed stray "null" text. Added **Clear players** for after testing.
- **v4:** Quiz timing moved to the **host's clock**. Response times, the time limit and late answers are now decided on the host's device. Players' countdowns follow the host's every second. This fixes the reload advantage, device-clock tricks and self-reported times (old LIM-1, LIM-2, LIM-3). New items: QZ-16, QZ-17, QZ-18, the `hostdata/round` record, and the `answer` / `tick` live topics.
- **v3:** Split into two sections (Quiz, Debate) and added the debate.

---

## 1. Roles & access

| ID | Requirement |
|---|---|
| ACC-1 | Anyone with the **player link** can open the page and play. No account or sign-in is needed. Each browser gets an anonymous id and a secret token, kept in that browser. |
| ACC-2 | **Host** = a browser that has opened the **host link** (`/?host=<HOST_KEY>`) at least once. The key is removed from the address bar and remembered on that browser. Hosts get host controls, the question bank and the answer key. Co-hosts use the same host link. |
| ACC-3 | A wrong host key is ignored. That browser carries on as a player. |
| ACC-4 | Players can write only **their own** name, quiz answer and debate vote. The server validates and trims these (name ≤32 chars, choice 0–3, pick myth/fact). Control and invisible formatting characters are removed from names and whitespace is collapsed, so a name can't look blank or span lines. Anything else returns 403. |
| ACC-5 | Players can broadcast only the `answer` topic, and the server checks its shape and delivers it to **host browsers only**. All other live topics are host-only. |
| ACC-6 | The question bank, claims list (answers and verdicts) and round timing record are **never sent to player browsers**. Other players' quiz answers aren't sent while a round is open. After Stop, the closed-round state carries each player's choice and host-measured time (see LIM-13). |
| ACC-7 | Players are limited to 20 requests per second. Request bodies are capped at 1 MB and live messages at 8 KB. |
| ACC-8 | Only hosts can change game state, scores, the live activity, the bank, the claims or the player list. |
| ACC-9 | **Event code** (when `EVENT_CODE` is set). A new browser must enter the code before it gets a session. The check ignores case and surrounding spaces. A wrong code shows "That code didn't work". Wrong codes and wrong host keys share one limit: after 10 in a minute per IP, "Too many tries". The client IP comes from proxy headers only when `TRUST_PROXY` is set. The host link skips the code, and browsers that already joined aren't asked again. Sessions are capped at 2,000. |
| ACC-10 | **No third-party requests.** Fonts (Unbounded, Figtree, JetBrains Mono, OFL) are served by the app itself. The page loads nothing from any other origin. |
| ACC-11 | **Security headers on every response:** CSP `default-src 'none'`, scripts only by SHA-256 hash, same-origin fonts/images/connections, `frame-ancestors 'none'`, `form-action 'none'`, `base-uri 'none'`. Also `X-Frame-Options: DENY`, `nosniff`, `Referrer-Policy: no-referrer`, COOP/CORP same-origin, a `Permissions-Policy` that turns off camera, mic, location and similar, and HSTS over HTTPS. |

## 2. Global shell

| ID | Requirement |
|---|---|
| GL-1 | On first visit a player must enter the event code (when one is set), then a display name (max 32 chars) before seeing any activity. The name is prefilled with the last name used on that device. |
| GL-2 | One name is used across both activities. Players can **Rename** at any time from the header chip. |
| GL-3 | The page has two sections, **Fastest Finger** and **Myth or Fact**, shown as tabs. Their content, controls and data are fully separate. |
| GL-0 | **Nothing is live until the host chooses.** After joining, players wait in a **waiting room** that shows who's there. The host sees "Nothing is live yet", picks either tab, and presses **Open … for players**. |
| GL-4 | Players see **only the live section**. They can't switch tabs, and the live tab shows a "Live" marker. When the host moves the room, every player switches automatically and gets a screen-reader announcement. |
| GL-5 | Hosts can open either tab freely. Opening a tab doesn't move players. If the host is viewing a section that isn't live, a banner says so and offers **Move players here**. |
| GL-6 | The status strip shows the clock, the player count and the connection state: Live, Synced, Reconnecting or Offline. |
| GL-7 | State changes reach players through two paths: an instant broadcast and the stored state, which acts as the backup. The newest state always wins, decided by a sequence number that the **server assigns** on every write to `game/state`, `debate/state` and `meta/live`. A dropped broadcast must not leave a player stuck. |

## 3. Fastest Finger (quiz)

### 3.1 Flow & phases
`lobby → (host selects, private) → question → closed → … → final`

| ID | Requirement |
|---|---|
| QZ-1 | **Lobby.** Players see "Waiting for the host", the scoring rules and the list of joined names. |
| QZ-2 | **Select (private).** When the host selects a question it is previewed **only to that host**, with the correct answer highlighted. Players see nothing. Other co-hosts don't see the selection either. |
| QZ-3 | **Start.** The question number, category, text and four options appear for all players with a 20 s countdown. The host who pressed Start becomes the question's **timekeeper**: its device records the start time and the deadline. The question is marked "Asked" in the bank. |
| QZ-4 | **Answering.** A player taps an option (or presses A–D / 1–4). The answer **locks immediately** and can't be changed. The player sees "Locked in: X" and is told their time is measured by the host. The host also keeps only the **first** answer it receives from each player, so changing it later through dev tools has no effect. |
| QZ-5 | **Response time** = the moment the answer reaches the timekeeper's device minus the moment the question started, both measured on the timekeeper's clock. Players' own clocks and reported times are never used. Network delay is included (see LIM-1). Resetting the timer does **not** reset the response time. |
| QZ-6 | **Countdown sync.** The timekeeper broadcasts the time remaining every second, and every player's countdown snaps to it. After a reload, a player's countdown matches the host's within about 1 s. |
| QZ-7 | **Deadline.** The deadline is held by the timekeeper. An answer reaching the timekeeper more than **1 s (grace)** after the deadline is marked **late** and scores 0. At 0 s, players who haven't answered see their options disabled and "Time's up". The round stays open until the host stops it. |
| QZ-8 | **Host live view.** The host sees the question with the answer key, an "answered / players" counter, and answers **in the order they arrived**, each with its host-measured time. Answers are tinted correct, wrong or late. |
| QZ-9 | **Reset timer.** Sets the deadline to 20 s from now for everyone. Players who already answered stay locked. |
| QZ-10 | **+15 s / +30 s.** Pushes the deadline back. If the deadline had already passed, it opens a fresh window of that length, which reopens answering for players who hadn't answered. |
| QZ-11 | **Stop question.** The round is scored and the phase becomes `closed`. The question text and options are **removed from shared state**, so players can no longer see them. |
| QZ-12 | **Closed (player).** Players see the winner's name and time, the answered/correct/late counts, and their own result: fastest (+10, with their time), correct (+3, with their time), wrong (0), too late (0) or didn't answer. |
| QZ-13 | **Closed (host).** The host still sees the question, the answer key and how many players picked each option (late answers aren't counted in the option totals). |
| QZ-14 | **End quiz** asks for confirmation first. If a question is live it is scored first, then everyone sees the **final podium** (top 3). **Reopen quiz / Back to lobby** returns to the lobby and keeps the scores. |
| QZ-15 | **Reset scores** asks for confirmation, clears all quiz scores and the "Asked" marks, and returns to the lobby. Players stay joined. **Clear players** asks for confirmation, then removes every player, answer, vote and score, resets the quiz and the debate, and closes the live section. Anyone still on the page goes back to the name screen and then the waiting room. |
| QZ-16 | **Timekeeper only.** Reset timer and +15/+30 s are available only to the timekeeper. Another host sees "Another host is timing this question" with **Take over timing** and **Stop question**. |
| QZ-17 | **Take over timing.** Loads the timing record so far, makes this host the timekeeper, and continues stamping and ticking from there. |
| QZ-18 | **Timekeeper reload.** If the timekeeper reloads mid-question, the start time, deadline and answers stamped so far are recovered from the timing record, and ticking resumes. |

### 3.2 Scoring

| ID | Requirement |
|---|---|
| SC-1 | The fastest correct, on-time answer (lowest host-measured time) earns **+10** and a **win**. |
| SC-2 | Every other correct, on-time answer earns **+3**. A wrong answer, a late answer or no answer earns 0. |
| SC-3 | The leaderboard sorts by score (desc), then wins (desc), then name (A–Z). |
| SC-4 | The leaderboard shows rank, name, score, wins and correct count, with the current player highlighted. Joined players with 0 points are listed. |
| SC-5 | Scores update only when the host stops a question. They never change mid-round. |
| SC-6 | A question can be asked again ("Ask again"). Each asking is timed and scored independently. |
| SC-7 | At Stop, any stored answer the timekeeper never saw arrive is stamped at that moment. It is therefore normally late. |

### 3.3 Question bank (host only)

| ID | Requirement |
|---|---|
| QB-1 | The bank starts with 61 questions with unique ids: Everyday tech 20 (`tech…`), Mobile dev & code 21 (13 mobile `dev…` plus 8 easy data-structures-and-algorithms `dsa…`), Phones & devices 20 (`dvc…`). The correct option position is shuffled across A–D. |
| QB-2 | Category filter tabs show counts. Unasked questions sort above asked ones. Each row shows the category and the answer. |
| QB-3 | **Add a question:** text (≤160), four options (≤60 each), correct option and category, all required. A new question appears at the top of the bank. |
| QB-4 | **Remove** asks for confirmation. A question that is currently live can't be removed. |
| QB-5 | Selecting another question is disabled while a question is live. |

## 4. Myth or Fact (debate)

### 4.1 Flow & phases
`idle → (host selects, private) → claim → voting → closed → revealed → …`

| ID | Requirement |
|---|---|
| DB-1 | **Idle.** Players see a 4-step "how it works" explainer. |
| DB-2 | **Select (private).** The host previews the claim with its verdict and explanation. Players see nothing. |
| DB-3 | **Show claim.** All players see the claim number and claim text. |
| DB-4 | **Speakers.** The host sets a Myth-side and a Fact-side name, by typing it or tapping **Random player**. Random never picks the person already on the other side. |
| DB-5 | **Start 60 s.** Players see a colored "Now arguing MYTH/FACT · Name" banner with a countdown. Starting the other side replaces the current speaker. |
| DB-6 | **Speaker timer controls:** Restart 60 s, +15 s, +30 s (which starts a fresh window if the timer had expired) and Stop speaker. At 0 the banner shows "Time's up". Nothing else is blocked. |
| DB-7 | **Open voting.** Players see Myth and Fact buttons, or can press M / F. They can **change their vote** until voting closes. |
| DB-8 | During voting players see only the **number of votes**, never the split. The host sees the live split and a "voted / players" counter. |
| DB-9 | **Close voting.** Votes are tallied and everyone sees the Myth/Fact split with percentages and counts. Players see "about to reveal". |
| DB-10 | **Reveal the truth.** Everyone sees the verdict (Myth, Fact, It depends, or No right answer) and the explanation. |
| DB-11 | For Myth or Fact verdicts, each player sees "You called it", "The room fooled you this time" or "You didn't vote", plus the % of the room that got it right. "It depends" and "No right answer" verdicts show no right/wrong message. |
| DB-12 | **Clear the stage** returns to idle. **Reset debate** asks for confirmation, then restarts claim numbering and clears the "Debated" marks. |
| DB-13 | The debate has **no points**. It never touches the quiz leaderboard. |

### 4.2 Claims list (host only)

| ID | Requirement |
|---|---|
| CL-1 | The list starts with 11 claims. The last, "iPhone is better than Android", has the verdict *No right answer*. |
| CL-2 | **Add a claim:** text (≤140), verdict, and reveal explanation (≤280), all required. A new claim appears at the top. |
| CL-3 | **Remove** asks for confirmation. The live claim can't be removed. |
| CL-4 | Selecting a new claim is disabled while a claim is in the claim, voting or closed phase. The host must reveal it or clear the stage first. |

## 5. UX & accessibility

| ID | Requirement |
|---|---|
| UX-1 | **Responsive.** Usable at 400 px width with no horizontal scrolling. Two-column layouts stack on phones, and the leaderboard and vote panel move below the stage. |
| UX-2 | **Themes.** The app follows the device's light/dark setting, and both themes are fully legible. |
| UX-3 | **Keyboard.** Every control is reachable by Tab and shows a visible focus ring. Quiz answers: A–D / 1–4. Debate votes: M / F. Shortcuts are ignored while typing in a field and when Ctrl, Cmd or Alt is held. |
| UX-4 | **Screen readers.** New questions, timer resets and added time, round results, claim changes, voting open/close, verdicts and section switches are announced through a live region. The connection state (Live, Reconnecting, Offline) is a status region. Option buttons expose their letter, text and pressed state. |
| UX-5 | **Color isn't the only signal.** Options carry letters (A–D), vote buttons carry M/F plus labels, and correct/wrong/late states use text as well as color. |
| UX-6 | **Reduced motion.** With the OS setting on, all animation (pulses, hovers, bar fills) is turned off. |
| UX-7 | **No browser pop-ups.** Every destructive action uses an inline confirm with Cancel. |
| UX-8 | **Feedback.** Every host action shows a short toast. Failed writes say what to do ("didn't save, check connection") and roll back the optimistic UI. A failed join offers **Try again** and **Reload**. The host's add-question and add-claim forms are not rebuilt while the host is typing in them. |
| UX-10 | **No stray text.** Empty slots in the layout never show up as "null". |
| UX-9 | **Touch targets.** Answer and vote buttons are at least 68 px tall. Other buttons are at least 36 px. |

## 6. Technical notes

**Stack:** one Go binary (standard library only) that serves the page and a small shared document store:

| Endpoint | Purpose |
|---|---|
| `GET /` | the game page (game + adapter, embedded in the binary) |
| `POST /api` | `{op: hello \| set \| update \| delete \| emit}` with header `X-Token`. `hello` issues or returns the browser's id and token, plus host role if the key matches |
| `GET /events?token=` | Server-Sent Events: a `snapshot` of every document this browser may read, then `doc` changes and `room` messages as they happen, with a heartbeat every 15 s |
| `GET /healthz` | liveness check |

- **Adapter (`web/shim.js`).** The page keeps the same `claude.use("db" | "room" | "user")` API it had as a Claude artifact. The adapter backs it with a live local mirror of the store, so the game logic is unchanged.
- **Persistence.** Documents and sessions are saved to `DATA_FILE` every second (written to a temp file, then renamed). On restart, players keep their ids and the game resumes. The question bank and claims are seeded on first start.
- **Ordering.** Every write gets a global revision number, and the adapter drops older revisions. A browser that falls behind is dropped and reconnects to a fresh snapshot.
- **One instance only.** All state lives in one process.

**Data model**

| Path | Contents | Read | Write |
|---|---|---|---|
| `meta/live` | `{activity: "quiz"\|"debate", seq}`. Missing means nothing is live and players wait. | everyone | host |
| `game/state` | quiz phase, askId, `driver` (timekeeper's id), timerKey, limitMs, qnum, current question (question phase only), used ids; after Stop: `res {uid: [choice, ms, late]}`, tally, counts, winner | everyone | host |
| `scores/board` | `{players: {uid: {name, score, wins, correct}}}` | everyone | host |
| `players/{uid}` | `{name, at}`, validated server-side | everyone | that player / host |
| `answers/{uid}` | `{askId, choice, name, at}`, one doc per player, overwritten each round. It holds **no timing**. | host + that player | that player / host |
| `hostdata/round` | timekeeper's record: `{askId, startAt, windowEnd, stamps: {uid: {c, ms, late}}}`, all on the host's clock | host | host |
| `debate/state` | debate phase, claim text, speaker, names, tally, verdict (after reveal only) | everyone | host |
| `votes/{uid}` | `{claimId, pick, name, at}` | everyone | that player / host |
| `hostdata/bank`, `hostdata/claims` | question bank and claims with answers/verdicts | host | host |

**Live topics (instant messages over `/events`, never stored)**

| Topic | Sender | Payload | Purpose |
|---|---|---|---|
| `state`, `debate`, `meta` | host only | full state | instant delivery of state changes (stored copy is the backup) |
| `tick` | host only (timekeeper) | `{askId, timerKey, remainingMs}` every 1 s | keeps every countdown on the host's clock |
| `answer` | players | `{askId, choice}` | lets the timekeeper stamp an answer the moment it's sent. The server attaches the sender's id from their session token, so it can't be forged. |

**Timing rules**
- On Start, the timekeeper records `startAt = now` and `windowEnd = now + 20 s` (its own `Date.now()`) in `hostdata/round`.
- When a player answers, they send an `answer` message **and** write `answers/{uid}`. The timekeeper stamps whichever arrives first: `ms = now − startAt` and `late = now > windowEnd + 1000`. It ignores anything after the first stamp for that player.
- Reset timer: `windowEnd = now + 20 s`. Extend: `windowEnd += N` (or `now + N` if it had already passed).
- Stop: stored answers that were never stamped are stamped now. The round is scored from the stamps only and the scoreboard is written once. `hostdata/round` keeps the final stamps for review.
- The server stamps `seq` (its global revision) on every host write to `game/state`, `debate/state` and `meta/live`, and clients ignore any state with a lower or equal `seq`. Answers and votes are tied to a per-round id, so stale documents from earlier rounds are ignored.
- Storage limit is 25,000 documents. Usage is roughly 3 docs per player plus a few fixed docs, so there's no practical limit for one session.

## 7. Known limitations

| ID | Limitation | Impact / workaround |
|---|---|---|
| LIM-1 | Response time includes the network delay between each player and the timekeeper. That delay is usually well under 0.5 s, but a player on a slow connection is slightly disadvantaged. | This is inherent to any fair server-side clock. Ties within a few hundred ms are effectively photo finishes. |
| LIM-2 | **The timekeeper's page must stay open** during a question. While it's closed or asleep, nobody stamps arrivals. Answers sent in that gap are stamped when the page comes back (QZ-18) or at Stop (SC-7), so they're usually late. | The timekeeper should keep the tab in front and the device awake. A co-host can **Take over timing**. |
| LIM-3 | If a browser loses its connection, the status strip shows "Reconnecting", and it resyncs automatically when the connection returns. While a player is disconnected their countdown runs on its own estimate. If the server comes back with a fresh data file, the page reloads itself to the join screen. | Reload if it's stuck. |
| LIM-4 | `votes` is sent to every browser. The UI hides the live split, but dev tools could show it. | Low risk. Could be moved to host-only plus a published tally. |
| LIM-9 | **Anyone with the player link and the event code can join under any name.** There's no sign-in, so two people could pick the same name. | Post the link and the code separately in the company chat. Use **Clear players** if strangers join. |
| LIM-12 | A Cloudflare quick-tunnel address (`*.trycloudflare.com`) has poor domain reputation and may be blocked by company web filters. Tunnel software may also be flagged on company devices. | Prefer a company subdomain or a managed host. Run any tunnel only on a personal machine. See `docs/SECURITY.md`. |
| LIM-10 | **The host link is the only host credential.** Anyone who gets it becomes a host. | Keep it private. To revoke it, restart the server with a new `HOST_KEY`. Existing host browsers stay hosts until `afeehy-data.json` is deleted. |
| LIM-11 | Clearing `localStorage` gives a player a new identity. Their old score stays under the old entry. | Cosmetic. Use Clear players after testing. |
| LIM-5 | If several co-hosts change state at the same moment, the last write wins (every client agrees on which one, since the server numbers them). A host's private selection isn't shared. | Agree on one person "driving" each section. |
| LIM-13 | After a round closes, `game/state` carries every player's choice and host-measured time, keyed by anonymous id, so dev tools could show who picked what. During the round nothing about other players' answers is sent. | Accepted for a social game. Could be moved to per-player result documents at the cost of one write per player at Stop. |
| LIM-14 | Session creation isn't limited per IP (offices share one address), so anyone holding the event code could fill the 2,000-session cap. Without `EVENT_CODE`, anyone with the link could. | Always set `EVENT_CODE`. Restart with a new code if it leaks. |
| LIM-6 | Renaming updates the leaderboard immediately. Names stored with past scores refresh at the next scored round. | Cosmetic. |
| LIM-7 | Reset scores and Reset debate don't delete old answer or vote documents. They're ignored because each round has its own id. | None needed. |
| LIM-8 | The debate speaker timer still counts down on each device. It isn't host-synced because the debate isn't scored. | Visual only. |

## 8. Test checklist

Use **3 browsers** (separate browsers or private windows): Host (opened with the host link), Player A and Player B. Add a second host browser to test take-over. Test on at least one phone, over the public link.

**Access & join**
- [ ] Open the player link in a private window with no account. Enter the event code, then a name, and you join (ACC-1, ACC-9)
- [ ] A wrong event code is rejected. Lower-case of the right code is accepted. After 10 wrong tries in a minute you're told to wait (ACC-9)
- [ ] Ten wrong host keys in a minute (open `/?host=wrong` repeatedly in private windows) also lead to "Too many tries" (ACC-9)
- [ ] Select each "Phones & devices" question: the preview shows that question, not a Mobile dev one (QB-1)
- [ ] As a player, hold Cmd/Ctrl and press A or F: nothing is answered or voted (UX-3)
- [ ] Stop the server, delete `afeehy-data.json`, start it again: open pages reload to the event-code screen within a few seconds (LIM-3)
- [ ] A browser that already joined isn't asked for the code again after a reload (ACC-9)
- [ ] In dev tools Network tab, every request goes to the game's own address, with no Google, CDN or analytics requests (ACC-10)
- [ ] The response headers include the `Content-Security-Policy` and `X-Frame-Options: DENY`, and the console shows no CSP violations (ACC-11)
- [ ] **On an official laptop**, the public link opens and isn't blocked by the company web filter
- [ ] The host link turns that browser into host, and the key disappears from the address bar (ACC-2)
- [ ] A wrong host key leaves you as a player (ACC-3)
- [ ] After joining, players wait in the waiting room, and nothing is live until the host opens an activity (GL-0)
- [ ] The host can open **either** activity first (GL-0)
- [ ] Player joins with a name, and the name appears in the lobby and leaderboard for everyone (GL-1)
- [ ] Rename updates the header chip and leaderboard (GL-2)
- [ ] As a player, confirm in dev tools that you can't read `hostdata/*` or others' answers, and that writing game state, another player's doc, or broadcasting `state` fails with 403 (ACC-4/5/6)
- [ ] **Clear players** sends everyone back to the name screen and then the waiting room, with scores and both activities reset (QZ-15)

**Section switching**
- [ ] Host opens the Debate tab and players stay on the Quiz (GL-5)
- [ ] Host clicks **Move players here** and both players switch within about 1 s (GL-4)

**Quiz: core**
- [ ] Host selects a question and players see nothing (QZ-2)
- [ ] Start shows the question to both players with a 20 s timer (QZ-3)
- [ ] Player A answers correctly and fast, Player B answers correctly and slower. The host's arrival list shows A before B with times. After Stop: A gets +10 and a win, B gets +3 (QZ-8, SC-1/2)
- [ ] A wrong answer gets 0, and so does no answer
- [ ] An answer locks and can't be changed (QZ-4)
- [ ] After Stop, players can't see the question text anywhere, including after a reload (QZ-11)
- [ ] The host's closed view shows the per-option counts (QZ-13)
- [ ] Add a custom question, ask it, and score it (QB-3)
- [ ] Remove a question, with confirmation (QB-4)
- [ ] End quiz during a live question: it's scored, then the podium shows (QZ-14)
- [ ] Reset scores sets everything to 0 and players remain (QZ-15)

**Quiz: timing (v4)**
- [ ] **Reload advantage gone:** Player A reloads mid-question and answers immediately. A's recorded time is still counted from the original start, so it's larger than A's apparent time since the reload (QZ-5)
- [ ] **Countdown sync:** after a player reloads, their countdown matches the host's within about 1 s (QZ-6)
- [ ] **Device clock:** move Player B's device clock forward or back by minutes. Their countdown and recorded times are unaffected (QZ-5/6)
- [ ] **Late answer:** let the timer reach 0 and confirm the options are disabled. Then, in dev tools, write an answer for Player A: the host marks it **late** and A gets 0 and "Too late" (QZ-7, SC-2)
- [ ] **Forged time:** in dev tools, write `ms: 1` into your own answer doc. It's ignored, and only the host-measured time counts (QZ-4/5)
- [ ] **Changed answer:** after locking, rewrite your choice in dev tools. The host keeps the first choice (QZ-4)
- [ ] **Reset timer** restarts 20 s for everyone, and the already-answered player stays locked (QZ-9)
- [ ] **+15 s** after expiry reopens answering, and an answer then counts on time (QZ-10)
- [ ] **Timekeeper reload:** the host reloads mid-question, the arrival list and countdown come back, and scoring is correct (QZ-18)
- [ ] **Take over:** the second host sees "Another host is timing", takes over, and can Reset timer, extend and Stop correctly (QZ-16/17)

**Debate**
- [ ] Select a claim and players see nothing. Show claim makes it visible (DB-2/3)
- [ ] Random player fills a name and never duplicates the other side (DB-4)
- [ ] Start a speaker and players see the banner and countdown. Test +15 s, Restart and Stop (DB-5/6)
- [ ] Open voting: a player votes, switches the vote, and only the count is visible to players (DB-7/8)
- [ ] Close voting shows the split to everyone (DB-9)
- [ ] Reveal shows the verdict and explanation, plus "You called it" or "fooled you" correctly (DB-10/11)
- [ ] Reveal the opinion claim (iPhone vs Android) and confirm no right/wrong message (DB-11)
- [ ] Quiz scores are unchanged after the debate (DB-13)

**UX**
- [ ] Phone width: no sideways scroll, and the panels stack, including with a 32-character name with no spaces and on the host's question bank (UX-1)
- [ ] Dark and light themes are both readable (UX-2)
- [ ] Keyboard-only play: A–D and M/F work, and focus is visible (UX-3)
- [ ] Screen reader announces a new question, added time and the verdict (UX-4)
