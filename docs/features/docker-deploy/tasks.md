# Docker Deployment — Tasks

Each task must pass `just verify` before it is done. Tasks that touch the image
also need a clean `just docker-build` once DOCKER-DEPLOY-01 lands.

| ID | Description | Complexity | Suggested Model | Status | Notes |
| --- | --- | --- | --- | --- | --- |
| DOCKER-DEPLOY-01 | Add a multi-stage `Dockerfile` (`golang:1.27.2` builder, `CGO_ENABLED=0 go build ./cmd/pastebin`, `gcr.io/distroless/static` nonroot runtime, `EXPOSE 8080`, `ENV GOMEMLIMIT=320MiB`, no `HEALTHCHECK`) and a `.dockerignore` that drops `.git`, `docs/` and `scripts/`. Add `just docker-build` and `just docker-run` recipes, kept out of `just verify`, and list them in `AGENTS.md`. | small | sonnet | done | Q2, Q4, Q5. Setting `GOMEMLIMIT` in the image keeps local runs and Dokploy the same. |
| DOCKER-DEPLOY-02 | Check the worst case under the memory limit: run the image with `--memory=384m`, connect five SSE subscribers, fill 10 boxes with 10 MiB each, and confirm the container stays up and every subscriber converges. Record the peak memory in `plan.md`. If it does not fit, stop and raise it rather than changing the limit. | small | sonnet | pending | Depends on -01. Q5. A manual check; no new script unless the run needs one. |
| DOCKER-DEPLOY-03 | Create the public GitHub repo `atick-faisal/pastebin` with `gh`, add it as `origin`, and push `main`. | trivial | haiku | pending | Q1. Outward-facing: confirm with the human before pushing. Name checked free on 2026-10-16. |
| DOCKER-DEPLOY-04 | Add a GitHub Actions workflow that runs `just verify` and `docker build` on every push and pull request, using `go.mod` for the Go version. `browser-check` stays out. | small | sonnet | pending | Depends on -01, -03. Q6. Green on the first push to GitHub. |
| DOCKER-DEPLOY-05 | Write `docs/deploy.md` with the Dokploy Application settings (GitHub source, Dockerfile build, port 8080, domain `pastebin.atick.dev` with HTTPS via the `letsencrypt` resolver, 1 replica, 384 MiB memory limit) and the name.com A record to the host's public IP (the human supplies it; it stays out of the repo). The human applies them in the dashboard and at name.com. Then run the manual check: two browsers at `https://pastebin.atick.dev` sync text, add and remove boxes, and copy works. | moderate | sonnet | pending | Depends on -02, -03, -04. Q3, Q7. Closes `docs/goal.md:23-24`. |
