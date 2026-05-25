# isnipes

A modern, browser-playable, multiplayer reimagining of the early-1980s
SuperSet/Novell game **Snipes**: a top-down maze where generators spawn hostile
snipes, players spread across the maze, and everyone can shoot everything.
Free-for-all PvP plus PvE, server-authoritative simulation with client-side
prediction.

The whole thing ships as a **single static Go binary** with the TypeScript +
Canvas2D client embedded — no runtime dependencies.

## Requirements

- **Go 1.22+** (the repo's toolchain is pinned to 1.26).
- **Node.js 20+ / npm** — only needed to build the web client (i.e. for the
  embedded production build or the e2e tests). Not needed to run a dev server
  against a prebuilt `web/dist`.

## Quick start

Build the client, embed it, and build the binary, then run it:

```sh
make build        # npm -C web ci → web bundle → go build -tags embed
./isnipes         # serves client + WS on :8080, admin on 127.0.0.1:6060
```

Then open <http://localhost:8080> in a browser. Open it in a second
tab/window (or another machine on your LAN) to play multiplayer — create or
join a match from the lobby.

`make run` does the build and launch in one step.

## Running without the embedded client (dev)

A plain `go build` (no `-tags embed`) produces a binary with **no** embedded
assets. Point it at a built client directory instead:

```sh
npm -C web run build            # produces web/dist/{app.js,index.html}
go build -o isnipes ./cmd/isnipes
./isnipes --web-dist web/dist --insecure-origin
```

`--insecure-origin` is **dev only** — it disables the same-host WebSocket
Origin check. Never use it in production.

## Controls

Default **Classic** preset:

- **Move:** arrow keys (hold two adjacent arrows for diagonals).
- **Shoot:** `W` `A` `S` `D` (N / W / S / E; combos fire diagonally).
- **Turbo:** `Space` (hold — runs at projectile speed; you can't fire while
  turboing).

An alternate **WASD-move** preset (move with `WASD`, shoot with arrows, turbo
on `Shift`) is selectable, and all bindings are individually rebindable; they
persist in `localStorage`.

## Common flags

| Flag | Default | Purpose |
|---|---|---|
| `--addr` | `:8080` | public HTTP/WS listener |
| `--admin-addr` | `127.0.0.1:6060` | `/metrics` + pprof listener (empty disables) |
| `--max-matches` | `64` | concurrent match cap |
| `--motd` | | lobby message of the day |
| `--log-level` | `info` | `debug` / `info` / `warn` / `error` |
| `--web-dist` | | serve client from a dir instead of the embed |
| `--allowed-origins` | | comma-separated extra WS origins (host[:port]) |
| `--insecure-origin` | `false` | accept any WS Origin (**dev only**) |
| `--version` | | print version and exit |

Run `./isnipes --help` for the full list (TLS, pprof, etc.).

## Docker

```sh
docker build -t isnipes .
docker run -p 8080:8080 isnipes
```

The multi-stage build yields a ~10 MB `FROM scratch` image with only the
static binary. Only `:8080` is published; the admin listener stays on
loopback inside the container.

## Testing

```sh
make test            # unit/integration (go test ./...)
make test-race       # race detector + frozen-path guard
make test-load       # smoke load (16 players × 60s; PR-gating perf)
```

Client tests: `npm -C web test` (unit), `npm -C web run test:e2e`
(Playwright). Nightly/soak perf is operator-run — see the perf notes in
[`deploy/README.md`](deploy/README.md).

## Deployment

For production (systemd, nginx/TLS termination, metrics, origin policy,
performance baselines), see **[`deploy/README.md`](deploy/README.md)**.

## Project layout

- `cmd/isnipes/` — server binary (flags, embed, admin mux).
- `internal/` — `sim` (game simulation), `net` (WebSocket server), `lobby`,
  `match`, `observ` (metrics), `loadtest` (load harness).
- `web/` — TypeScript + Canvas2D client (esbuild bundle).
- `scripts/`, `testdata/`, `deploy/` — tooling, fixtures, deploy artifacts.

The full design and phased implementation plan live in `SPEC.md` and the
`PHASE*.md` files.
