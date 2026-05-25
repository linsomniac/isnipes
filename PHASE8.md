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
- **The frozen-file guard stays green and is *strengthened*.** Today
  `scripts/check-frozen.sh` locks `internal/sim/**`,
  `internal/proto/checksum.go`, and `web/src/{proto,sim,prediction,
  interp,netClient}.ts` — but **not** the wire encoders
  `internal/proto/frame.go` / `internal/proto/messages.go`, so the
  current manifest does not fully back the "no wire change" claim. Phase
  8 expands the manifest to cover **all** of `internal/proto/*.go` and
  adds a CI path-diff gate (§14) over those paths *plus the guard
  script and manifest themselves*, so the freeze cannot be silently
  re-seeded without showing up in review. Phase 8 edits none of the
  frozen *source* files; it only edits `scripts/check-frozen.sh` and
  re-seeds `scripts/frozen.sha256` to widen coverage (both intentional,
  reviewed, and out of the frozen set).

Phase 8 ships:

- **Observability** (`internal/observ`) — a dependency-free metrics
  registry: a lock-free tick-duration histogram (atomic fixed buckets)
  used for `/metrics` reporting, counters (active matches, joined
  players, bytes in/out, snapshot drops, ticks over budget), and a
  `/metrics` handler emitting Prometheus **text exposition** format. No
  new module dependency (open question §19.1). **`/metrics` and pprof
  are served on a separate admin listener** (`--admin-addr`, default
  `127.0.0.1:6060`, settable to empty to disable), **never** on the
  public `--addr` listener — so traffic/runtime internals are not
  exposed to the internet (§6.3).
- **pprof, actually mounted, admin-only** (`cmd/isnipes`) —
  `--enable-pprof` currently sets a discarded local
  (`_ = enablePprof`); Phase 8 mounts `net/http/pprof` under
  `/debug/pprof/*` on the **admin** listener **only** when the flag is
  set (off by default). The public listener never serves pprof or
  `/metrics`; the nginx example additionally denies both paths (§12.2)
  as defence in depth.
- **Tick-budget instrumentation + over-budget catch-up**
  (`internal/match`) — the actor times each `m.tick()` via the
  injectable `clock()` and records the duration into an `observ`-supplied
  sampler (nil-safe; zero behaviour change when unset). It also
  implements the canonical SPEC §11 over-budget rule, which is currently
  **unimplemented** (`match.go:752` broadcasts every 2nd tick
  unconditionally): when a tick exceeds `tickInterval` (33 ms), the
  actor **suppresses the next scheduled snapshot** (15 Hz → one skipped
  beat to catch up; never accumulated) and increments
  `isnipes_snapshot_drops_total` and the over-budget counter. The drop
  counter counts *snapshots suppressed by the over-budget rule* (not AOI
  trimming). This is an actor-only change — no `internal/sim` or wire
  edit. See §6.5.
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
- **Transport security** (`cmd/isnipes`, `internal/net`) — two changes
  SPEC §12 (open-question 5) requires for a production-facing v1:
  (a) **direct TLS** via `--require-tls`, `--tls-cert`, `--tls-key`
  (the binary can terminate TLS itself, the documented alternative to
  fronting with nginx); and (b) a **WebSocket origin policy** replacing
  today's blanket `InsecureSkipVerify: true`
  (`internal/net/server.go:83,149`, which accepts any `Origin` and is a
  cross-site-WebSocket-hijack risk). Default becomes same-origin
  enforcement (nhooyr's default when `InsecureSkipVerify=false` and
  `OriginPatterns` is empty), with `--allowed-origins` for extra hosts
  and `--insecure-origin` as an explicit dev override. Neither file is
  frozen; no frame changes, so `schemaChecksum` is untouched.
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

testdata/
└── perf_baseline.json # NEW — committed nightly regression baseline (§9)

cmd/isnipes/testdata/
├── tls_cert.pem      # NEW — self-signed cert for TestMain_DirectTLS (§16.3)
└── tls_key.pem       # NEW

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
├── main.go           # add the admin listener (--admin-addr) serving observ
│                     # /metrics + (when --enable-pprof) net/http/pprof — NOT on
│                     # the public --addr mux; add --require-tls/--tls-cert/
│                     # --tls-key + --allowed-origins/--insecure-origin; move
│                     # //go:embed behind a build tag (embed_on.go / embed_off.go
│                     # split); wire the match tick sampler + over-budget hook into
│                     # the registry config.
├── embed_on.go       # NEW — //go:build embed — //go:embed all:dist + real staticFS
├── embed_off.go      # NEW — //go:build !embed — placeholder notice FS
└── default_asset_test.go # NEW — //go:build embed — GET / serves the real bundle (§10)

internal/match/
├── match.go          # time m.tick() via clock(); record into observ sampler
│                     # (nil-safe); over-budget rule suppresses next snapshot +
│                     # increments drop/over-budget counters; actor updates the
│                     # joined-players gauge transactionally on join/leave/drop.
│                     # No sim edit.
├── registry.go       # add StopAll(ctx): signal every match to end cleanly and
│                     # wait for the map to drain (deterministic teardown for the
│                     # harness + graceful shutdown); active-matches gauge updated
│                     # on Create/RemoveEnded.
└── match_test.go     # extend — TestMatch_TickDurationRecorded,
                      # TestMatch_OverBudgetSkipsNextSnapshot, TestRegistry_StopAll

internal/net/
└── server.go         # MANDATORY: replace InsecureSkipVerify:true with the origin
                      # policy (§ transport security); count on-the-wire frame bytes
                      # in/out into the observ registry (the ONLY producer of the
                      # production bytes series). Additive; no frame format change.

Makefile              # build runs web build then go build -tags embed; add
                      # test-load, perf-nightly, docker targets.
web/package.json      # (build script already emits dist/app.js+index.html) — no change
                      # expected; documented here as the embed source of truth.
.gitignore            # ignore /web/dist/ build output and any load-report artifacts.
scripts/check-frozen.sh + scripts/frozen.sha256  # widen the manifest to all
                      # internal/proto/*.go and re-seed (§1 invariant, §14).
```

`internal/sim/**`, `internal/proto/**` (the encoders themselves),
`web/src/proto.ts`, `web/src/sim.ts`, `web/src/prediction.ts`,
`web/src/interp.ts`, and `web/src/netClient.ts` are **NOT** edited
(frozen guard, DoD #2). Byte accounting for the **production** `/metrics`
series is owned by `internal/net` (the server is the only place that
sees real per-connection traffic); the **load-harness** additionally
counts its own client-side WS bytes for the load `Report` (§7.1) — these
are two distinct producers for two distinct consumers, not a substitute
for each other. No frame format changes either way.

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

// RecordingSampler keeps every observed duration. The smoke gate injects
// this (not the bucketed Histogram) so its P99 assertion is computed
// from EXACT samples, not a bucket-interpolated estimate (§6.1, §8).
type RecordingSampler struct{ /* mutex-guarded []time.Duration */ }
func (s *RecordingSampler) Observe(d time.Duration)
func (s *RecordingSampler) Quantile(q float64) time.Duration // exact, from sorted copy
func (s *RecordingSampler) Len() int

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
    TickP50, TickP99          time.Duration // EXACT, from the RecordingSampler (§6.1)
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
// internal/match — RegistryConfig gains optional, nil-safe hooks. When
// all are nil the actor behaves exactly as today.
type RegistryConfig struct {
    MaxConcurrentMatches int
    TickSampler          observ.Sampler // NEW, optional — Observe(tickDuration)
    OnTickOverBudget     func()         // NEW, optional — over-budget counter bump
    OnSnapshotDrop       func()         // NEW, optional — over-budget snapshot skip
    OnJoinedDelta        func(delta int) // NEW, optional — actor-owned joined gauge
}

// StopAll signals every live match to end cleanly (a ctlShutdown control
// message → graceful MatchOver, NOT reason=SERVER_ERROR, so it is not
// counted as a MatchAbort) and waits until the registry map drains or
// ctx is done. Used by graceful shutdown AND the load-harness teardown
// so the goroutine-leak check samples after a deterministic drain
// (finding: Registry.Close does not stop matches; DC-grace actors
// otherwise linger).
func (r *Registry) StopAll(ctx context.Context) error
// Len reports the number of live matches (drives the active-matches
// gauge and the harness drain wait).
func (r *Registry) Len() int
```

The joined-players and active-matches gauges are updated **from inside
the owning goroutine** — the actor calls `OnJoinedDelta` on each
admit/leave/drop, and the registry adjusts active-matches on
`Create`/`RemoveEnded`. Nothing polls `Match.JoinedCount()` from outside
the actor (that read is explicitly unsafe on a live match,
`match.go:390`).

`internal/match` importing `internal/observ` only for the one-method
`Sampler` interface keeps the dependency direction clean
(match → observ, never the reverse). If even that import is unwanted,
the `Sampler`/`func()` hooks can be declared in `internal/match` and
`observ.Histogram` satisfies them structurally — decided at
implementation time, but the actor must not gain a hard dependency on
the HTTP/metrics surface.

---

## 6. Observability (`internal/observ`)

### 6.1 Tick histogram (observability) vs exact gate samples

Two distinct surfaces, deliberately separated:

- **`Histogram`** — fixed buckets in microseconds, e.g. upper bounds
  `{100µs, 250µs, 500µs, 1ms, 2ms, 5ms, 10ms, 20ms, 33ms, 50ms, 100ms,
  +Inf}`. `Observe` finds the bucket and `atomic.AddUint64`s its count
  plus the running sum/count; no lock. `Quantile(q)` walks cumulative
  counts and interpolates within the target bucket. This is the
  `/metrics` reporting surface and is explicitly **an estimate** — it is
  **not** used to decide a pass/fail gate.
- **`RecordingSampler`** — keeps every sample; `Quantile` sorts a copy
  and returns the **exact** rank. The smoke gate (§8) injects this as
  the match `TickSampler` and asserts `report.TickP99 < 10 ms` from
  exact samples, so a true P99 of exactly 10 ms cannot be smeared below
  the threshold by bucket interpolation (the prior wording incorrectly
  claimed the histogram alone could verify a strict bound). DoD #7.

### 6.2 Counters and `/metrics`

Atomic counters: `isnipes_ticks_total`, `isnipes_ticks_over_budget_total`,
`isnipes_bytes_in_total`, `isnipes_bytes_out_total`,
`isnipes_snapshot_drops_total`; gauges `isnipes_active_matches`,
`isnipes_joined_players`; histogram `isnipes_tick_seconds` exported as
Prometheus `_bucket`/`_sum`/`_count` lines. **Every series has a real
producer** (finding: do not promise series nothing increments):
- `bytes_in/out_total` — incremented by `internal/net`'s frame
  read/write path (the only code that sees real connection traffic);
- `snapshot_drops_total` — incremented by the actor's over-budget rule
  (§6.5);
- `ticks_total` / `ticks_over_budget_total` / `tick_seconds` — from the
  actor tick wrapper (§6.4);
- `active_matches` — adjusted by the registry on `Create`/`RemoveEnded`;
- `joined_players` — adjusted by each actor via `OnJoinedDelta` on
  admit/leave/drop (never polled from outside the actor).

`WriteProm` emits valid text exposition (HELP/TYPE comments, sorted,
`\n`-terminated). `Handler()` sets
`Content-Type: text/plain; version=0.0.4; charset=utf-8`. DoD #8.

### 6.3 Admin listener for `/metrics` + pprof (not public)

Metrics and profiling are **operational** surfaces and must not sit on
the internet-facing listener. `cmd/isnipes/main.go` starts a **second**
HTTP server on `--admin-addr` (default `127.0.0.1:6060`; empty disables
it). The admin mux serves `/metrics` always and the stdlib
`net/http/pprof` handlers **only** when `--enable-pprof` is set. The
public `--addr` mux serves exactly `/`, `/healthz`, `/version`, and
`/ws/*` — never `/metrics` or `/debug/pprof`. Defence in depth: the
nginx example (§12.2) also denies both paths on the proxied path. Both
servers share graceful shutdown. DoD #9.

### 6.4 Match tick instrumentation

In `internal/match` the `Run` loop wraps the `StateLive` `m.tick()`
call:

```
start := m.clock()
m.tick()                      // §6.5 decides whether this tick's snapshot is skipped
d := m.clock().Sub(start)
if s := m.tickSampler; s != nil { s.Observe(d) }
if d > tickInterval {
    m.overBudget = true       // consumed by the NEXT scheduled snapshot beat (§6.5)
    if m.onTickOverBudget != nil { m.onTickOverBudget() }
}
```

All hooks are nil in every existing test and in any boot that does not
pass them, so behaviour is unchanged and `internal/sim` is untouched.
DoD #10.

### 6.5 Over-budget snapshot suppression (SPEC §11)

SPEC §11 ("Sim tick over budget") requires: skip the next snapshot to
catch up, never accumulate, emit a metric. This is currently
**unimplemented** — `match.go:752` broadcasts on every
`ServerTick % snapshotEveryTicks == 0` unconditionally. Phase 8 adds an
actor-local one-shot flag:

```
if m.sim.ServerTick()%snapshotEveryTicks == 0 {
    if m.overBudget {
        m.overBudget = false          // consume; never accumulate
        if m.onSnapshotDrop != nil { m.onSnapshotDrop() }  // drops_total++
    } else {
        m.broadcastSnapshot()
    }
}
```

So a single over-budget tick suppresses exactly the next scheduled
snapshot beat (15 Hz → 7.5 Hz for one beat), then cadence resumes. The
flag is one-shot, so a burst of over-budget ticks within one snapshot
interval still skips only one beat. `isnipes_snapshot_drops_total`
counts **these** suppressions only. This is actor-only state; no
`internal/sim` and no wire change. Tested by
`TestMatch_OverBudgetSkipsNextSnapshot` (§16.2). DoD #10a.

---

## 7. Load harness (`internal/loadtest`)

### 7.1 Synthetic client + the bandwidth unit

One synthetic client = one goroutine that: dials `/ws/lobby`, creates or
joins a room to obtain `matchID` + `joinToken` (or the driver pre-creates
rooms and hands tokens out), dials `/ws/match/{id}`, sends `MatchJoin`
(`schemaChecksum` = `proto.SchemaChecksum()`), then loops at `InputHz`
sending `Input` frames (a deterministic walk pattern keyed off the
client index so the sim does real work) and concurrently drains inbound
frames. It records, per inbound `Snapshot`, the end-to-end latency as
`now − sendTimeOf(snapshot.yourLastInputTick)`. On any protocol/IO error
it increments `ClientErrors` and exits cleanly. Reuses the frame helpers
from `cmd/isnipes/e2e_test.go` (extracted into the shared harness, not
duplicated).

**Bandwidth unit (was ambiguous).** The `Report` bandwidth figures are
**application WebSocket-message bytes** — the byte length of each binary
WS message the client sends/receives (i.e. the isnipes frame: 8-byte
header + payload), summed per client and divided by wall-clock seconds.
This **excludes** WebSocket frame headers + client masking, the HTTP
upgrade handshake, and any TLS/proxy overhead; that overhead is a small,
bounded per-message constant (a few bytes) and is documented as
out-of-scope for the threshold. SPEC §8's 12 KB/s / 8 KB/s targets are
about *application snapshot bandwidth* (post-AOI), which this measures
directly. The DoD (§20) names the unit explicitly so the gate is
comparable. The same client-side tally drives only the load `Report`; it
is **not** the source of the production `bytes_*_total` series (those are
the server's, §6.2).

### 7.2 Driver + deterministic teardown

`Run(t, cfg, reg)`: boots `httptest.NewServer(srv.Handler())` with a
`match.Registry` whose `RegistryConfig.TickSampler` is an
`observ.RecordingSampler` (exact P99, §6.1), `OnTickOverBudget`/
`OnSnapshotDrop`/`OnJoinedDelta` wired to `reg`. It records
`GoroutinesBefore`/`RSSStart`, spawns `Matches × ClientsEach` clients,
runs for `Duration`, then:

1. signals all clients to stop and **joins every client goroutine**;
2. calls `registry.StopAll(ctx)` to end the test matches cleanly and
   **waits until `registry.Len() == 0`** (so no actor / DC-grace timer
   goroutine lingers — `Registry.Close` alone does *not* stop matches,
   `registry.go:71`);
3. shuts down the HTTP server and waits for its handler goroutines;
4. `runtime.GC()` + a short settle, then records
   `GoroutinesAfter`/`RSSEnd`.

Because clean `StopAll` shutdown ends matches with a graceful reason
(not `SERVER_ERROR`), it is **excluded** from `MatchAborts`;
`MatchAborts` counts only `MatchOver{reason = SERVER_ERROR}` /
panic-aborts observed *during* the run. `TickP50/TickP99` come from the
exact `RecordingSampler`; per-client KB/s from the client byte tallies.

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
compare against `testdata/perf_baseline.json`), `--soak` (sets `Soak`,
default 24 h, `--rotate-every` 30 s), `--baseline` (path, default
`testdata/perf_baseline.json`), `--update-baseline` (re-seed the
baseline and exit 0). Calls `loadtest.RunStandalone`, prints the
`Report`, and exits **non-zero** when `--nightly`/`--soak` detects a
flagged regression (§9) so the scheduled job goes red; a plain run with
no thresholds is report-only and exits 0. DoD #18, #19.

---

## 8. Smoke gate (PR-blocking, tag `loadtest`)

`internal/loadtest/smoke_test.go` (`//go:build loadtest`):

- `TestLoad_Smoke` runs `Run(t, Config{Matches:4, ClientsEach:4,
  Duration:60s, InputHz:30}, reg)` and asserts:
  - `ClientErrors == 0` and `MatchAborts == 0` (DoD #3),
  - `report.TickP99 < 10 * time.Millisecond`, computed from the **exact**
    `RecordingSampler`, not the bucketed histogram (DoD #4),
  - `!report.GoroutineLeaked(allowedSlack)` (DoD #5). Because §7.2 drains
    all matches via `StopAll` before sampling, the residual is ~0, so
    `allowedSlack` is *small* (e.g. 2, for genuine runtime/httptest
    residue) and does **not** mask lingering match actors,
  - `report.BytesInPerClientPerSec ≤ 12` and
    `report.BytesOutPerClientPerSec ≤ 12` (application WS-message KB/s,
    §7.1) (DoD #6).
- Under `go test -short` the duration drops to ~5 s for a fast local
  sanity pass; CI runs it **without** `-short` (full 60 s) via `make
  test-load`. The 60 s gate lives in its own CI step, not in
  `go test ./...`.

`make test-load` = `go test -race -count=1 -tags loadtest
./internal/loadtest/...`.

---

## 9. Nightly / soak (deferred-to-operator, `make perf-nightly`)

- **Larger load** — 64 matches × 8 clients (512 players), 60 s on a
  4-core box; sustain 30 Hz; **regression-style**, defined concretely:
  - **Baseline format** — a committed `testdata/perf_baseline.json`
    `{tick_p99_us, bytes_in_kbps, bytes_out_kbps, recorded_at, host}`.
    Absent/empty file → the run *writes* a baseline and exits 0 (first
    run seeds it).
  - **Comparison** — flag (print a `REGRESSION:` line) if
    `TickP99 > baseline.tick_p99_us × 1.20` (20 % tolerance to absorb
    runner noise) or bandwidth exceeds its target below.
  - **Failure semantics** — `--nightly` exits **non-zero** on a flagged
    regression so the scheduled CI job goes red; it never hard-fails a
    PR (it is not in the PR gate). `--update-baseline` re-seeds the file.
- **Bandwidth target** — ≤ 8 KB/s per client (application WS-message
  bytes, §7.1) at that scale (post-AOI); compared the same way.
- **Soak (24 h)** — `--soak`, rotating clients. The harness samples
  **in-process** every `--rotate-every` (default 30 s):
  `runtime.NumGoroutine()` and `rssBytes()` (§7.4) — **no pprof scrape
  is required** (pprof would only matter for a *remote* probe; in-process
  the runtime values are authoritative). It reports goroutine-count
  drift and `RSS_end / RSS_start − 1`; flags if goroutines trend upward
  (not flat) or RSS grows > 5 % over the window. Exits non-zero on a
  flagged soak regression.

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
- `Makefile` `build`: `npm -C web ci` (lockfile-pinned, **no**
  `|| npm install` fallback — a fallback would silently drift from the
  lockfile) → `npm -C web run build` → `find cmd/isnipes/dist -mindepth
  1 ! -name .gitkeep -delete` (clear stale chunks, keep `.gitkeep`) →
  `cp -r web/dist/* cmd/isnipes/dist/` → `go build -tags embed -o
  isnipes ./cmd/isnipes`. The committed `cmd/isnipes/dist/` keeps only
  `.gitkeep` (build artifacts are git-ignored; the placeholder
  `index.html` is removed). The clear-before-copy step prevents a
  renamed/old bundle from staying embedded.
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
# Pin builder images by digest in the real file (tag shown for clarity).
# Stage 1: web build (node)
FROM node:20-alpine AS web
WORKDIR /web
COPY web/package.json web/package-lock.json ./
RUN npm ci                   # lockfile-pinned; never npm install
COPY web/ ./
RUN npm run build            # → /web/dist/{app.js,index.html}

# Stage 2: go build with embedded assets
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# Clear any dist the build context carried in, then take ONLY the freshly
# built web output (no stale/renamed chunks survive into the embed).
RUN rm -rf cmd/isnipes/dist && mkdir -p cmd/isnipes/dist
COPY --from=web /web/dist/ ./cmd/isnipes/dist/
RUN CGO_ENABLED=0 GOOS=linux go build -tags embed -trimpath \
      -ldflags "-s -w" -o /isnipes ./cmd/isnipes

# Stage 3: minimal final
FROM scratch
COPY --from=build /isnipes /isnipes
EXPOSE 8080                     # public listener only; --admin-addr stays loopback
USER 65534:65534                # nobody; scratch has no /etc/passwd, numeric UID
ENTRYPOINT ["/isnipes"]
# Public listener on :8080; metrics/pprof admin listener bound to loopback
# (unreachable from outside the container unless explicitly published).
CMD ["--addr=:8080", "--admin-addr=127.0.0.1:6060"]
```

- `FROM scratch` + `CGO_ENABLED=0` static binary → image ≈ the binary
  size (target ~10 MB; DoD allows ≤ 15 MB to absorb toolchain drift).
- **Builder images pinned by digest** (`node:20-alpine@sha256:…`,
  `golang:1.26-alpine@sha256:…`) in the committed file for reproducible
  builds; tags are shown above only for readability.
- Only `:8080` is `EXPOSE`d. The admin listener (`/metrics`, pprof) is
  loopback and never published, so metrics/profiling are not reachable
  from outside the container by default.
- No shell/TLS-cert layer. TLS is terminated by nginx (§12.2) **or** the
  binary itself via `--require-tls --tls-cert --tls-key` (§ transport
  security). For HTTPS *from the container directly* an operator must add
  a CA bundle layer or use the distroless base (§19.6); documented in §13
  as a known `scratch` limitation.
- `.dockerignore` excludes `node_modules`, `web/dist`, `cmd/isnipes/dist`
  (built fresh in-stage), `.git`, test caches, `.codex`,
  `.phase-loop-notes.md`. `*_test.go` is **not** excluded (the go build
  ignores test files anyway).

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
`ExecStart=/usr/local/bin/isnipes --addr=127.0.0.1:8080
--admin-addr=127.0.0.1:6060` (both loopback; nginx fronts the public
one, the admin/metrics surface stays host-local). Documented `[Install]
WantedBy=multi-user.target`. DoD #15.

### 12.2 `deploy/nginx.conf`

`server {}` on 443 with TLS (`ssl_certificate` / `ssl_certificate_key`
placeholders), `location / { proxy_pass http://127.0.0.1:8080; }` with
the WebSocket upgrade dance (`proxy_http_version 1.1`, `Upgrade` /
`Connection` headers, `proxy_read_timeout` ≥ the 5 s idle ping window
with margin, e.g. 60 s). Since the public listener never serves them,
the admin paths are already unreachable through the proxy; as defence in
depth the example **also** `return 404`s `location = /metrics` and
`location /debug/pprof` so an operator who accidentally points
`location /` at the admin port still does not leak them. A `:80 → :443`
redirect server block. The metrics scrape is expected to hit the admin
listener (`127.0.0.1:6060`) directly, not through nginx. DoD #16.

---

## 13. Deploy README (`deploy/README.md`)

Documents: `make build` (single static binary), `docker build` /
`docker run -p 8080:8080`, the systemd install (copy binary to
`/usr/local/bin`, unit to `/etc/systemd/system`, `daemon-reload`,
`enable --now`), and the full flag list:

- `--addr` (public HTTP/WS listener, default `:8080`),
- `--admin-addr` (metrics + pprof listener, default `127.0.0.1:6060`,
  empty disables),
- `--enable-pprof` (mount pprof on the admin listener; off by default),
- `--require-tls` / `--tls-cert` / `--tls-key` (terminate TLS in the
  binary — the documented alternative to nginx, SPEC §12 OQ5),
- `--allowed-origins` (comma-separated extra WS origins) /
  `--insecure-origin` (dev-only: accept any `Origin`; **never** in prod),
- `--max-matches`, `--motd`, `--log-level`, `--web-dist`, `--version`.

It splits the endpoints by listener: **public** (`/`, `/healthz`,
`/version`, `/ws/lobby`, `/ws/match/{id}`) vs **admin** (`/metrics`
always; `/debug/pprof/*` when `--enable-pprof`). It covers **two TLS
paths** — nginx termination (§12.2) and direct `--require-tls` — and
the **WS origin policy** (same-origin by default; widen with
`--allowed-origins`; `--insecure-origin` is dev-only and a CSWSH risk in
prod). It notes the `scratch`-image no-CA-bundle caveat (use distroless
or add certs for direct outbound HTTPS) and how to read `/metrics` (key
series + the < 10 ms / < 5 ms tick budgets). DoD #17.

---

## 14. CI wiring (`.github/workflows/`)

- `ci.yml` (on push / PR), in order:
  1. **Frozen path-diff gate** — `git diff --name-only
     ${{base}}...HEAD` must not touch `internal/sim/**`,
     `internal/proto/**`, `web/src/{proto,sim,prediction,interp,
     netClient}.ts`, **or** `scripts/check-frozen.sh` /
     `scripts/frozen.sha256`. Touching any of those fails CI unless a
     `frozen-change-approved` label/commit-trailer is present. This is
     the *mechanical* backstop the sha-manifest alone cannot provide
     (the manifest can be re-seeded; a path-diff over the script +
     manifest themselves cannot be silently bypassed). The
     `make check-frozen` sha check still runs as a second line of
     defence.
  2. `go vet ./...`; `make test-race` (its prereq runs `make
     check-frozen`, DoD #2); `make test-testhooks`.
  3. `npm -C web ci` + `npm -C web run test:coverage`; `npm -C web run
     test:e2e` (Playwright, Chromium).
  4. **Embed build + embed-tag tests** — `make build` (populates
     `cmd/isnipes/dist`) then `go test -tags embed ./cmd/isnipes/...`
     (runs `TestE2E_EmbeddedClientServed`, DoD #13).
  5. `make test-load` (the 60 s smoke gate, its own step).
  - Go matrix may include linux/amd64 + linux/arm64 for the sim
    determinism cross-check (SPEC §12) — at minimum amd64.
- `nightly.yml` (scheduled `cron`): `make perf-nightly` (512-player +
  baseline-regression, §9); a `--soak` job; the strict cross-browser
  golden job deferred from P7 §20 may also live here.
- The deliverable is **valid, correctly-wired YAML** referencing the
  real `make` targets and scripts; whether a GitHub runner executes it
  is operator-side. DoD #20 verifies the files parse and name the
  targets + the frozen path-diff gate.

---

## 15. Concurrency rules

- The tick histogram / counter write side is single-goroutine **per
  match** (`Run`), but multiple match actors update the *same* shared
  `observ.Registry` concurrently → all counter/gauge/bucket mutations
  are `atomic`. Read side (`/metrics`) snapshots atomically; an
  in-flight increment may or may not be visible, which is fine for a
  scrape.
- Gauges are updated **by their owning goroutine**, never polled from
  outside: `active_matches` from the registry's locked
  `Create`/`RemoveEnded`; `joined_players` from each actor via
  `OnJoinedDelta` on admit/leave/drop. Nothing reads a live match's
  slot map externally (`Match.JoinedCount` is unsafe on a live match,
  `match.go:390`).
- `Registry.StopAll` posts `ctlShutdown` to each actor's inbox (the
  actor's own goroutine performs the end) and waits on each `done`
  channel + the registry drain; it does not touch slot state directly.
- The load harness owns its clients' goroutines and joins all of them,
  then `StopAll`-drains the matches, *then* reads `GoroutinesAfter`, so
  the leak check measures real residue, not in-flight teardown.
- The admin listener is a second `http.Server`; both servers are
  `Shutdown`- on-signal. pprof handlers are stdlib; mounting them adds
  no new concurrency.

---

## 16. Test plan

### 16.1 internal/observ
- `TestObserv_HistogramQuantileEstimate` (#7) — known samples →
  `Quantile(0.99)` lands in the bracketing bucket; `Count` exact;
  `Reset` clears.
- `TestObserv_RecordingSamplerExact` (#7) — `RecordingSampler.Quantile`
  returns the exact rank (so the §8 gate is exact, not interpolated).
- `TestObserv_HistogramConcurrentObserve` — N goroutines `Observe`;
  total `Count` == N (race-free under `-race`).
- `TestObserv_MetricsEndpoint` (#8) — `Handler` returns 200, correct
  content-type, parseable text exposition with the documented series,
  each with a non-trivial producer exercised.

### 16.2 internal/match
- `TestMatch_TickDurationRecorded` (#10) — a fake clock + fake sampler;
  after K live ticks the sampler saw K observations; over-budget hook
  fires when a tick exceeds `tickInterval`; nil hooks = no-op (existing
  tests unaffected).
- `TestMatch_OverBudgetSkipsNextSnapshot` (#10a) — inject a clock that
  makes one tick exceed `tickInterval`; assert exactly **one** scheduled
  snapshot beat is suppressed, `OnSnapshotDrop` fires once, cadence
  resumes, and a burst within one interval still skips only one beat.
- `TestRegistry_StopAll` (#3-support) — create N matches, `StopAll(ctx)`
  ends them cleanly (no `SERVER_ERROR`), `Len()` drains to 0, and no
  actor goroutine survives (checked under `-race` with a goroutine
  delta).

### 16.3 cmd/isnipes
- `TestMain_AdminListenerSeparation` (#9) — the **public** mux serves
  `/healthz`/`/ws/*` but returns 404 for `/metrics` and `/debug/pprof/`;
  the **admin** mux serves `/metrics` (200) always and `/debug/pprof/`
  only when `--enable-pprof` (200 vs 404).
- `TestNet_OriginPolicy` (#9b, in `internal/net`) — a cross-origin WS
  handshake is rejected by default; same-origin and `--allowed-origins`
  hosts are accepted; `--insecure-origin` accepts any (dev override).
- `TestMain_DirectTLS` (#9c) — with `--require-tls` + a self-signed
  cert/key from `testdata/`, the server answers HTTPS on `--addr` and a
  plain-HTTP request is refused; without `--require-tls` it serves HTTP.
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
  reference `test-race`, `test-load`, `perf-nightly` **and the frozen
  path-diff gate**; the Dockerfile has `FROM scratch`, `-tags embed`,
  pinned `@sha256:` builder digests, and clears `cmd/isnipes/dist`
  before copying; the nginx example denies `/metrics` + `/debug/pprof`.
  DoD #14 (presence/shape), #20.
- `TestFrozen_GuardCoversProto` — `scripts/check-frozen.sh` (and the
  re-seeded `frozen.sha256`) include every `internal/proto/*.go`
  (frame.go, messages.go, checksum.go, …) and `internal/sim/**`; a
  simulated edit to `internal/proto/messages.go` makes `make
  check-frozen` fail. DoD #2.

---

## 17. Risks

- **60 s smoke gate slows CI** → isolated in `make test-load` as its own
  step, `-short` gives a 5 s local pass, default `go test ./...` excludes
  it (build tag).
- **Tick-budget flakiness on shared CI runners** → the gate is P99 <
  10 ms (generous vs the ~sub-ms real cost at 16 players); nightly's
  < 5 ms is regression-only with a 20 % tolerance, never hard-fails a
  PR.
- **Gate must not be smeared by histogram estimation** → the pass/fail
  P99 is computed from the exact `RecordingSampler`; the bucketed
  `Histogram` is reserved for `/metrics` and is never the gate (§6.1).
- **Goroutine-leak false positives / false negatives** → §7.2 joins all
  clients **and** `StopAll`-drains all matches (waiting for `Len()==0`)
  before `runtime.GC()` + settle + the after-count, so DC-grace actors
  cannot linger and a tiny `allowedSlack` no longer masks real leaks.
- **A promised metric with no producer** → every `/metrics` series has a
  named producer (§6.2); the endpoint test exercises each. Production
  bytes come from `internal/net`, not the harness's client-side tally.
- **Bandwidth number is ambiguous** → DoD/§7.1 fix the unit to
  application WS-message bytes and call out the excluded framing/TLS
  overhead.
- **RSS probe portability** → Linux `/proc` primary, `MemStats.Sys`
  fallback with a documented caveat; the soak RSS check runs on the
  Linux deploy target.
- **`scratch` image can't do outbound TLS / has no certs** → documented;
  TLS terminates at nginx or via `--require-tls`; distroless or a
  CA-bundle layer is shown for the direct-HTTPS case (§19.6).
- **Embedding the wrong/stale client** → `make build` and the Docker
  build clear `cmd/isnipes/dist` before copying fresh output; the
  `-tags embed` default-asset test fails if the placeholder slips back
  in (DoD #13).
- **`/metrics` + pprof exposed to the internet** → both live on a
  loopback admin listener, not the public one; pprof additionally
  gated behind `--enable-pprof`; nginx denies both paths (§6.3, §12.2).
- **Cross-site WebSocket hijacking** → blanket `InsecureSkipVerify` is
  replaced with same-origin-by-default; widening requires explicit
  `--allowed-origins`; `--insecure-origin` is dev-only and documented as
  unsafe in prod.
- **Frozen invariant not mechanically enforced** → manifest widened to
  all `internal/proto/*.go` **and** a CI path-diff gate over the frozen
  paths + the guard script/manifest themselves (§14).
- **Adding a metrics dep bloats the minimal-dep posture** → default is
  dependency-free text exposition (§19.1).

---

## 18. Determinism / no-regression guarantees

- `go test -race ./...` green incl. new `observ` / `loadtest` /
  `cmd/isnipes` tests; P1/P3/P5 fingerprints byte-identical (no
  `internal/sim` edit; the tick timing wraps `m.tick()` and changes no
  sim state).
- `schemaChecksum` `0x42607394`; `scripts/check-frozen.sh` green over a
  **widened** manifest (now all `internal/proto/*.go` + `internal/sim/**`
  + the client mirrors); Phase 8 edits no frozen *source* file — only the
  guard script + its manifest, to broaden coverage (DoD #2).
- All P4–P7 vitest + Playwright suites pass unchanged; Phase 8 adds no
  `web/src` behaviour. The embed-tag default-asset test is additive.
- The match actor's existing tests pass with nil hooks (default), proving
  the instrumentation + over-budget rule are behaviour-neutral when the
  hooks are unset (the suppression flag still trims one snapshot beat,
  matching SPEC §11, but existing match tests assert on sim state and
  events, not snapshot cadence, so they are unaffected; a focused test
  covers the cadence change).

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
8. **Admin-listener default.** Default: `--admin-addr=127.0.0.1:6060`
   (on, loopback) so `/metrics` works out of the box but is host-local;
   empty disables it. Alt: default **off** (operator must opt in).
   Recommend loopback-on — metrics with no internet exposure is the
   safer-and-useful middle ground.
9. **Transport-security scope (direct TLS + origin policy).** SPEC §12
   OQ5 says v1 documents *both* nginx and direct `--require-tls`, and the
   current blanket `InsecureSkipVerify` is unsafe for a public deploy.
   Default: implement both in Phase 8 (small, bounded, deploy-phase
   appropriate, edits only `cmd/isnipes` + `internal/net`, neither
   frozen, no wire change). Alt: ship nginx-only TLS and merely
   *document* the origin risk, deferring the code to v1.1. Recommend
   doing it now — a perf/deploy phase that ships an internet-facing
   binary with no origin checks is incomplete. (If the operator wants to
   keep Phase 8 strictly non-behavioural, this is the one item to cut;
   flagged here so it's a conscious choice, not a silent gap.)
10. **Over-budget snapshot suppression is a live-match behaviour change.**
    It implements the unimplemented SPEC §11 rule and alters snapshot
    cadence under load — by design. It touches no `internal/sim` and no
    wire format, but it *does* change observable server output during an
    over-budget tick. Default: include it (SPEC mandates it; the perf
    phase is the right home). Alt: split it to its own change-set if the
    operator wants the load harness landed first. Recommend including it;
    the load harness is what would *surface* an over-budget condition, so
    the fix belongs with it.

---

## 20. Definition of done (canonical checklist)

| # | Item | Verified by |
|---:|---|---|
| 1 | `go test -race ./...` green incl. new observ/loadtest/match/net/cmd tests; P1/P3/P5 fingerprints byte-identical | CI |
| 2 | `schemaChecksum` `0x42607394`; **widened** `scripts/check-frozen.sh` covers all `internal/proto/*.go`; an edit to `messages.go` fails it; CI path-diff gate over frozen paths + guard/manifest | `TestSchemaChecksumValue` + `TestFrozen_GuardCoversProto` + `make check-frozen` |
| 3 | `make test-load` smoke (4×4=16 players, 60 s): no client errors, no match aborts | `TestLoad_Smoke` §16.4 |
| 4 | Smoke P99 server tick budget < 10 ms, computed from the **exact** `RecordingSampler` (not the histogram) | `TestLoad_Smoke` |
| 5 | Smoke: no goroutine leak after `StopAll`-drain + GC (small slack, not masking match actors) | `TestLoad_Smoke` + `TestRegistry_StopAll` |
| 6 | Smoke: mean per-client bandwidth ≤ 12 KB/s in **and** out, in **application WS-message bytes** (§7.1) | `TestLoad_Smoke` |
| 7 | `internal/observ` histogram (`Quantile`/`Count`/`Reset`, race-free `Observe`) **and** exact `RecordingSampler` | `TestObserv_HistogramQuantileEstimate` + `TestObserv_RecordingSamplerExact` §16.1 |
| 8 | `/metrics` valid Prometheus text exposition; every documented series has a real producer | `TestObserv_MetricsEndpoint` §16.1 |
| 9 | `/metrics` + pprof on the **admin** listener only; public mux 404s both; pprof gated by `--enable-pprof` | `TestMain_AdminListenerSeparation` §16.3 |
| 9b | WS origin policy: same-origin by default, `--allowed-origins` widens, `--insecure-origin` dev override (replaces blanket `InsecureSkipVerify`) | `TestNet_OriginPolicy` §16.3 |
| 9c | Direct TLS via `--require-tls`/`--tls-cert`/`--tls-key` serves HTTPS; documented in deploy guide | `TestMain_DirectTLS` §16.3 + §13 |
| 10 | Match actor records tick duration via nil-safe sampler + over-budget hook; no sim edit | `TestMatch_TickDurationRecorded` §16.2 |
| 10a | Over-budget tick suppresses exactly the next scheduled snapshot (SPEC §11) + bumps `snapshot_drops_total`; never accumulates | `TestMatch_OverBudgetSkipsNextSnapshot` §16.2 |
| 11 | `make build` runs `npm ci` + web build (no `npm install` fallback), clears dist, then `go build -tags embed`; binary embeds the real client | `make build` + #13 |
| 12 | Plain `go build ./cmd/isnipes` (no embed) builds with empty dist and serves the "build with -tags embed / --web-dist" notice | `TestMain_EmbedOffNotice` §16.3 |
| 13 | Embed build `GET /` serves the real bundle (references `app.js`, no "Phase 2 placeholder") | `TestE2E_EmbeddedClientServed` (`-tags embed`) §16.3 |
| 14 | `Dockerfile` multi-stage (node → go `-tags embed` → `FROM scratch`), digest-pinned builders, clears dist before copy, `EXPOSE 8080` only, admin loopback; `.dockerignore` present; build/run/size ≤ 15 MB | file shape (§16.5) + **deferred-to-operator** docker build |
| 15 | `deploy/isnipes.service` hardened systemd unit present (loopback `--addr` + `--admin-addr`) | §16.5 |
| 16 | `deploy/nginx.conf` TLS-termination + WS-upgrade proxy; denies `/metrics` + `/debug/pprof` | §16.5 |
| 17 | `deploy/README.md` documents build/docker/systemd/nginx, all flags, public-vs-admin endpoints, both TLS paths, origin policy | §16.5 |
| 18 | `make perf-nightly` runs the 512-player (64×8) load; baseline-regression (`testdata/perf_baseline.json`, 20 % tol) exits non-zero on regression | **deferred-to-operator** (`load_test --nightly`) §9 |
| 19 | Soak (`load_test --soak`) samples goroutines + RSS in-process; flags upward goroutine trend or RSS growth > 5 % | **deferred-to-operator** §9 |
| 20 | `.github/workflows/{ci,nightly}.yml` parse and wire the frozen path-diff gate + `test-race`/`test-load`/`perf-nightly`; Dockerfile names `FROM scratch` + `-tags embed` | `TestDeploy_FilesPresent` §16.5 |
| 21 | Per-file coverage ≥ 70 % for `internal/observ` and `internal/loadtest` | `go test -cover` + CI |

Items 1–13, 9b, 10a, 15–17, 20, 21 are gate-able inside the
implementation loop. Item 9c (direct TLS) is loop-testable with a
self-signed cert in `testdata`. Items 14 (Docker build/run/size — needs
a Docker daemon), 18 and 19 (512-player / 24 h soak — infra + runtime)
are **deferred-to-operator** and non-PR-blocking per SPEC §8 Phase 8 and
§9; their *artifacts* (Dockerfile, make targets, CLI flags, workflow
files) are produced and shape-checked in the loop.

The binary wire protocol is unchanged (`schemaChecksum = 0x42607394`).
`internal/sim/**`, `internal/proto/*.go` (the encoders), and
`web/src/{proto,sim,prediction,interp,netClient}.ts` are not edited; the
server changes are the additive, nil-safe tick instrumentation + the
SPEC §11 over-budget snapshot rule in `internal/match`, the origin-policy
+ byte-accounting in `internal/net`, and the admin-listener / pprof /
`/metrics` / direct-TLS / embed wiring in `cmd/isnipes`.
