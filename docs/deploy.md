# Deploying Pastebin

Pastebin runs as one Docker container on the Hetzner VPS. Dokploy builds it from
this repo's `Dockerfile`, and Traefik serves it over HTTPS at
`https://pastebin.atick.dev`.

These steps are applied by hand in the Dokploy dashboard and at name.com. Dokploy
field names can differ slightly between versions; the values below are what matter.

## 1. DNS (name.com)

Add one **A record**:

| Host | Type | Answer |
| --- | --- | --- |
| `pastebin` (under `atick.dev`) | A | the VPS's public IP |

Use the same IP the host's other apps (for example `fastapi.atick.dev`) already
point at. The IP is deliberately not written in this repo.

Check it: `dig +short pastebin.atick.dev` prints that IP.

## 2. Dokploy Application

Create an **Application** (not Compose). It matches the host's other
single-container app.

| Setting | Value |
| --- | --- |
| Source | GitHub provider, repo `atick-faisal/pastebin`, branch `main` |
| Build type | `Dockerfile`, file `Dockerfile`, context `.` |
| Domain | `pastebin.atick.dev`, container port `8080`, HTTPS on, certificate `letsencrypt` |
| Replicas | **1** (see "Why one replica" below) |
| Memory limit | **384 MiB**, entered as bytes: `402653184` |

Where to find them (docs: <https://docs.dokploy.com/docs/core/applications/advanced>):

- **Replicas:** Advanced tab, Cluster Settings, Replicas field. Enter `1`.
- **Memory limit:** Advanced tab, Resources, Memory Limit field. Enter
  `402653184` with no unit. Dokploy passes the number straight to Docker, so a
  value like `384m` is read as 384 bytes and fails with a vague Swarm error.
- Press **Redeploy** after changing either one, or it does not take effect.

Notes:

- The memory limit is set here, not in the image. The image already sets
  `GOMEMLIMIT=320MiB` so the garbage collector works harder before the hard
  limit is reached. The 384 MiB figure was checked in
  `docs/features/docker-deploy/plan.md` (Q5).
- No volumes, environment variables, health check or auth are needed.
- Turn on auto-deploy if you want push-to-deploy; otherwise press Deploy.

### Why one replica

All text lives in one process's memory. A second replica would split the
connected browsers between two containers, each with different text, and they
would never converge. Keep replicas at 1.

A redeploy or restart wipes all text. That is expected; there is no persistence.

## 3. Manual check

Open `https://pastebin.atick.dev` in two different browsers (or one normal window
and one private window). Every item must pass.

- [ ] The page loads over HTTPS with a valid certificate and no warning.
- [ ] Typing in a box in browser A appears in browser B within about a second.
- [ ] Pasting a large block of text in A shows up in B.
- [ ] Adding a box in A adds it in B; removing a box in A removes it in B.
- [ ] The copy button puts the box's full text on the clipboard (paste it
      somewhere to confirm). This only works over HTTPS, so it is a real test
      of the deployment.
- [ ] Reloading browser B shows the current text, not an empty page.

Size and latency targets are not part of this check; they are the next feature.

## Troubleshooting

- **Certificate not issued, or a Traefik default certificate shown:** the A record
  has not propagated yet, or the domain's certificate is not set to
  `letsencrypt`. Check `dig` first.
- **404 from Traefik:** the domain's container port is not `8080`.
- **Text stops syncing after a while:** something is buffering or timing out the
  event stream. The server sends a keepalive every 15 seconds; look for a proxy
  timeout shorter than that.
- **Text disappeared:** the container restarted (redeploy, crash or out-of-memory
  kill). Check the Dokploy logs for an OOM kill.
