---
slug: docker-deploy
title: Docker Deployment
status: in-progress
created: 2026-10-16
---

# Docker Deployment

## Summary

Package the pastebin server as a Docker image and run it on the existing Hetzner
VPS through Dokploy, with Traefik serving it over HTTPS at `pastebin.atick.dev`.
This closes the last unbuilt success criterion in `docs/goal.md:23-24`. Both
shipped features deferred it on purpose (`docs/features/live-shared-text/plan.md:89-96`,
`docs/features/multi-box-sync/plan.md:107-115`).

It also unblocks two things that only work once the app is public. The copy
button needs HTTPS, because `navigator.clipboard.writeText` only runs on HTTPS or
localhost (`docs/features/multi-box-sync/plan.md:52-55`). And the 250 ms latency
target can only be measured honestly through the real VPS and proxy
(`docs/features/live-shared-text/plan.md:86-87`).

The app code should barely change. Most of the work is the image, the Dokploy
setup, and a check that keeps the image building.

## Tech Stack / Approach

**What the server already does right (no change needed):**

- Listens on `-addr`, default `:8080` (`cmd/pastebin/main.go:13`). That matches
  how the host's existing Dokploy apps are routed (Traefik sends traffic to
  the app's container on port 8080).
- Sets no server-wide write timeout, so SSE streams stay open
  (`cmd/pastebin/main.go:21-22`). It sends a 15 s keepalive
  (`internal/httpserver/server.go:294-296,332`).
- Embeds its web assets with `go:embed` (`internal/httpserver/server.go:21-22`),
  so the image needs only one static binary.
- Uses relative URLs in the browser (`internal/httpserver/web/app.js:14,87-89`),
  so it works behind a proxy unchanged.
- Has no SIGTERM handler, which is fine. The Go runtime exits on SIGTERM even as
  PID 1, and in-memory state is lost on redeploy whatever we do (`docs/goal.md:32`).
  Browsers reconnect and get a fresh snapshot (`internal/httpserver/server.go:286-287`).

**What the deploy host looks like (checked on 2026-10-16):**

- **Machine:** x86_64, 2 vCPU. **Memory is tight (a few hundred MB free) and
  there is no swap.**
- **Software:** Docker in Swarm mode, Dokploy, Traefik v3.
- **Traefik:** the `websecure` entry point uses the `letsencrypt` resolver by
  default, through a name.com DNS challenge. The only shared middleware is
  `redirect-to-https`. No compression or buffering middleware exists, so SSE
  passes through unbuffered. `X-Accel-Buffering: no` (`server.go:43`) is an
  nginx header that Traefik ignores; it does no harm.
- **Existing apps:**
  - Every app on the host is deployed from a GitHub repo under `atick-faisal`
    through Dokploy's GitHub provider.
  - The existing single-container app is a Dokploy *Application* built from
    its Dockerfile, with no memory limit.
  - The multi-container stacks are Dokploy *Compose* apps, and each service sets
    its own `mem_limit` (96m to 1g).
- **This app:** Dokploy has no pastebin app yet. **`pastebin.atick.dev` has no
  DNS record.** The host's other apps point at its public IP, so the pastebin
  record is a manual A record at name.com. The IP is kept out of this repo.
- **This repo:** no git remote yet (`git remote -v` is empty).
- **Base image:** the `golang:1.27.2` and `golang:1.27.2-alpine` images exist on
  Docker Hub, which matches `go 1.27.0` in `go.mod`.

**What gets added:**

- A multi-stage `Dockerfile`. A Go 1.27 stage runs
  `CGO_ENABLED=0 go build ./cmd/pastebin`, and the binary is copied into a
  minimal runtime image (see Q4).
- A `.dockerignore`, so `.git`, `docs/` and `scripts/` stay out of the build
  context.
- `just` recipes in the existing style (`justfile`), such as `docker-build` and
  `docker-run`, listed in `AGENTS.md` next to `browser-check`. Whether any of
  them joins `just verify` is Q6.
- Dokploy and DNS setup. These are manual steps on the dashboard and at name.com.
  They are written down in the repo so they can be repeated, but they are not
  automated.
- **Exactly one replica.** All state lives in one process's memory
  (`docs/goal.md:41`). A second replica would split the boxes between containers,
  so the plan must pin replicas to 1 wherever Dokploy exposes it.

## Open Questions

## Resolved

### Q1: How does Dokploy get the code?

- [x] Push this repo to GitHub and connect it through Dokploy's GitHub provider (recommended) — every app on the host already deploys this way, so you get push-to-deploy with no new tooling; the cost is creating the remote.
- [ ] Build the image elsewhere (locally or in CI), push it to a registry such as GHCR, and have Dokploy deploy the image — keeps the source off the server and makes the deployed image exactly the tested one, but adds registry credentials and a push step.
- [ ] Use Dokploy's manual upload or a self-hosted git remote — no GitHub needed, but nothing on the host uses it, deploys become a manual chore, and CI (Q6) has nowhere to run.

The repo is public, as `atick-faisal/pastebin` (confirmed 2026-10-16).

### Q2: How is the image built?

- [x] A Dockerfile in the repo (recommended) — `docker build` gives the same image locally and on the server, which is what "built and run in Docker" (`docs/goal.md:23`) asks for, and it matches the host's existing Application.
- [ ] Dokploy's Nixpacks/Railpack auto-build — no file in the repo, but the image is decided by the builder's Go detection, can't be reproduced locally with plain Docker, and can change when Dokploy upgrades.

### Q3: Which Dokploy app type?

- [x] Application (recommended) — matches the host's other single-container app; Dokploy creates the Traefik router and HTTPS certificate from the domain you set, and the memory limit is set in its resource settings. The cost is that these settings live in the dashboard, so the repo needs a short written record of them.
- [ ] Compose, with a `docker-compose.yml` in the repo — the memory limit and replica count live in version control, like the host's observability stacks; but it adds a compose file and Dokploy's compose networking for one container, and the domain is still set in the dashboard.

### Q4: Which runtime base image, and is there a container health check?

- [x] `gcr.io/distroless/static` (nonroot) with no Docker `HEALTHCHECK` (recommended) — small, no shell, runs as non-root; a Go binary is listening within milliseconds of starting, and health monitoring beyond that is a Non-Goal (`docs/goal.md:30`).
- [ ] distroless plus a `-healthcheck` flag in the binary that calls `GET /` on itself — Swarm then knows when the container is really serving, but it adds code and a test to `main.go` for a check nothing on the host acts on.
- [ ] `alpine` with a `wget`-based `HEALTHCHECK` — a shell for debugging and an easy health check, but a larger image and an OS package set to keep patched.
- [ ] `scratch` — the smallest image, but it has no CA certificates or `/etc/passwd`, so running as non-root has to be set up by hand; it saves little over distroless.

### Q5: How much memory may the container use?

- [x] A 384 MiB container limit with `GOMEMLIMIT=320MiB` (recommended) — the worst case is about 100 MiB of text (`docs/features/multi-box-sync/plan.md:91`), plus a 10 MiB request body and snapshot copies for each browser; `GOMEMLIMIT` makes Go's garbage collector work harder before the hard limit kills the process, and 384 MiB fits within the host's free memory. Checked locally with `docker run --memory` while filling 10 full boxes.
- [ ] A 256 MiB limit, like the host's smaller compose services — leaves more room for other apps, but snapshot copies at the worst case could push it over, and an out-of-memory kill wipes everyone's text.
- [ ] No limit, like the existing single-container app — nothing to tune, but the host has no swap, so a worst-case spike could make the kernel kill other services on the VPS (`docs/goal.md:36-37`).

Checked on 2026-10-16 (DOCKER-DEPLOY-02): with `--memory=384m`, five SSE
subscribers and 10 boxes of 10 MiB each, overwritten for 8 rounds, the peak was
about 342 MiB (sampled with `docker stats`, so a very short spike could be a
little higher). The container was never OOM-killed and every subscriber
converged. That leaves about 42 MiB of headroom, so the limit holds.

### Q6: What checks keep the image building?

- [x] GitHub Actions that runs `just verify` and `docker build` on every push; `browser-check` stays out (recommended) — a broken Dockerfile is caught before Dokploy tries to deploy it; this needs Q1 to be GitHub.
- [ ] The same, plus `just browser-check` in CI with Playwright installed — browser regressions are caught too, which is the question `multi-box-sync` left open "until CI and Docker are planned" (`docs/features/multi-box-sync/plan.md:103`); costs a few minutes per run and a Node/Playwright setup to keep working.
- [ ] No CI; add a `just docker-build` recipe only — the smallest change, but nothing runs it automatically, so a broken image is first seen at deploy time.

### Q7: Does this feature also prove the size and latency targets?

- [x] No. This feature ends when the app is live and passes a written manual check: two browsers at `https://pastebin.atick.dev` sync text, add and remove boxes, and copy works. Size and latency acceptance becomes the next feature (recommended) — it keeps this feature about packaging, and that feature can then measure against the real deployment.
- [ ] No, but teach `scripts/browser-check.mjs` to target a URL, so the same two-browser check runs against the deployed site — the post-deploy check is repeatable, but it changes the browser script in a packaging feature.
- [ ] Yes, include full size and latency acceptance (`docs/goal.md:12-15`) — closes every success criterion at once, but adds a measurement harness, makes this feature complex, and may force diff-based transfers (`docs/features/multi-box-sync/plan.md:99`) into it.

## Out of Scope

Checked against `docs/goal.md`. Excluded by the goal's Non-Goals:

- Volumes, backups or any persistence across redeploys (`docs/goal.md:32`).
- Metrics, log shipping, tracing, or joining the host's existing observability
  stack (`docs/goal.md:30`).
- Authentication or access restrictions at Traefik (`docs/goal.md:29`, `:42-43`).

Excluded by the goal's Constraints:

- TLS handled inside the app; Traefik and Dokploy provide HTTPS (`docs/goal.md:38`).
- Any database or shared service (`docs/goal.md:41`).

Deliberately not done here:

- More than one replica, or any load balancing across containers.
- Changes to Traefik's global config, its middlewares, or any other service on
  the host.
- Automated DNS management. The `pastebin.atick.dev` A record is added by hand.
- Size and latency acceptance, as resolved in Q7. It becomes the next feature.
