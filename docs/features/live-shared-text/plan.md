---
slug: live-shared-text
title: Live Shared Text
status: in-progress
created: 2026-10-10
---

# Live Shared Text

## Summary

Build the first usable Pastebin slice: one shared text box served by a Go
process. Open it in two browsers, edit or paste in either, and see both converge
on the server's current text. The server holds text in memory and accepts whole
text replacements in received order, without merging concurrent edits. New and
reconnected browsers receive the current state.

This slice establishes the application and synchronization protocol. Multiple
boxes, dedicated copy controls, and deployment are later increments of the
product described in `docs/goal.md`.

## Tech Stack / Approach

- Follow `docs/goal.md`: Go backend, plain HTML, minimal JavaScript, public
  access, and in-memory state. There is no existing application code to reuse.
- Introduce a Go module and a small HTTP server that serves the text box and
  handles synchronization. Keep the browser assets with the application;
  avoid a frontend build system, database, or separate service.
- Use SSE for server snapshots and HTTP POST for text replacements, with Go's
  standard library and browser-native APIs.
- Maintain one authoritative text value and a monotonically increasing
  revision for each accepted replacement within a server process. Serialize
  acceptance so revision order and server-received update order agree.
- Broadcast accepted snapshots to every connected browser, including the
  sender. Apply only newer revisions within a connection; a new connection
  establishes its baseline from a fresh snapshot, including after a restart.
  Handle the subscription handshake without a gap between the initial snapshot
  and subsequent updates.
- Send complete text replacements. Server revisions order snapshots; they do
  not reject a write just because another browser edited first. The last update
  received replaces the text, as required by `docs/goal.md`.
- Distinguish user input from programmatic text replacement to avoid broadcast
  loops. Preserve the latest local draft while writes are pending, then adopt
  server state when the local write queue drains. On reconnect,
  adopt the fresh server snapshot and discard unsent edits from the previous
  connection rather than automatically replaying them.
- Bound accepted text to 10 MiB of UTF-8 bytes, with bounded request overhead.
  Reject oversized writes without changing shared state and show a concise
  failure message. Clean up disconnected subscriptions and bound outgoing
  work so a slow browser cannot block all other browsers or grow memory without
  limit. Text resets when the process restarts.
- Reuse `justfile` as the verification entrypoint. Meaningful Go tests cover
  received-order replacement, concurrent access, sender delivery, initial and
  reconnect snapshots, size rejection, and slow/disconnected clients. Every
  implementation task must pass `just verify`.
- Browser acceptance covers editing and pasting in both directions, overlapping
  edits followed by convergence when typing stops, reconnecting to current
  state, and avoiding feedback loops. Go tests alone do not establish browser
  behavior. Test five subscribers for correctness and enforce the size bound;
  full size and latency acceptance is deferred, as recorded below.

## Open Questions

## Resolved

### Q1: Which transport should carry live updates?

- [x] SSE for server snapshots plus HTTP POST for text replacements (recommended) — keeps the first slice within Go's standard library and browser-native APIs; writes and snapshots still need revision ordering across separate channels.
- [ ] WebSocket for writes and snapshots — puts both directions on one connection, but requires a Go library dependency and explicit reconnect handling.

### Q2: What should happen to local typing when server snapshots arrive while a write is pending?

- [x] Preserve and submit the latest local draft while writes are pending, then adopt server state once the local write queue drains (recommended) — protects continued typing from earlier echoes, but needs draft/submission tracking and can overwrite a remote edit with a later whole-text submission; it still performs no merging.
- [ ] Apply every newer server snapshot immediately, discarding any unsent local draft — most directly matches adopting server state with minimal client bookkeeping; snapshots can replace text typed since an earlier submission, including the sender's own echo.

Both options must serialize writes from each browser, prevent older snapshots
from rolling back newer accepted state, and converge after writes stop. Applying
a server snapshot must not generate another write on its own.

### Q3: How much size and performance validation belongs in this first slice?

- [x] Establish correctness and bounds now; defer full performance acceptance (recommended) — demonstrate two-browser behavior, test five subscribers for correctness and enforce the 10 MiB limit, while leaving 100,000-line responsiveness and the five-browser 250 ms target explicitly unproven.
- [ ] Include full size and performance acceptance now — exercise up to 100,000 lines within 10 MiB and measure small-edit latency with five active browsers, assessing large pastes separately; this adds browser measurement work and may require revisiting whole-text transfers before the slice is accepted.

The product targets in `docs/goal.md` remain unchanged under either option.
Local checks cannot establish deployed latency through the VPS and proxy;
deployment validation remains a later increment.

## Out of Scope

Checked against `docs/goal.md`. The following product features are deferred for
this slice, rather than removed from the goal:

- Multiple text boxes, synchronized add/remove controls, and dedicated copy buttons.
- Docker packaging, Dokploy/Traefik configuration, HTTPS setup, and VPS deployment.
- CI configuration; the existing `just verify` remains the local gate.

The goal's non-goals also apply:

- CRDT, operational transformation, conflict merging, or collaborative cursor features.
- Authentication, accounts, private access, persistence, or a database.
- Analytics, observability features, and UI work beyond a functional text box
  and basic connection/write feedback.
