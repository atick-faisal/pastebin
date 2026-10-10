# Pastebin

## Problem

Need a shared place to dump and copy text across browser instances,
with immediate synchronization and minimal interface overhead.

## Success Criteria

- Pasted and edited text automatically appears across all connected instances.
- Up to five concurrent connections converge on the server's authoritative state.
- Each text box handles up to 100,000 lines, subject to a 10 MiB text limit,
  while pasting, editing, copying, and synchronizing remain responsive.
- Small edits reach the other connected instances within a target of 250 ms
  with five concurrent connections. Large pastes are assessed separately.
- Each text box has a button that copies its complete content.
- Users can add and remove text boxes, with those changes synchronized
  across connected instances.
- New and reconnected instances receive the current server state.
- The server processes updates in its received order; the last update received
  for a text box replaces its content. All clients adopt the resulting server
  state, without merging concurrent edits.
- The application can be built and run in Docker and deployed on the
  existing Hetzner VPS through Dokploy and Traefik with HTTPS.

## Non-Goals

- CRDT, operational transformation, or Google Docs-style collaborative merging.
- Authentication, accounts, or private access.
- Analytics or observability features.
- UI/UX work beyond functional text boxes and copy/add/remove controls.
- Persistence across process restarts or redeploys.

## Constraints

- Target deployment: the existing $6 Hetzner VPS, sharing its resources
  with existing services.
- HTTPS is provided by the existing Traefik/Dokploy setup.
- Intended domain: pastebin.atick.dev.
- Stack: Go backend and plain HTML with minimal JavaScript.
- Shared text and box state live in Go memory; no database is required.
- Access is public and unauthenticated; connected visitors can read
  and change the shared text boxes.
