# Deploying isnipes

`isnipes` is a single self-contained binary: the Go server with the web
client embedded. It needs no runtime dependencies.

## Build

```sh
make build        # npm -C web ci + web build → embed → go build -tags embed
./isnipes         # serves the client + WS on :8080, admin on 127.0.0.1:6060
```

A plain `go build ./cmd/isnipes` (no `-tags embed`) produces a binary
**without** embedded assets; run it with `--web-dist DIR` or use `make
build` for production.

## Docker

```sh
docker build -t isnipes .
docker run -p 8080:8080 isnipes
```

Multi-stage build (node → go `-tags embed` → `FROM scratch`); the final
image is the static binary (~10 MB). Only `:8080` is published; the admin
listener stays on loopback inside the container. `scratch` ships no CA
bundle — for outbound HTTPS from the container, add a CA layer or switch
the final stage to `gcr.io/distroless/static`. Pin the builder images by
digest for reproducible builds.

## systemd

```sh
sudo cp isnipes /usr/local/bin/
sudo cp deploy/isnipes.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now isnipes
```

The unit (`deploy/isnipes.service`) runs unprivileged (`DynamicUser`, no
capabilities) with filesystem/kernel hardening, binding the public
listener and the admin listener to loopback so nginx fronts the former
and the latter stays host-local.

## TLS

Two supported options:

1. **nginx termination** (`deploy/nginx.conf`): nginx holds the cert and
   reverse-proxies to `127.0.0.1:8080` with the WebSocket Upgrade
   headers. It also `404`s `/metrics` and `/debug/pprof` defensively.
2. **Direct TLS**: `isnipes --require-tls --tls-cert cert.pem --tls-key
   key.pem` terminates TLS in the binary itself.

## WebSocket origin policy

By default the server enforces a **same-host** Origin check (blocks
cross-site WebSocket hijacking). Behind a TLS-terminating proxy this just
works (the check is host-based, matching the proxied Host). For a
separate dev front-end (e.g. Vite on `localhost:5173`), pass
`--allowed-origins localhost:5173`. `--insecure-origin` disables the
check — **dev only, never in production** (note `--allowed-origins '*'`
is equivalent to insecure).

## Flags

| Flag | Default | Purpose |
|---|---|---|
| `--addr` | `:8080` | public HTTP/WS listener |
| `--admin-addr` | `127.0.0.1:6060` | `/metrics` + pprof listener (empty disables) |
| `--enable-pprof` | `false` | mount `/debug/pprof/*` on the admin listener |
| `--require-tls` | `false` | terminate TLS directly (needs cert/key) |
| `--tls-cert` / `--tls-key` | | PEM cert/key for `--require-tls` |
| `--allowed-origins` | | comma-separated extra WS origins (host[:port]) |
| `--insecure-origin` | `false` | accept any WS Origin (DEV ONLY) |
| `--max-matches` | `64` | concurrent match cap |
| `--motd` | | lobby message of the day |
| `--log-level` | `info` | `debug`/`info`/`warn`/`error` |
| `--web-dist` | | serve client from a dir instead of the embed |
| `--version` | | print version and exit |

## Endpoints

**Public listener** (`--addr`): `/` (client), `/healthz`, `/version`,
`/ws/lobby`, `/ws/match/{id}`.

**Admin listener** (`--admin-addr`, loopback): `/metrics` (Prometheus
text exposition), and `/debug/pprof/*` when `--enable-pprof`.

## Metrics

Scrape the admin listener (`127.0.0.1:6060/metrics`). Key series:

- `isnipes_tick_seconds{bucket,sum,count}` — sim tick budget; healthy
  P99 is well under the **10 ms** PR target / **5 ms** nightly target.
- `isnipes_ticks_over_budget_total`, `isnipes_snapshot_drops_total` —
  over-budget ticks and the snapshots they suppressed (SPEC §11).
- `isnipes_bytes_in_total`, `isnipes_bytes_out_total` — match traffic.
- `isnipes_active_matches`, `isnipes_joined_players` — live gauges.

## Performance

- PR-gating smoke load: `make test-load` (16 players × 60 s; P99 tick
  < 10 ms; bandwidth ≤ 12 KB/s/client; no goroutine leak).
- Nightly / soak (operator-run): `make perf-nightly` (512-player
  regression vs `testdata/perf_baseline.json`; 24 h soak).
