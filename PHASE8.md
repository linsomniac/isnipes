# Phase 8 — Performance, load testing, deploy

This document is the buildable, testable expansion of §8 Phase 8 of
[`SPEC.md`](./SPEC.md). It assumes Phases 1–7 are on `main`:
`internal/sim` is the deterministic 30 Hz simulation; `internal/proto`
defines the binary wire schema (`schemaChecksum = 0x42607394`);
`internal/match` runs match actors (lives, dead-cam, scoring,
`Scoreboard`/`MatchOver`, DC-grace reconnect, in-match chat relay);
`internal/net` carries frames over WebSocket; `internal/lobby` is the
full lobby (chat / kick / level validation / GC / delta-push /
deep-link / `level_presets`); `cmd/isnipes` boots all of it; and `web/`
ships the full client (`render.ts`, `hud.ts`, `audio.ts`, `input.ts`,
`settings.ts`, … bundled through `browser.ts`) with a vitest +
Playwright harness.

Anywhere this document conflicts with `SPEC.md`, `SPEC.md` is canonical
and this document is wrong; please file an issue. Section references
like "§8" point into `SPEC.md`; "P5 §x", "P6 §x", "P7 §x" point into
the earlier phase docs.

---

## 1. Scope and definition of done

**Scope.** Phase 8 turns the working game into an operable, deployable,
load-validated service. It adds (a) **observability** — per-tick budget
instrumentation, a `/metrics` endpoint, and a real (gated) pprof mount;
(b) a **synthetic-client load harness** that drives the full
lobby→match handshake in-process and measures end-to-end latency,
server tick budget, bandwidth, goroutines, and RSS; (c) a **PR-gating
smoke load** plus a non-blocking **nightly/soak** profile; (d) the
**production embed/build pipeline** so the binary serves the real
client (closing the deferred Phase-6 finding that `./isnipes` serves the
Phase 2 placeholder); and (e) **deployment artifacts** — a multi-stage
`FROM scratch` Dockerfile, a hardened systemd unit, an nginx TLS
example, and a deploy guide.

This phase is **server- and ops-heavy** with **no client gameplay
change** and **no wire-schema change**. The hard invariants carried
forward from Phase 7 are:

- **`internal/sim` is not edited.** The Phase 1/3/5 determinism
  fingerprints stay byte-identical. Tick-budget instrumentation lives
  in the `internal/match` actor wrapper (around the call to `m.tick()`),
  never inside the sim.
- **`schemaChecksum` stays `0x42607394`.** `internal/proto/checksum.go`
  and the client mirrors are not edited. No frame is added or changed;
  the load harness speaks the existing wire protocol.
- **The Phase 7 frozen-file guard stays green.** `scripts/check-frozen.sh`
  must still pass over its manifest (`internal/sim/**`,
  `internal/proto/checksum.go`, `web/src/{proto,sim,prediction,interp,
  netClient}.ts`). Phase 8 edits none of those files.

Phase 8 ships:

- **Observability** (`internal/observ`) — a dependency-free metrics
  registry: a lock-free tick-duration histogram (atomic fixed buckets)
  with a `P99()` query, counters (active matches, joined players, bytes
  in/out, snapshot drops, ticks over budget), and a `/metrics` handler
  emitting Prometheus **text exposition** format. No new module
  dependency (open question §19.1).
- **pprof, actually mounted** (`cmd/isnipes`) — `--enable-pprof`
  currently sets a discarded local (`_ = enablePprof`); Phase 8 mounts
  `net/http/pprof` under `/debug/pprof/*` **only** when the flag is set
  (off by default, so the prod surface stays minimal).
- **Tick-budget instrumentation** (`internal/match`) — the actor times
  each `m.tick()` via the injectable `clock()` and records the duration
  into an `observ`-supplied sampler (nil-safe; zero behaviour change
  when unset). It also increments the "tick over budget" counter when a
  tick exceeds `tickInterval` (33 ms), matching SPEC §11.
- **Load harness** (`internal/loadtest`) — an in-process synthetic
  client (reusing the lobby→match handshake pattern from
  `cmd/isnipes/e2e_test.go`) and a driver that spawns N clients across M
  matches, sends inputs at 30 Hz, drains snapshots, and tallies
  end-to-end input→snapshot latency, per-client bytes in/out,
  `runtime.NumGoroutine`, and RSS (Linux `/proc/self/statm`, with a
  `runtime.MemStats` fallback). Returns a typed `Report`.
- **Load CLI** (`scripts/load_test.go`) — a thin `package main` wrapper
  around `internal/loadtest` exposing `--matches`, `--clients`,
  `--duration`, `--nightly`, and `--soak` flags, printing the `Report`.
  Used by `make perf-nightly` and for manual runs.
- **Smoke gate** (`internal/loadtest/smoke_test.go`, build tag
  `loadtest`) — 4 matches × 4 clients (16 players) × 60 s: asserts no
  client errors, no match aborts, P99 tick budget < 10 ms, no goroutine
  leak, and mean per-client bandwidth ≤ 12 KB/s. Run via `make
  test-load` as a dedicated CI step (kept out of the default `go test
  ./...` so unit runs stay fast).
- **Embed/build pipeline** (`cmd/isnipes`, `Makefile`,
  `web/package.json`) — `//go:embed` moves behind a build tag so a plain
  `go build ./cmd/isnipes` succeeds with an empty `dist/` (serving a
  clear "build with `-tags embed` or pass `--web-dist`" notice), and
  `make build` runs the web build then `go build -tags embed`, baking
  the real client in. A default-asset test asserts the embed build
  serves the real bundle, not the placeholder.
- **Deployment artifacts** — `Dockerfile` (multi-stage: node build → go
  `-tags embed` build → `FROM scratch`), `.dockerignore`,
  `deploy/isnipes.service` (hardened systemd unit),
  `deploy/nginx.conf` (TLS termination + WS upgrade proxy), and a
  deploy guide in `deploy/README.md`.
- **CI wiring** (`.github/workflows/`) — a `ci.yml` running the PR gates
  (vet, `make test-race` incl. the frozen guard, `make test-testhooks`,
  vitest + coverage, Playwright e2e, `make test-load`) and a
  `nightly.yml` running `make perf-nightly`. Wiring the gates is what
  makes the Phase 7 frozen guard and golden diffs actually enforced.

**Definition of done.** All non-deferred items in §20 pass on `main`.

---

## 2. Out of scope

Explicitly **not** built in Phase 8:

- **Any wire-schema change.** `schemaChecksum` frozen; `proto.ts` /
  `checksum.go` untouched. The harness speaks the existing protocol.
- **Any `internal/sim` change.** Determinism fingerprints frozen.
- **Any new client gameplay/render feature.** Phase 8 touches `web/`
  only in `package.json` (a `build` already exists) and the build
  pipeline; no `web/src/*.ts` behaviour change.
- **A prometheus/client_golang dependency** — default is a
  dependency-free text exposition (§19.1). Adopting the library is a
  documented alternative, not the Phase 8 default.
- **Distributed tracing / OpenTelemetry / external metrics push** —
  v1.1+. Phase 8 exposes a scrape endpoint only.
- **Horizontal scaling / multi-process sharding / a match scheduler
  across hosts** — v1 is a single process (SPEC §1.2, §12 "Scope
  creep").
- **Kubernetes manifests / Helm / cloud-specific IaC** — the deploy
  guide covers Docker + systemd + nginx; orchestration is operator
  choice.
- **A real GitHub Actions *run*** — Phase 8 authors the workflow YAML
  and asserts it wires the named `make` targets; executing it on
  GitHub's infra is operator-side (the workflow is the deliverable).
- **24 h soak and 512-player loads as PR gates** — they live in `make
  perf-nightly`, are regression/alert-style, and are deferred-to-operator
  (SPEC §8 Phase 8 rationale; §9 CI-vs-nightly split).

---

## 3. Prerequisites and assumptions

- Phases 1–7 are on `main`. `internal/match` exposes `TickHz = 30`,
  `tickInterval = time.Second / TickHz`, an injectable `clock()` and
  `Ticker`, and the actor's `Run` loop calls `m.tick()` from a single
  goroutine (so per-tick timing needs no lock on the write side).
- `match.Registry` (`match.NewRegistry`) owns live matches and is the
  natural place for the harness and `/metrics` to enumerate active
  matches and joined-player counts. The lobby creates rooms and mints
  `joinToken`s; the match WS validates `MatchJoin` (SPEC §6.3).
- `cmd/isnipes/e2e_test.go` already implements in-process lobby and
  match clients (`dialLobby`, `dialMatch`, `lobbyClient`, `matchClient`)
  over `httptest.Server` using `nhooyr.io/websocket`. The load harness
  reuses that exact handshake shape; it is **not** a new protocol.
- `cmd/isnipes/main.go` embeds `all:dist` unconditionally today and
  `--enable-pprof` is a no-op (`_ = enablePprof`, line 85). `dist/`
  contains only the Phase 2 placeholder `index.html` plus `.gitkeep`;
  the real bundle (`app.js` + `index.html`) is produced by `npm -C web
  run build` into `web/dist/` and copied into `cmd/isnipes/dist/` by the
  `Makefile` `build` target — but that target does not currently run the
  web build, so a fresh `make build` embeds the placeholder.
- The web build is **esbuild + npm** (`npm -C web run build` →
  `dist/app.js` + `dist/index.html`), not pnpm/Vite. SPEC §10 sketches
  `pnpm build`; Phase 8 keeps the actual npm/esbuild toolchain (open
  question §19.4, mirrors P7 §19.3).
- Go ≥ 1.22 (`go.mod`), toolchain 1.26.x. Single runtime dependency is
  `nhooyr.io/websocket`; Phase 8 adds **no** runtime dependency under
  the default metrics design (§19.1).
- Target deploy OS is Linux/amd64 (RSS via `/proc/self/statm`); the RSS
  probe degrades to `runtime.MemStats.Sys` on non-Linux so the harness
  still runs cross-platform (with a documented caveat that the RSS
  figure is approximate off-Linux).

---

## 4. Package and file layout

New files in Phase 8:

```
internal/observ/
├── metrics.go        # NEW — Registry: histogram + counters + /metrics handler (§6)
├── histogram.go      # NEW — lock-free atomic fixed-bucket latency histogram (§6.1)
├── metrics_test.go   # NEW
└── histogram_test.go # NEW

internal/loadtest/
├── client.go         # NEW — in-process synthetic lobby→match client (§7.1)
├── driver.go         # NEW — spawns N clients × M matches; collects Report (§7.2)
├── report.go         # NEW — Report type + Markdown/text formatting (§7.3)
├── proc.go           # NEW — RSS probe (/proc/self/statm; MemStats fallback) (§7.4)
├── driver_test.go    # NEW — tiny in-process functional test (default-tagged, fast)
└── smoke_test.go     # NEW — //go:build loadtest — the PR smoke gate (§8)

scripts/
└── load_test.go      # NEW — //go:build ignore — package main CLI over loadtest (§7.5)

deploy/
├── README.md         # NEW — build / docker / systemd / nginx / flags / endpoints (§13)
├── isnipes.service   # NEW — hardened systemd unit (§12.1)
└── nginx.conf        # NEW — TLS termination + WS upgrade reverse proxy (§12.2)

.github/workflows/
├── ci.yml            # NEW — PR gates (§14)
└── nightly.yml       # NEW — perf-nightly (§14)

Dockerfile            # NEW — multi-stage, FROM scratch (§11)
.dockerignore         # NEW
```

Existing files modified by Phase 8:

```
cmd/isnipes/
├── main.go           # mount net/http/pprof when --enable-pprof; mount observ
│                     # /metrics; move //go:embed behind a build tag (embed_on.go /
│                     # embed_off.go split); wire the match tick sampler into the
│                     # registry config.
├── embed_on.go       # NEW — //go:build embed — //go:embed all:dist + real staticFS
├── embed_off.go      # NEW — //go:build !embed — placeholder notice FS
└── default_asset_test.go # NEW — //go:build embed — GET / serves the real bundle (§10)

internal/match/
├── match.go          # time m.tick() via clock(); record into observ sampler
│                     # (nil-safe); count ticks-over-budget. No sim edit.
└── match_test.go     # extend — TestMatch_TickDurationRecorded

internal/net/
└── server.go         # (only if needed) expose byte counters to observ via an
                      # optional hook; no behaviour change when the hook is nil.

Makefile              # build runs web build then go build -tags embed; add
                      # test-load, perf-nightly, docker targets.
web/package.json      # (build script already emits dist/app.js+index.html) — no change
                      # expected; documented here as the embed source of truth.
.gitignore            # ignore /web/dist/ build output and any load-report artifacts.
```

`internal/sim/**`, `internal/proto/**`, `web/src/proto.ts`,
`web/src/sim.ts`, `web/src/prediction.ts`, `web/src/interp.ts`, and
`web/src/netClient.ts` are **NOT** edited (frozen guard, DoD #2). The
`internal/net/server.go` change is **optional and additive** — byte
accounting can instead live in the harness's own counting `io` wrapper
around the client WS, avoiding any server edit; that is the **default**
(§7.1), and the server hook is only added if in-process byte counts
prove insufficient. Either way no frame format changes.

---

## 5. Public API additions

```go
// internal/observ

// Histogram is a lock-free fixed-bucket latency histogram. Observe is
// safe for concurrent callers (atomic bucket increments); the read-side
// queries (P99, Count) are race-free snapshots.
type Histogram struct{ /* atomic bucket counts; upper bounds in µs */ }
func NewLatencyHistogram() *Histogram
func (h *Histogram) Observe(d time.Duration)
func (h *Histogram) Quantile(q float64) time.Duration // linear-interpolated bucket estimate
func (h *Histogram) Count() uint64
func (h *Histogram) Reset()

// Sampler is the minimal interface internal/match depends on so the
// match package does not import the whole registry. nil is a valid
// no-op sampler (the actor checks for nil before calling).
type Sampler interface{ Observe(d time.Duration) }

// Registry is the process-wide metrics surface scraped by /metrics.
type Registry struct{ /* tick histogram + atomic counters */ }
func NewRegistry() *Registry
func (r *Registry) TickHistogram() *Histogram
func (r *Registry) IncTickOverBudget()
func (r *Registry) AddBytesIn(n uint64)
func (r *Registry) AddBytesOut(n uint64)
func (r *Registry) SetActiveMatches(n int)
func (r *Registry) SetJoinedPlayers(n int)
func (r *Registry) IncSnapshotDrop()
func (r *Registry) WriteProm(w io.Writer)   // Prometheus text exposition
func (r *Registry) Handler() http.HandlerFunc // GET /metrics

// internal/loadtest

type Config struct {
    Matches      int           // M
    ClientsEach  int           // N per match
    Duration     time.Duration // run length
    InputHz      int           // default 30
    Soak         bool          // rotate clients for the whole Duration
    RotateEvery  time.Duration // soak client churn interval
}

type Report struct {
    Matches, Clients          int
    Duration                  time.Duration
    LatencyP50, LatencyP99    time.Duration // input.clientTick → echoing snapshot
    TickP50, TickP99          time.Duration // server tick budget (from observ)
    TicksOverBudget           uint64
    BytesInPerClientPerSec    float64       // KB/s
    BytesOutPerClientPerSec   float64       // KB/s
    GoroutinesBefore, GoroutinesAfter int
    RSSStartBytes, RSSEndBytes uint64
    ClientErrors              int
    MatchAborts               int
}
func (r Report) String() string             // human-readable table
func (r Report) GoroutineLeaked(slack int) bool
func (r Report) RSSGrewMoreThan(frac float64) bool

// Run boots an in-process server (httptest), drives the load, and
// returns the Report. The caller provides the observ.Registry so the
// harness can read tick percentiles the server recorded.
func Run(t testing.TB, cfg Config, reg *observ.Registry) Report
// RunStandalone is the CLI entry: boots its own server + registry,
// runs, prints the Report, returns a non-zero-on-failure bool.
func RunStandalone(cfg Config) (Report, bool)
```

```go
// internal/match — RegistryConfig / Config gains an optional sampler.
// When nil, the actor records nothing (current behaviour).
type RegistryConfig struct {
    MaxConcurrentMatches int
    TickSampler          observ.Sampler // NEW, optional
    OnTickOverBudget     func()         // NEW, optional
}
```

`internal/match` importing `internal/observ` only for the one-method
`Sampler` interface keeps the dependency direction clean
(match → observ, never the reverse). If even that import is unwanted,
the `Sampler`/`func()` hooks can be declared in `internal/match` and
`observ.Histogram` satisfies them structurally — decided at
implementation time, but the actor must not gain a hard dependency on
the HTTP/metrics surface.

---

## 6. Observability (`internal/observ`)

### 6.1 Tick histogram

Fixed exponential-ish buckets in microseconds covering the relevant
range for a 33 ms budget, e.g. upper bounds `{100µs, 250µs, 500µs, 1ms,
2ms, 5ms, 10ms, 20ms, 33ms, 50ms, 100ms, +Inf}`. `Observe` finds the
bucket and `atomic.AddUint64`s its count and the running total/count;
no lock. `Quantile(q)` walks cumulative counts to the target bucket and
linearly interpolates within it (a standard histogram-quantile
estimate; documented as an estimate, sufficient for a < 10 ms / < 5 ms
budget assertion against buckets that bracket those thresholds). DoD #7.

### 6.2 Counters and `/metrics`

Atomic counters: `isnipes_ticks_total`, `isnipes_ticks_over_budget_total`,
`isnipes_bytes_in_total`, `isnipes_bytes_out_total`,
`isnipes_snapshot_drops_total`; gauges `isnipes_active_matches`,
`isnipes_joined_players`; histogram `isnipes_tick_seconds` exported as
Prometheus `_bucket`/`_sum`/`_count` lines. `WriteProm` emits valid
text exposition (HELP/TYPE comments, sorted, `\n`-terminated).
`Handler()` sets `Content-Type:
text/plain; version=0.0.4; charset=utf-8`. DoD #8.

### 6.3 pprof mount (gated)

`cmd/isnipes/main.go` registers the stdlib `net/http/pprof` handlers on
the existing mux **only** when `--enable-pprof` is true. Default off:
production exposes `/healthz`, `/version`, `/metrics`, `/ws/*`, and the
static client — *not* pprof. `/metrics` is always on (scrape target);
if an operator wants it private they front it with nginx (§12.2). DoD #9.

### 6.4 Match tick instrumentation

In `internal/match` the `Run` loop wraps the `StateLive` `m.tick()`
call:

```
start := m.clock()
m.tick()
d := m.clock().Sub(start)
if s := m.tickSampler; s != nil { s.Observe(d) }
if d > tickInterval && m.onTickOverBudget != nil { m.onTickOverBudget() }
```

Both hooks are nil in every existing test and in any boot that does not
pass them, so behaviour is unchanged and `internal/sim` is untouched.
The registry sets `active_matches`/`joined_players` gauges by
periodically reading `match.Registry` counts (a 1 Hz updater goroutine
in `cmd/isnipes`, stopped on shutdown). DoD #10.

---

## 7. Load harness (`internal/loadtest`)

### 7.1 Synthetic client

One synthetic client = one goroutine that: dials `/ws/lobby`, creates or
joins a room to obtain `matchID` + `joinToken` (or the driver pre-creates
rooms and hands tokens out), dials `/ws/match/{id}`, sends `MatchJoin`
(`schemaChecksum` = `proto.SchemaChecksum()`), then loops at `InputHz`
sending `Input` frames (a deterministic walk pattern keyed off the
client index so the sim does real work) and concurrently drains inbound
frames. It records, per inbound `Snapshot`, the end-to-end latency as
`now − sendTimeOf(snapshot.yourLastInputTick)` and tallies bytes via a
counting wrapper around the WS read/write (so **no server edit** is
needed for bandwidth). On any protocol/IO error it increments
`ClientErrors` and exits cleanly. Reuses the exact frame helpers from
`cmd/isnipes/e2e_test.go` (extracted/shared, not duplicated where
practical).

### 7.2 Driver

`Run(t, cfg, reg)`: boots `httptest.NewServer(srv.Handler())` with a
`match.Registry` whose `RegistryConfig.TickSampler =
reg.TickHistogram()` and `OnTickOverBudget = reg.IncTickOverBudget`,
records `GoroutinesBefore`/`RSSStart`, spawns `Matches × ClientsEach`
clients, runs for `Duration`, then signals stop, waits for all clients,
tears down the server, calls `runtime.GC()` + a short settle, and
records `GoroutinesAfter`/`RSSEnd`. It pulls `TickP50/TickP99` from the
`observ` histogram and computes per-client KB/s from the byte tallies.
`MatchAborts` is read from a registry counter / `MatchOver{reason =
SERVER_ERROR}` observations.

### 7.3 Report

`Report.String()` prints a fixed-width table (used by the CLI and on
test failure). `GoroutineLeaked(slack)` = `After > Before + slack`.
`RSSGrewMoreThan(frac)` = `RSSEnd > RSSStart * (1+frac)`. The smoke gate
and nightly job assert against these. DoD #3–#6, #18.

### 7.4 RSS probe (`proc.go`)

`func rssBytes() uint64`: on Linux, read `/proc/self/statm`, multiply
the resident field by `os.Getpagesize()`; elsewhere return
`MemStats.Sys` (documented approximation). No cgo.

### 7.5 CLI (`scripts/load_test.go`)

`//go:build ignore` so it never compiles into `go build ./...` or
`go test ./...` (run via `go run scripts/load_test.go [flags]`). Flags:
`--matches` (default 4), `--clients` (default 4), `--duration` (default
60s), `--input-hz` (30), `--nightly` (sets 64 × 8, 60 s, regression
print), `--soak` (sets `Soak`, default 24 h, `--rotate-every` 30 s).
Calls `loadtest.RunStandalone`, prints the `Report`, exits non-zero only
for `--nightly`/smoke-style hard failures (soak is report-only). DoD #18,
#19.

---

## 8. Smoke gate (PR-blocking, tag `loadtest`)

`internal/loadtest/smoke_test.go` (`//go:build loadtest`):

- `TestLoad_Smoke` runs `Run(t, Config{Matches:4, ClientsEach:4,
  Duration:60s, InputHz:30}, reg)` and asserts:
  - `ClientErrors == 0` and `MatchAborts == 0` (DoD #3),
  - `report.TickP99 < 10 * time.Millisecond` (DoD #4),
  - `!report.GoroutineLeaked(allowedSlack)` (DoD #5; `allowedSlack`
    small, e.g. 4, to tolerate runtime/httptest residue),
  - `report.BytesInPerClientPerSec ≤ 12` and
    `report.BytesOutPerClientPerSec ≤ 12` KB/s (DoD #6).
- Under `go test -short` the duration drops to ~5 s for a fast local
  sanity pass; CI runs it **without** `-short` (full 60 s) via `make
  test-load`. The 60 s gate lives in its own CI step, not in
  `go test ./...`.

`make test-load` = `go test -race -count=1 -tags loadtest
./internal/loadtest/...`.

---

## 9. Nightly / soak (deferred-to-operator, `make perf-nightly`)

- **Larger load** — 64 matches × 8 clients (512 players), 60 s on a
  4-core box; sustain 30 Hz; **regression-style**: print `TickP99`,
  flag if it worsened versus a committed/last baseline, but do **not**
  hard-fail (SPEC §8: "aspirational … enforced as a *regression* test").
- **Bandwidth target** — ≤ 8 KB/s per client at that scale (post-AOI);
  report-only.
- **Soak (24 h)** — `--soak`, rotating clients; assert (report-only)
  `/debug/pprof/goroutine` flat (goroutine count stable) and RSS growth
  < 5 % over the window.

`make perf-nightly` = `go run scripts/load_test.go --nightly` (and a
documented `--soak` invocation). These are unsuitable as PR gates
(infra + runtime); they are **deferred-to-operator** and surfaced as
`.github/workflows/nightly.yml` (scheduled). DoD #18 (file/target +
512-scale run shape) and #19 (soak shape) are operator-run.

---

## 10. Embed / build pipeline

The embed moves behind a build tag to decouple `go build` from a
populated `dist/`:

- `embed_on.go` (`//go:build embed`): `//go:embed all:dist`,
  `embeddedStatic()` returns the `fs.Sub(embeddedFS, "dist")`.
- `embed_off.go` (`//go:build !embed`): `embeddedStatic()` returns a
  tiny in-memory FS whose `index.html` says the binary was built without
  embedded assets and to run with `--web-dist DIR` or rebuild with
  `make build`.
- `main.go`: `staticFS = os.DirFS(*webDist)` if `--web-dist` set, else
  `embeddedStatic()`. `--web-dist` still wins (the e2e harness path is
  unchanged).
- `Makefile` `build`: `npm -C web ci || npm -C web install` →
  `npm -C web run build` → `rm -rf cmd/isnipes/dist/* (keep .gitkeep)` →
  `cp -r web/dist/* cmd/isnipes/dist/` → `go build -tags embed -o
  isnipes ./cmd/isnipes`. The committed `cmd/isnipes/dist/` keeps only
  `.gitkeep` (build artifacts are git-ignored; the placeholder
  `index.html` is removed).
- `cmd/isnipes/default_asset_test.go` (`//go:build embed`): builds the
  static handler from the embedded FS and asserts `GET /` returns the
  real bundle — body references `app.js` and does **not** contain the
  string "Phase 2 placeholder". This test only runs under `-tags embed`
  (so CI must build/test the embed tag once assets exist). DoD #11, #13.
- `embed_off` fallback is covered by `TestMain_EmbedOffNotice` (default
  tag): the non-embed static FS serves the notice, never the game. DoD
  #12.

Because built assets are not committed, the embed-tag tests run in CI
*after* `npm -C web run build` populates `cmd/isnipes/dist/`. The
default (untagged) `go test ./...` does not require dist and exercises
the `embed_off` path.

---

## 11. Dockerfile (multi-stage, `FROM scratch`)

```
# Stage 1: web build (node)
FROM node:20-alpine AS web
WORKDIR /web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build            # → /web/dist/{app.js,index.html}

# Stage 2: go build with embedded assets
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /web/dist/ ./cmd/isnipes/dist/
RUN CGO_ENABLED=0 GOOS=linux go build -tags embed -trimpath \
      -ldflags "-s -w" -o /isnipes ./cmd/isnipes

# Stage 3: minimal final
FROM scratch
COPY --from=build /isnipes /isnipes
EXPOSE 8080
USER 65534:65534                # nobody; scratch has no /etc/passwd, numeric UID
ENTRYPOINT ["/isnipes"]
CMD ["--addr=:8080"]
```

- `FROM scratch` + `CGO_ENABLED=0` static binary → image ≈ the binary
  size (target ~10 MB; DoD allows ≤ 15 MB to absorb toolchain drift).
- No shell/TLS-cert layer: TLS is terminated by nginx (§12.2) or the
  binary's own `--require-tls` if later added; for HTTPS *from the
  container directly* an operator must add a CA bundle layer (documented
  in §13 as a known limitation of `scratch`).
- `.dockerignore` excludes `node_modules`, `web/dist`, `.git`,
  `*_test.go` is **not** excluded (build only compiles non-test), test
  caches, `.codex`, `.phase-loop-notes.md`.

DoD #14 (Dockerfile present + multi-stage + scratch; build/run/size
check is operator/CI — requires a Docker daemon, deferred-to-operator
for the loop).

---

## 12. systemd + nginx examples

### 12.1 `deploy/isnipes.service`

Hardened unit: `DynamicUser=yes` (or a dedicated `isnipes` user),
`NoNewPrivileges=yes`, `ProtectSystem=strict`, `ProtectHome=yes`,
`PrivateTmp=yes`, `RestrictAddressFamilies=AF_INET AF_INET6`,
`CapabilityBoundingSet=` (empty), `AmbientCapabilities=` (none — binds
:8080, an unprivileged port), `Restart=on-failure`,
`ExecStart=/usr/local/bin/isnipes --addr=127.0.0.1:8080` (loopback;
nginx fronts it). Documented `[Install] WantedBy=multi-user.target`.
DoD #15.

### 12.2 `deploy/nginx.conf`

`server {}` on 443 with TLS (`ssl_certificate` / `ssl_certificate_key`
placeholders), `location / { proxy_pass http://127.0.0.1:8080; }` with
the WebSocket upgrade dance (`proxy_http_version 1.1`, `Upgrade` /
`Connection` headers, `proxy_read_timeout` ≥ the 5 s idle ping window
with margin, e.g. 60 s), and a `location /metrics { allow 127.0.0.1;
deny all; }` example so the scrape surface is not public. A `:80 → :443`
redirect server block. DoD #16.

---

## 13. Deploy README (`deploy/README.md`)

Documents: `make build` (single static binary), `docker build` /
`docker run -p 8080:8080`, the systemd install (copy binary to
`/usr/local/bin`, unit to `/etc/systemd/system`, `daemon-reload`,
`enable --now`), nginx TLS termination, the full flag list (`--addr`,
`--max-matches`, `--motd`, `--log-level`, `--enable-pprof`,
`--web-dist`, `--version`), the served endpoints (`/`, `/healthz`,
`/version`, `/metrics`, `/ws/lobby`, `/ws/match/{id}`,
`/debug/pprof/*` when enabled), the `scratch`-image HTTPS-from-container
caveat, and how to read `/metrics` (key series + the < 10 ms / < 5 ms
tick budgets). DoD #17.

---

## 14. CI wiring (`.github/workflows/`)

- `ci.yml` (on push / PR): `go vet ./...`; `make test-race` (includes
  the frozen guard via its prereq, DoD #2); `make test-testhooks`;
  `npm -C web ci` + `npm -C web run test:coverage`; `npm -C web run
  test:e2e` (Playwright, Chromium); `make test-load`. Go matrix may
  include linux/amd64 + linux/arm64 for the sim determinism cross-check
  (SPEC §12) — at minimum amd64.
- `nightly.yml` (scheduled `cron`): `make perf-nightly`; the strict
  cross-browser golden job deferred from P7 §20 may also live here.
- The deliverable is **valid, correctly-wired YAML** referencing the
  real `make` targets and scripts; whether a GitHub runner executes it
  is operator-side. DoD #20 verifies the file parses and names the
  targets.

---

## 15. Concurrency rules

- The tick histogram write side is single-goroutine **per match**
  (`Run`), but multiple match actors `Observe` the *same* shared
  histogram concurrently → bucket increments are `atomic`. Read side
  (`/metrics`, harness) snapshots atomically; an in-flight increment may
  or may not be visible, which is fine for a percentile estimate.
- The 1 Hz gauge updater in `cmd/isnipes` reads `match.Registry` counts
  with the registry's existing synchronization; it is a separate
  goroutine stopped on shutdown (no leak — joined before exit).
- The load harness owns its clients' goroutines and joins all of them
  before reading `GoroutinesAfter`, so the leak check measures real
  residue, not in-flight teardown.
- pprof handlers are stdlib; mounting them adds no new concurrency.

---

## 16. Test plan

### 16.1 internal/observ
- `TestObserv_TickHistogramPercentile` (#7) — known samples →
  `Quantile(0.99)` lands in the bracketing bucket; `Count` exact;
  `Reset` clears.
- `TestObserv_HistogramConcurrentObserve` — N goroutines `Observe`;
  total `Count` == N (race-free under `-race`).
- `TestObserv_MetricsEndpoint` (#8) — `Handler` returns 200, correct
  content-type, parseable text exposition with the documented series.

### 16.2 internal/match
- `TestMatch_TickDurationRecorded` (#10) — a fake clock + fake sampler;
  after K live ticks the sampler saw K observations; over-budget hook
  fires when a tick exceeds `tickInterval`; nil hooks = no-op (existing
  tests unaffected).

### 16.3 cmd/isnipes
- `TestMain_PprofGated` (#9) — handler with pprof off → `/debug/pprof/`
  404; with the flag on → 200. `/metrics` 200 in both.
- `TestMain_EmbedOffNotice` (#12) — default tag: static FS serves the
  "build with -tags embed" notice, not the game.
- `TestE2E_EmbeddedClientServed` (#13, `//go:build embed`) — embed build
  `GET /` body references `app.js`, no "Phase 2 placeholder".

### 16.4 internal/loadtest
- `TestLoad_DriverSmall` — default-tagged, fast (2 matches × 2 clients ×
  ~2 s): `ClientErrors==0`, latency populated, bytes counted, no
  goroutine leak. Keeps the harness honest in `go test ./...` without
  the 60 s gate.
- `TestLoad_Smoke` (#3–#6, `//go:build loadtest`) — the PR gate (§8).
- `TestLoad_ReportHelpers` — `GoroutineLeaked` / `RSSGrewMoreThan` /
  `String` unit-level.

### 16.5 deploy / docker / CI (file-shape checks, loop-runnable)
- `TestDeploy_FilesPresent` (or a Make/script check) — `Dockerfile`,
  `.dockerignore`, `deploy/isnipes.service`, `deploy/nginx.conf`,
  `deploy/README.md`, `.github/workflows/ci.yml`,
  `.github/workflows/nightly.yml` exist; the workflows parse as YAML and
  reference `test-race`, `test-load`, `perf-nightly`; the Dockerfile has
  `FROM scratch` and `-tags embed`. DoD #14 (presence/shape), #20.

---

## 17. Risks

- **60 s smoke gate slows CI** → isolated in `make test-load` as its own
  step, `-short` gives a 5 s local pass, default `go test ./...` excludes
  it (build tag). 
- **Tick-budget flakiness on shared CI runners** → assert against
  buckets that bracket the threshold; the gate is P99 < 10 ms (generous
  vs the ~sub-ms real cost at 16 players); nightly's < 5 ms is
  regression-only, never hard-fail.
- **Histogram quantile is an estimate** → documented; thresholds chosen
  so the answer is unambiguous unless the server is genuinely over
  budget; `Count`/`_sum` exported for cross-checking.
- **Goroutine-leak false positives** → join all clients, `runtime.GC()`
  + settle before the after-count; small `allowedSlack`; the check is
  "no *growth* beyond slack", not "exact".
- **RSS probe portability** → Linux `/proc` primary, `MemStats.Sys`
  fallback with a documented caveat; the soak RSS check runs on the
  Linux deploy target.
- **`scratch` image can't do outbound TLS / has no certs** → documented;
  TLS terminates at nginx; a CA-bundle layer is shown for the
  direct-HTTPS case.
- **Embedding the wrong/stale client** → `make build` always rebuilds
  `web/dist` first; the `-tags embed` default-asset test fails if the
  placeholder slips back in (DoD #13).
- **pprof exposed in prod** → off by default; `/metrics` restricted via
  the nginx example.
- **Adding a metrics dep bloats the minimal-dep posture** → default is
  dependency-free text exposition (§19.1).

---

## 18. Determinism / no-regression guarantees

- `go test -race ./...` green incl. new `observ` / `loadtest` /
  `cmd/isnipes` tests; P1/P3/P5 fingerprints byte-identical (no
  `internal/sim` edit; the tick timing wraps `m.tick()` and changes no
  sim state).
- `schemaChecksum` `0x42607394`; `scripts/check-frozen.sh` green over
  its existing manifest (DoD #2). Phase 8 edits no frozen file.
- All P4–P7 vitest + Playwright suites pass unchanged; Phase 8 adds no
  `web/src` behaviour. The embed-tag default-asset test is additive.
- The match actor's existing tests pass with nil tick hooks (default),
  proving the instrumentation is behaviour-neutral.

---

## 19. Open questions

Flagged for codex review / author decision. Defaults listed.

1. **Metrics: dependency-free text vs `prometheus/client_golang`.**
   Default: hand-rolled atomic histogram + text exposition (keeps the
   single-dependency posture; the surface is tiny and well-tested).
   Alt: adopt `client_golang` (the "boring standard", richer, but a new
   dependency tree and the project has deliberately stayed at one
   runtime dep). Recommend default; revisit if metrics needs grow.
2. **Load harness home: `internal/loadtest` package vs a single
   `scripts/load_test.go`.** SPEC §9 names `scripts/load_test.go`.
   Default: put the testable harness in `internal/loadtest` (importable
   by the PR-gated `*_test.go`) and keep `scripts/load_test.go` as the
   thin `//go:build ignore` CLI. A single un-importable `package main`
   file can't host a `go test` gate. Recommend the split; the named file
   still exists and `go run scripts/load_test.go --nightly` still works.
3. **Smoke duration as a PR gate.** SPEC says 60 s, PR-blocking.
   Default: keep 60 s in `make test-load` (its own CI step) with a
   `-short` 5 s local mode; do **not** put it in `go test ./...`. Alt:
   shorten the gate to 20 s (faster PRs, weaker signal). Recommend 60 s
   isolated.
4. **Web toolchain: npm/esbuild vs SPEC's pnpm.** Default: keep the
   actual npm + esbuild build (P6/P7 reality); the Dockerfile and
   Makefile use `npm ci` / `npm run build`. Documented deviation, mirrors
   P7 §19.3. Switching to pnpm is cosmetic and out of scope.
5. **Tick instrumentation coupling.** Default: `internal/match` imports
   `internal/observ` only for the one-method `Sampler` interface
   (clean, match → observ). Alt: declare the hook types in
   `internal/match` and let `observ.Histogram` satisfy them structurally
   (zero import). Either is fine; the binding rule is the actor must not
   depend on the HTTP/metrics surface and `internal/sim` stays untouched.
6. **Docker base for the build stage / final size budget.** Default:
   `golang:1.26-alpine` build → `FROM scratch` final, target ~10 MB, DoD
   tolerance ≤ 15 MB. Alt: `gcr.io/distroless/static` final (adds certs
   + nonroot user + tzdata, ~2 MB more) for direct-HTTPS without a manual
   CA layer. Recommend `scratch` for v1 with the distroless note in the
   deploy README.
7. **CI: GitHub Actions vs the SPEC's "or equivalent".** Default:
   author `.github/workflows/{ci,nightly}.yml`. If the project's CI is
   elsewhere, the same `make` targets are the contract and the YAML is
   illustrative. DoD #20 checks the file shape, not a live run.

---

## 20. Definition of done (canonical checklist)

| # | Item | Verified by |
|---:|---|---|
| 1 | `go test -race ./...` green incl. new observ/loadtest/cmd tests; P1/P3/P5 fingerprints byte-identical | CI |
| 2 | `schemaChecksum` `0x42607394`; `scripts/check-frozen.sh` green; no frozen file edited | `TestSchemaChecksumValue` + `make check-frozen` |
| 3 | `make test-load` smoke (4×4=16 players, 60 s): no client errors, no match aborts | `TestLoad_Smoke` §16.4 |
| 4 | Smoke P99 server tick budget < 10 ms | `TestLoad_Smoke` |
| 5 | Smoke: no goroutine leak after teardown (≤ baseline + slack) | `TestLoad_Smoke` |
| 6 | Smoke: mean per-client bandwidth ≤ 12 KB/s in **and** out | `TestLoad_Smoke` |
| 7 | `internal/observ` tick histogram: `Quantile`/`Count`/`Reset`, race-free concurrent `Observe` | `TestObserv_TickHistogramPercentile` §16.1 |
| 8 | `/metrics` serves valid Prometheus text exposition with the documented series | `TestObserv_MetricsEndpoint` §16.1 |
| 9 | `--enable-pprof` mounts `/debug/pprof/*` only when set; off by default; `/metrics` always on | `TestMain_PprofGated` §16.3 |
| 10 | Match actor records tick duration via nil-safe sampler + over-budget hook; no sim edit | `TestMatch_TickDurationRecorded` §16.2 |
| 11 | `make build` runs the web build then `go build -tags embed`; binary embeds the real client | `make build` + #13 |
| 12 | Plain `go build ./cmd/isnipes` (no embed) builds with empty dist and serves the "build with -tags embed / --web-dist" notice | `TestMain_EmbedOffNotice` §16.3 |
| 13 | Embed build `GET /` serves the real bundle (references `app.js`, no "Phase 2 placeholder") | `TestE2E_EmbeddedClientServed` (`-tags embed`) §16.3 |
| 14 | `Dockerfile` multi-stage (node → go `-tags embed` → `FROM scratch`) + `.dockerignore` present; build/run/size ≤ 15 MB | file shape (§16.5) + **deferred-to-operator** docker build |
| 15 | `deploy/isnipes.service` hardened systemd unit present | §16.5 |
| 16 | `deploy/nginx.conf` TLS-termination + WS-upgrade proxy present | §16.5 |
| 17 | `deploy/README.md` documents build/docker/systemd/nginx/flags/endpoints | §16.5 |
| 18 | `make perf-nightly` runs the 512-player (64×8) load, regression-style P99/bandwidth report | **deferred-to-operator** (`load_test --nightly`) §9 |
| 19 | Soak (`load_test --soak`) reports goroutine-flat + RSS growth < 5 % over the window | **deferred-to-operator** §9 |
| 20 | `.github/workflows/{ci,nightly}.yml` parse and wire `test-race`/`test-load`/`perf-nightly`; Dockerfile names `FROM scratch` + `-tags embed` | `TestDeploy_FilesPresent` §16.5 |
| 21 | Per-file coverage ≥ 70 % for `internal/observ` and `internal/loadtest` | `go test -cover` + CI |

Items 1–13, 15–17, 20, 21 are gate-able inside the implementation loop.
Items 14 (Docker build/run/size — needs a Docker daemon), 18 and 19
(512-player / 24 h soak — infra + runtime) are **deferred-to-operator**
and non-PR-blocking per SPEC §8 Phase 8 and §9; their *artifacts*
(Dockerfile, make targets, CLI flags, workflow files) are produced and
shape-checked in the loop.

The binary wire protocol is unchanged (`schemaChecksum = 0x42607394`).
`internal/sim/**`, `internal/proto/**`, `web/src/proto.ts`,
`web/src/sim.ts`, `web/src/prediction.ts`, `web/src/interp.ts`, and
`web/src/netClient.ts` are not edited; the sole server changes are the
additive, nil-safe tick instrumentation in `internal/match` and the
pprof/`metrics`/embed wiring in `cmd/isnipes`.
