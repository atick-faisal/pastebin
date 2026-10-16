---
slug: multi-box-sync
title: Multi-Box Sync
status: shipped
created: 2026-10-16
---

# Multi-Box Sync

## Summary

Turn the single shared text box into an ordered set of boxes. Each box has a
button that copies its whole content. Any visitor can add or remove a box, and
every connected browser sees that change, just as it already sees text edits.
This closes the two success criteria in `docs/goal.md` that the first slice
deferred: a copy button per box, and synchronized add/remove.

The server stays the single source of truth. Edits to one box are applied in the
order they arrive, and the last one received wins for that box. Adding and
removing boxes are ordered the same way. Nothing is merged, and state still lives
only in memory.

## Tech Stack / Approach

- Same stack as `live-shared-text`: Go standard library, plain HTML and a little
  JavaScript, assets embedded with `go:embed` (`internal/httpserver/server.go:17`),
  no frontend build and no new dependencies.
- **State.** `internal/textstate/state.go:18-53` holds one `Snapshot{Text, Revision}`
  today. It becomes an ordered list of boxes, each with an ID and its text.
  The 10 MiB limit (`state.go:11`) applies to each box. A box-count limit is added
  (see Q5) so that public visitors cannot grow memory without bound on the shared
  VPS (`docs/goal.md:36-37`).
- **Ordering.** Keep the existing pattern where one mutex covers both accepting a
  change and publishing it (`server.go:71-91`). Add, remove and text writes all go
  through it, so revision order still matches arrival order.
- **Protocol.** Today there are two routes: `POST /text` and `GET /events`
  (`server.go:40-41`). They become routes for adding a box, removing a box, and
  writing one box's text, plus the existing SSE stream. What the stream carries
  is Q3. Connecting or reconnecting still starts with a full snapshot, and
  `Last-Event-ID` is still ignored (`server.go:133-134`). The body size limit
  (`server.go:111`) gets a small allowance for the box ID on top of 10 MiB.
- **Slow browsers.** Today a slow browser's one-slot queue is refilled with the
  newest full snapshot (`server.go:81-89`). Whatever Q3 picks, outgoing work per
  browser must stay bounded and must still end with the browser holding the
  current state.
- **Browser client.** `internal/httpserver/web/app.js:3-100` keeps one set of
  module-wide variables (`text`, `latest`, `writing`, `pending`, `failure`). Each
  box needs its own copy of that write queue and draft tracking. The existing
  reconnect behaviour (`app.js:91-100`) and stale-result guards (`app.js:51,61`)
  are reused per box. `index.html:17-18` changes from one static textarea to boxes
  rendered from server state.
- **Copy button.** Uses `navigator.clipboard.writeText`, which works on HTTPS
  (production, `docs/goal.md:38`) and on localhost. When it is unavailable or
  fails, such as over plain HTTP on a LAN, the box shows a short failure message
  instead of failing silently. The copy button needs no protocol change, so it can
  land first.
- **Tests.** The 9 Go tests in `internal/httpserver/server_test.go` fix the
  single-text protocol in place. They are rewritten to cover per-box ordering,
  add/remove ordering, the box limit, writes to removed boxes, five subscribers
  converging, and slow subscribers. `scripts/browser-check.mjs:73-200` is
  hardcoded to `#text` and is rewritten for multiple boxes, copy, and add/remove
  across two browsers.

## Open Questions

## Resolved

### Q1: Who assigns box IDs, and how are boxes ordered?

- [x] Server assigns IDs from a counter; boxes appear in creation order with no reordering (recommended) — the server is already authoritative, a counter is simple and readable, and creation order needs no extra controls (`docs/goal.md:31`).
- [ ] Clients generate random IDs and send them when adding — a browser could show the new box before the server confirms it, but the server must check that IDs are unique and well-formed, and nothing else in the protocol is optimistic today.

### Q2: Can the last remaining box be removed?

- [x] No; there is always at least one box, and the remove button is disabled on the last one (recommended) — the page is never empty, no empty-state screen is needed, and a fresh server starts with one box as today.
- [ ] Yes; zero boxes is allowed and the page shows only the Add button — slightly more uniform rules, but adds an empty state to design and test.

### Q3: What does the SSE stream send when something changes?

- [x] Per-box events: a text event carries only the changed box, add/remove events carry the box list, and one global revision orders them all (recommended) — typing in a small box never resends a large neighbouring box; the cost is that a slow browser's queue must coalesce per box (bounded by the box limit) or fall back to a full resync.
- [ ] Whole-state snapshots of every box on every change, as today — smallest change and the "newest snapshot wins" queue stays correct, but every keystroke sends every box's text to every browser, so one 10 MiB box makes typing anywhere expensive, working against the 250 ms target (`docs/goal.md:14`).

### Q4: What happens when someone writes to a box another browser just removed?

- [x] The server rejects the write (404), and the writing browser drops that box with a brief "box was removed" notice (recommended) — removal is an ordered update like any other, so the last one received wins, and every browser adopts server state (`docs/goal.md:20-22`); the unsent draft in that box is lost.
- [ ] The write recreates the box — keeps the typist's text, but someone who is typing can silently undo another person's removal, so removal is not reliably synced.
- [ ] The server rejects the write, and the browser keeps the draft in a detached box the user can copy before it disappears — no text is lost, but this is extra UI beyond the copy/add/remove controls (`docs/goal.md:31`).

### Q5: What limits protect server memory?

- [x] At most 10 boxes, each up to 10 MiB; the Add button is disabled at the limit with a short message (recommended) — worst case is about 100 MiB of text, which is predictable on the shared VPS, and it keeps the per-box limit the goal names (`docs/goal.md:12`).
- [ ] At most 10 boxes plus a total cap across all boxes (for example 32 MiB) — lower worst case, but a large paste can be rejected even though its own box is under 10 MiB, which is harder to explain.
- [ ] One 10 MiB total across all boxes, no box count limit — smallest memory use, but one big box starves the others, and it arguably contradicts "each text box handles ... 10 MiB".

### Q6: Should this feature change how text is sent, or prove the performance targets?

- [x] No; keep sending one box's whole text per keystroke and keep performance acceptance deferred (recommended) — with per-box events (Q3) a keystroke only costs its own box; proving 100,000 lines and 250 ms is better done once the protocol is settled, as its own feature.
- [ ] Add a short client-side debounce (for example 50 ms) before each write — fewer requests while typing into a large box, but it spends part of the 250 ms budget and adds timer state per box.
- [ ] Include full size and latency acceptance now — closes criteria 3 and 4 (`docs/goal.md:12-15`), but adds measurement work and may force diff-based transfers into this feature.

### Q7: How should the browser check run?

- [x] Add a `just browser-check` recipe, listed in `AGENTS.md`, but keep it out of `just verify` (recommended) — easy to find and run, while `just verify` keeps no Node or Playwright requirement until CI and Docker are planned.
- [ ] Keep running `scripts/browser-check.mjs` by hand, as today — no change, but the far more complex client has no visible check to run.
- [ ] Make `just verify` run the browser check — browser regressions would fail the gate, but every machine (and future CI) then needs Node and Playwright installed, which is a new dependency for the main gate.

## Out of Scope

Checked against `docs/goal.md`. These product features are deferred to later
features, not removed from the goal:

- Docker packaging, Dokploy/Traefik configuration, HTTPS setup, and VPS deployment.
- CI configuration.
- Full size and latency acceptance (100,000 lines, 250 ms with five browsers),
  as resolved in Q6.

These are excluded by the goal's non-goals:

- Reordering, renaming, dragging, or styling boxes beyond the copy, add and remove
  controls.
- CRDT, operational transformation, or merging concurrent edits within a box.
- Authentication, accounts, private boxes, persistence, or a database.
- Analytics or observability features.
