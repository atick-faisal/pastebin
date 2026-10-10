# Live Shared Text — Tasks

Each task includes its tests and must pass `just verify` before it is done.
Browser behavior also requires the acceptance checks recorded in `plan.md`.

| ID | Description | Complexity | Suggested Model | Status | Notes |
| --- | --- | --- | --- | --- | --- |
| LIVE-SHARED-TEXT-01 | Bootstrap the Go module and in-memory text state with serialized replacements, revisions, the 10 MiB UTF-8 limit, and concurrency/limit tests. | moderate | balanced | done | No external dependencies. |
| LIVE-SHARED-TEXT-02 | Add the HTTP server, POST updates, and SSE snapshots with atomic initial subscription, sender delivery, bounded slow-client handling, disconnect cleanup, and integration tests for five subscribers and reconnects. | complex | deep | done | Depends on LIVE-SHARED-TEXT-01. |
| LIVE-SHARED-TEXT-03 | Serve the plain HTML text box and implement serialized writes, preservation of pending typing, revision ordering, reconnect reset, and failure feedback; verify two-browser convergence, overlapping edits, sender echoes, and feedback-loop prevention. | complex | deep | done | Depends on LIVE-SHARED-TEXT-02; full performance acceptance is deferred. |
