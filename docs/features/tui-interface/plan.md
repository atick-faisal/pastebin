---
slug: tui-interface
title: TUI-style Interface
status: shipped
created: 2026-10-16
---

# TUI-style Interface

## Summary

Give the pastebin page a minimal, terminal-like look in the One Dark colour scheme, and add
a few small usability features: keyboard shortcuts, a line count and a byte count for each
text box. The aim is a page that feels like a quiet terminal pane and stays out of the way,
which fits the goal's "minimal interface overhead". Sync behaviour, limits and the server
do not change.

`docs/goal.md` was amended on 2026-10-16, with the human's explicit one-time permission, so its
UI/UX Non-Goal now allows this work. It previously ruled out "UI/UX work beyond functional text
boxes and copy/add/remove controls".

## Tech Stack / Approach

Plain HTML, hand-written CSS and a little JavaScript. No build step, no framework, no CDN
or web fonts, no npm dependency (goal Constraints).

- **Styling:** replace the three inline rules in `internal/httpserver/web/index.html:7-11`
  with a small One Dark stylesheet. Use CSS custom properties for the palette, a system
  monospace font stack, thin borders and compact spacing. Keep `disabled` and `focus`
  states clearly different from each other, because the app disables controls while
  disconnected.
- **Counts:** a line count and a byte count per box, shown near the box's status line.
  Added in `internal/httpserver/web/app.js`, next to the existing `render(box)` and
  `createBox(id)` functions.
- **Keyboard shortcuts:** one `keydown` listener in `app.js` that calls the same code the
  buttons already use (`add` click handler, `removeBox`, the copy handler), so there is
  one code path per action.
- **Must stay stable:** the ids and classes `app.js` and `scripts/browser-check.mjs` query
  (`#status`, `#notice`, `#boxes`, `#add`, `#add-status`, `#box-template`, `.box`,
  `.status`, `.copy-status`, `.copy`, `.remove`, `textarea`, `label`), the
  `<script src="/app.js" defer></script>` tag asserted in
  `internal/httpserver/server_test.go:560`, and the `role="status" aria-live="polite"` and
  `aria-label` attributes.
- **Server:** `internal/httpserver/server.go` embeds exactly `web/index.html` and
  `web/app.js`. Inline CSS needs no server change.
- **Verification:** `just verify` only runs `go vet` and `go test -race`, so it cannot see
  visual or DOM regressions. Run `just browser-check` after every frontend task.
- **Performance guardrail:** boxes can hold 100,000 lines and 10 MiB. No line-number
  overlays, custom scrollbars or per-keystroke full-text scans on the textarea.

"Line count" means the number of lines in a box, confirmed by the human. It is a number in the status area, not a gutter beside the text.

## Open Questions

## Resolved

### Q1: How should the line and byte counts be computed?

- [x] Debounced, about 150 ms after the last input and again after remote text arrives (recommended) — one scan per pause in typing, so a 10 MiB box stays responsive
- [ ] On every `input` event — simplest and always exact, but rescans up to 10 MiB per keystroke and risks the responsiveness success criterion
- [ ] Only when a box loses focus or is idle — cheapest, but the numbers lag behind what the user sees

### Q2: Which keyboard shortcuts should exist?

- [x] Alt-based set: Alt+N adds a box, Alt+1…9 focuses a box, Alt+C copies the focused box, Alt+Backspace removes the focused box (recommended) — Alt combinations rarely clash with browser or OS shortcuts and stay out of the way of normal typing
- [ ] Ctrl/Cmd-based set — familiar, but clashes with browser shortcuts (Ctrl+N, Ctrl+W) and with copy/paste inside the textarea
- [ ] Vim-style prefix key (for example `Esc` then a letter) — fits the TUI theme, but is harder to discover and adds more state to the script
- [ ] Focus and copy only, no add or remove — safest, but gives up fast box management

### Q3: Where should the CSS live?

- [x] Inline `<style>` in `index.html` (recommended) — matches today's pattern, needs no new embed entry, route or Content-Type branch in `server.go`
- [ ] Separate `style.css` — cleaner file split, but adds an embed entry, a route and a test, plus a second request on first load

### Q4: How should shortcuts be shown to the user?

- [x] A one-line hint bar under the title, for example `Alt+N add · Alt+C copy · Alt+1-9 focus`, and key hints in button `title` text (recommended) — discoverable at a glance and still minimal
- [ ] A help overlay opened with `?` — tidy, but adds a modal, focus handling and more script
- [ ] No on-screen hints, documented in the README only — least UI, but users will not find the shortcuts

## Out of Scope

Checked against `docs/goal.md` Non-Goals. Only the UI/UX Non-Goal conflicts, and the human
chose to amend it.

- A light theme or a theme switcher. One Dark only, as requested.
- Line-number gutters, syntax highlighting or other editor-style features in the textarea.
- Any change to sync, ordering, limits (10 boxes, 10 MiB, 100,000 lines) or the server API.
- Persistence, authentication, analytics or CRDT-style merging (still Non-Goals).
- Web fonts, CDN assets, icon libraries or a build step.
