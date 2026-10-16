# Multi-Box Sync — Tasks

Each task includes its tests and must pass `just verify` before it is done.
Browser behavior also requires `just browser-check` once MULTI-BOX-SYNC-02 lands.

| ID | Description | Complexity | Suggested Model | Status | Notes |
| --- | --- | --- | --- | --- | --- |
| MULTI-BOX-SYNC-01 | Add a copy button to the existing text box using `navigator.clipboard.writeText`, with a short failure message when the clipboard is unavailable; extend the browser check to cover copying. | small | sonnet | done | Client only; no protocol change. |
| MULTI-BOX-SYNC-02 | Add a `just browser-check` recipe that runs `scripts/browser-check.mjs`, kept out of `just verify`, and list it in `AGENTS.md`. | trivial | haiku | done | Q7. While in `AGENTS.md`, drop its stale "no Go module" note. |
| MULTI-BOX-SYNC-03 | Turn the in-memory state into ordered boxes: counter-assigned IDs, add/remove/write under one global revision, at least one box, at most 10 boxes, 10 MiB per box, and distinct errors for unknown boxes and limits; with concurrency and limit tests. | moderate | sonnet | done | Q1, Q2, Q5. |
| MULTI-BOX-SYNC-04 | Replace `POST /text` with add, remove, and per-box write routes; send per-box and add/remove SSE events ordered by the global revision, a full snapshot on connect, and bounded per-box coalescing (or full resync) for slow subscribers; return 404 for writes to removed boxes. Rewrite the server tests for the new protocol, five subscribers, and slow clients. | complex | opus | done | Depends on -03. Q3, Q4. The page is broken until -05 lands; land them back to back. |
| MULTI-BOX-SYNC-05 | Render boxes from server state with a per-box write queue and draft tracking; add an Add button (disabled at the limit, with a message), a Remove button (disabled on the last box), and a "box was removed" notice; keep the copy button on every box. Rewrite the browser check for two browsers adding, removing, editing, and copying boxes. | complex | opus | done | Depends on -01, -02, -04. Q2, Q4, Q5, Q6. |
