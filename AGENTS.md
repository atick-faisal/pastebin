# Working in Pastebin

## Verification

**After implementing anything, run `just verify`.** It is the single entrypoint
that runs every check CI runs.

Do not report work as done until `just verify` exits zero. If a check fails for
a reason unrelated to your change, say so explicitly rather than removing the
check.

Run `just` with no arguments to see every recipe. The ones you will need most:

| Command | Purpose |
| --- | --- |
| `just verify` | Full verification. The entrypoint. |
| `just vet` | Check Go packages for suspicious code. |
| `just test` | Run tests with data race detection. |
| `just browser-check` | Drive two browsers against a local server. Run it after browser changes. |
| `just docker-build` | Build the Docker image. Run it after changing the `Dockerfile` or the build. |
| `just docker-run` | Build and run the image on port 8080 under the production memory limit. |

`just browser-check` needs Node and Playwright, so it is not part of
`just verify`. Set `PASTEBIN_PLAYWRIGHT_MODULE` to use an existing Playwright
install.

`just docker-build` and `just docker-run` need Docker, so they are not part of
`just verify` either.

## Planning

Work here is planned before it is built.

- `docs/goal.md` is the north star: Problem, Success Criteria, Non-Goals,
  Constraints. Read it first. It is immutable — changing it is a conversation,
  not a routine edit.
- `docs/features/` holds one directory per feature, named for its slug:
  `plan.md` (whose `## Resolved` section is settled law), `tasks.md` (the
  checklist and its task IDs), `decisions.md` (ADRs), and `pitfalls.md`
  (footguns hit along the way). These are agent state, not published docs.
- `$feature-plan` starts a new feature. `$implement` builds one task from an
  existing one. `$task-plan`, `$write-adr`, `$log-pitfall` and
  `$resolve-pitfall` are on-demand. None of them run on their own — a human
  types them.
- Every commit names its task in the subject, e.g.
  `✨ feat(api): add refresh endpoint (AUTH-ROTATION-04)`. A commit that
  genuinely belongs to no task says `[no-task]` instead.
