# Phase 7 — Polish & feature completeness

This document is the buildable, testable expansion of §8 Phase 7 of
[`SPEC.md`](./SPEC.md). It assumes Phases 1–6 are on `main`:
`internal/sim` is the deterministic 30 Hz simulation; `internal/proto`
defines the binary wire schema (`schemaChecksum = 0x42607394`);
`internal/match` runs match actors (lives, dead-cam, scoring,
`Scoreboard`/`MatchOver`, DC-grace reconnect); `internal/net` carries
frames; `internal/lobby` is the full lobby (chat / kick / level
validation / GC / delta-push / deep-link); `cmd/isnipes` boots all of
it and serves the client; and `web/` ships a *headless* client core
(`proto.ts`, `sim.ts`, `prediction.ts`, `interp.ts`, `netClient.ts`,
`lobby.ts`, `main.ts`) plus a *minimal* browser shell (`browser.ts`)
with a Playwright harness.

Anywhere this document conflicts with `SPEC.md`, `SPEC.md` is canonical
and this document is wrong; please file an issue. Section references
like "§7.3" point into `SPEC.md`; "P4 §x", "P5 §x", "P6 §x" point into
the earlier phase docs.

---

## 1. Scope and definition of done

**Scope.** Phase 7 turns the headless, DOM-minimal client into a
playable, accessible game. It is **client-only**: no Go server file is
edited, the binary wire protocol is **byte-identical**, and
`schemaChecksum` stays `0x42607394`. Phase 7 builds the SPEC §7 render
pipeline, audio, HUD, settings, and the lobby level selector, then
locks the visuals with broad-tolerance golden screenshots.

Phase 7 ships:

- **Rendering** (`web/src/render.ts`) — Canvas2D camera-follow render
  of maze + entities + projectiles, self drawn from prediction and
  others from the §7.3 interpolation buffer. Static maze cached on an
  `OffscreenCanvas`; entities y-sorted for pseudo-depth.
- **Animations** — 8-direction 4-frame walk cycle, muzzle flash on
  fire, death poof on kill, spawn-invuln shimmer. Animation phase is a
  pure function of `(entityId, renderTick)` so it is deterministic
  under a frozen clock (golden-test requirement).
- **Audio** (`web/src/audio.ts`) — procedurally-synthesised WebAudio
  cues: shoot, hit, scream (player death), generator collapse, spawn,
  victory. Driven by `Event` (0x04) frames + `MatchOver` (0x08). A
  swappable sink lets vitest assert event→cue mapping without hardware.
- **HUD** (`web/src/hud.ts`) — HP / lives / score readout, minimap,
  hold-`Tab` scoreboard overlay (from the `Scoreboard` 0x0B frame),
  and the end-of-match dialog (from `MatchOver` 0x08).
- **Input** (`web/src/input.ts`) — keyboard handling with the Classic
  default preset (arrows move, WASD fire, Space turbo), rebindable,
  bindings persisted in `localStorage`.
- **Settings** (`web/src/settings.ts`) — rebinds, color-blind palette
  toggle, high-contrast toggle, master-volume slider, and a
  server-list (saved server URLs). Round-trips via `localStorage`;
  cleared on opt-out.
- **Level selector** — the lobby renders the (`A`..`Z`, `1`..`9`)
  picker and preset previews from the Phase 6 `level_presets` envelope.
- **Golden screenshots** (`web/tests/e2e/golden.spec.ts`) — Playwright
  pixel diffs for empty lobby, full lobby, in-match HUD, scoreboard,
  and end screen. Chromium ≤ 2 % is the PR gate; ≤ 0.5 % cross-browser
  is nightly (non-blocking).

**Definition of done.** All of the following pass on `main`:

1. `go test -race -count=1 ./...` is green and **unchanged** — Phase 7
   edits no Go file. The Phase 1 `TestDeterminism_GoldenFingerprint`
   baseline continues to pass byte-for-byte.
2. `schemaChecksum` is unchanged (`0x42607394`); `TestSchemaChecksumValue`
   and `web/tests/proto.test.ts` pass unmodified.
3. `npm -C web test` (vitest) is green: all P4–P6 unit suites still
   pass, plus the new render / animation / audio / hud / input /
   settings suites.
4. `npm -C web run test:e2e` is green: the Phase 6 `deeplink.spec.ts`
   still passes, plus `golden.spec.ts` (Chromium, ≤ 2 % per-pixel).
5. `TestRender_CameraFollowsLocalPlayer` — the local player's predicted
   entity is centered (± half a tile) in the viewport regardless of its
   world position.
6. `TestRender_MazeOffscreenCacheReusedAcrossFrames` — the static maze
   is rasterised once per `MapInit` and reused; a second frame with the
   same map does not re-rasterise tiles.
7. `TestRender_EntitiesSortedByY` — draw order is ascending world `y`.
8. `TestAnim_WalkCyclePhaseDeterministic` — `walkFrame(id, tick)` is a
   pure function: same inputs → same frame; advances 1 frame every
   `WALK_FRAME_TICKS`; idle entities show frame 0.
9. `TestAnim_MuzzleFlashOnFire` and `TestAnim_DeathPoofOnKill` — an
   `EntitySpawn{projectile}` event arms a muzzle-flash overlay on the
   firing actor; an `EntityKill` event arms a death-poof overlay on the
   target; both decay after their fixed durations.
10. `TestAudio_EventCueMapping` — each `EventKind` / `MatchOver` reason
    maps to the correct cue via the fake sink (shoot/hit/scream/
    generator/spawn/victory); unmapped kinds are silent.
11. `TestAudio_MasterVolumeGatesOutput` — master volume 0 emits no cue;
    > 0 scales gain; the setting is read live from `settings.ts`.
12. `TestHUD_ScoreboardDecodeAndOrder` — a `Scoreboard` (0x0B) frame
    decodes to per-player {id, nick, lives, score} and the overlay
    orders rows by descending score (ties by ascending id).
13. `TestHUD_TabHoldTogglesScoreboard` — `keydown Tab` shows the
    overlay; `keyup Tab` hides it; `Tab` is suppressed from browser
    focus traversal while in-match.
14. `TestHUD_EndDialogFromMatchOver` — a `MatchOver` (0x08) frame
    renders the end dialog with the §3.8.1 reason text and the winner
    (or "no single winner" for `ALL_ELIMINATED`/`SERVER_ERROR`/ties).
15. `TestHUD_MinimapPlotsEntities` — the minimap plots the local player
    and visible entities at map-scaled coordinates within the minimap
    rect.
16. `TestInput_ClassicDefaultPreset` — with no stored bindings, arrows
    map to move-dirs, WASD to fire-dirs, Space to turbo; diagonal key
    combos produce the 8-dir intent.
17. `TestInput_RebindRejectsConflict` — binding one physical key to both
    a fire-N and a move-N action is rejected (§ SPEC line 363).
18. `TestSettings_RoundTripLocalStorage` — rebinds + color-blind +
    high-contrast + master-volume + server-list serialize to and
    restore from `localStorage`; "reset to defaults" clears the keys.
19. `TestLobby_LevelSelectorRendersPresets` (vitest) — given a
    `level_presets` envelope, the picker renders 26 × 9 cells and the
    selected cell's preview shows difficulty / lives / generators.
20. `golden.spec.ts` produces ≤ 2 % per-pixel diff on Chromium for all
    five reference scenes (empty lobby, full lobby, in-match HUD,
    scoreboard, end screen) against committed baselines.
21. Coverage ≥ 70 % statements for the new `web/src` modules
    (`render.ts`, `audio.ts`, `hud.ts`, `input.ts`, `settings.ts`),
    measured by `vitest --coverage`.

Items 1–21 are gate-able in CI. The nightly cross-browser strict-diff
job (≤ 0.5 % Chromium + Firefox + WebKit) is **deferred-to-operator**
(not PR-blocking) per SPEC §8 Phase 7 and §9.

---

## 2. Out of scope

Explicitly **not** built in Phase 7:

- **Any server / wire change.** No Go edits; `schemaChecksum` frozen.
  In-match binary chat (the 0x05 `Chat` frame) rendering is the one
  exception that *would* be a render concern, but P6 §6.4 already
  deferred it; Phase 7 renders **lobby** chat only and leaves the
  in-match chat overlay as a documented v1.1 candidate (§19).
- **Sprite-sheet PNG art.** SPEC §7.4 sketches PNG sprite sheets;
  Phase 7 instead draws entities with deterministic Canvas2D vector
  primitives (see §6.4 and open question §19.1). No binary art assets
  are committed.
- **jsfxr binary sound files.** SPEC §7.4 sketches `jsfxr` `.wav`s;
  Phase 7 synthesises cues at runtime via WebAudio (see §7 and §19.2).
  No binary audio assets are committed.
- **Load / perf / Docker / deploy** — Phase 8.
- **Matchmaking ranking, accounts, persistence** — non-goals per
  SPEC §1.2.
- **Mobile / touch controls** — keyboard only in v1.
- **Replay viewer UI** — the `scripts/replay.go` CLI is Phase 8 / tooling.

---

## 3. Prerequisites and assumptions

- Phases 1–6 are on `main`. The headless client core
  (`prediction.ts`, `interp.ts`, `netClient.ts`, `lobby.ts`,
  `main.ts`) is unit-tested and unchanged in behaviour; Phase 7 adds
  presentation on top and **must not** alter prediction/interp math.
- `interp_ticks = 2`; non-self entities render ~100 ms behind real
  time (SPEC §6 / lines 201, 887). Self is drawn from the prediction
  buffer with no interpolation lag.
- The build is **esbuild**, not Vite (the project deviated from SPEC
  §7.1 in Phase 6; Phase 7 keeps esbuild for consistency — see §19.3).
  `browser.ts` is the single bundled entry → `dist/app.js`.
- Wire facts Phase 7 consumes (all already defined, none changed):
  - Entity kinds: `Player=1, Generator=2, Projectile=3, Snipe=4`
    (`internal/sim/config.go`).
  - Entity flags: `Dead=0x01, SpawnInvuln=0x02, Turbo=0x04` (`proto.ts`).
  - `EventKind` (`proto.ts`): `EntitySpawn=0x01, EntityHit=0x02,
    EntityKill=0x03, GeneratorDestroyed=0x04, PlayerJoin=0x05,
    PlayerLeave=0x06, PlayerDC=0x07, PlayerRejoin=0x08,
    MatchStarting=0x09, MatchStarted=0x0a, MatchEnd=0x0b,
    ChatRelay=0x0c, RespawnPending=0x0d`.
  - `Scoreboard` (0x0B): `u32 server_tick, u8 entry_count,
    [u32 player_id, u8 nick_len, utf8 nick, u8 lives, i32 score]×n`.
  - `MatchOver` (0x08): `u32 final_tick, u8 reason, u32 winner_id_or_0,
    u8 entry_count, [u32 player_id, i32 score, u8 lives_remaining]×n`;
    reason enum `0=PVE_COMPLETE, 1=LAST_STANDING, 2=ALL_ELIMINATED,
    3=TIMER, 4=SERVER_ERROR`.
  - `MapInit` (0x09) carries the packed maze tiles already decoded by
    `decodeMapInit` in `proto.ts`.
- Node ≥ 20; system Google Chrome reachable for Playwright
  (`channel: "chrome"`, already configured in `playwright.config.ts`).
- No new npm runtime deps. Dev-only: none beyond the existing
  `@playwright/test`, `esbuild`, `vitest`, `typescript`. Coverage uses
  vitest's built-in `--coverage` (add `@vitest/coverage-v8` devDep).

---

## 4. Package and file layout

New files in Phase 7:

```
web/src/
├── render.ts        # NEW — Canvas2D render pipeline (§6)
├── anim.ts          # NEW — pure animation-phase functions (§6.3)
├── audio.ts         # NEW — WebAudio cue synth + event router (§7)
├── hud.ts           # NEW — HP/lives/score, minimap, scoreboard, end dialog (§8)
├── input.ts         # NEW — keyboard → ClientInputIntent + rebinds (§9)
├── settings.ts      # NEW — localStorage-backed settings store (§10)
├── palette.ts       # NEW — default + color-blind + high-contrast palettes (§10.2)

web/tests/
├── render.test.ts   # NEW — camera, maze cache, y-sort, viewport math
├── anim.test.ts     # NEW — walk/muzzle/poof phase determinism
├── audio.test.ts    # NEW — event→cue mapping, volume gating (fake sink)
├── hud.test.ts      # NEW — scoreboard decode/order, tab toggle, end dialog, minimap
├── input.test.ts    # NEW — classic preset, diagonals, rebind conflict
├── settings.test.ts # NEW — localStorage round-trip + reset
├── fixtures/
│   ├── scenes.ts    # NEW — canned MapInit/Snapshot/Scoreboard/MatchOver byte fixtures
│   └── README.md    # NEW — how the byte fixtures were generated
└── e2e/
    └── golden.spec.ts   # NEW — 5 reference-scene screenshot diffs (§12)

web/tests/e2e/golden.spec.ts-snapshots/   # NEW — committed baseline PNGs
```

Existing files modified by Phase 7:

```
web/src/
├── browser.ts   # wires render loop + audio + hud + input + settings into the
│                # match section; renders the level selector in the lobby;
│                # adds a settings panel + nick-edit field. Match-view DOM gains
│                # the golden-scene test hooks (§12.2).
├── main.ts      # exposes the match data stream (MapInit/Snapshot/Scoreboard/
│                # Event/MatchOver decoded frames) to the presentation layer via
│                # callbacks; no change to prediction/interp/lobby logic.
├── index.html   # adds the canvas sizing + settings/hud mount containers.

web/
├── package.json     # add `test:coverage`; add @vitest/coverage-v8 devDep.
├── playwright.config.ts  # pin deviceScaleFactor=1, fixed viewport; add the
│                         # golden project + ?scene= screenshot route wait.
├── vitest.config.ts # add coverage config (provider v8, include src/**).
```

`internal/**`, `cmd/**`, `web/src/proto.ts`, `web/src/sim.ts`,
`web/src/prediction.ts`, `web/src/interp.ts`, and
`web/src/netClient.ts` are **NOT** edited (netClient already exposes the
decoded-frame callback the presentation layer needs).

---

## 5. Public API additions (client)

```ts
// render.ts
export interface Camera { x: number; y: number; w: number; h: number; }
export interface RenderState {
  map: MapInit | null;
  selfId: number;
  selfPredicted: { x: number; y: number; facing: number; flags: number } | null;
  entities: Entity[];           // interpolated non-self entities (subtile coords)
  renderTick: number;           // monotonically increasing; drives animation phase
}
export class Renderer {
  constructor(canvas: HTMLCanvasElement, palette: Palette);
  setMap(m: MapInit): void;     // (re)builds the OffscreenCanvas maze cache
  draw(s: RenderState, hud: HudModel): void;  // one frame; pure wrt inputs
  worldToScreen(cam: Camera, x: number, y: number): [number, number];
}

// anim.ts — all pure, no clock, no DOM.
export const WALK_FRAME_TICKS = 4;       // render ticks per walk frame
export const MUZZLE_FLASH_TICKS = 6;
export const DEATH_POOF_TICKS = 18;
export function walkFrame(entityId: number, renderTick: number, moving: boolean): 0|1|2|3;
export function dir8FromFacing(facing: number): 0|1|2|3|4|5|6|7;

// audio.ts
export interface AudioSink { play(cue: Cue, gain: number): void; }
export type Cue = "shoot"|"hit"|"scream"|"generator"|"spawn"|"victory";
export class AudioEngine {
  constructor(sink: AudioSink, getMasterVolume: () => number);
  onEvent(kind: number, payload: Uint8Array): void;   // EventKind router
  onMatchOver(reason: number): void;                   // → "victory" or silence
}

// hud.ts
export interface ScoreRow { id: number; nick: string; lives: number; score: number; }
export interface HudModel {
  hp: number; lives: number; score: number;
  rows: ScoreRow[];          // ordered desc score, asc id
  showScoreboard: boolean;
  endDialog: { reason: number; winnerId: number; rows: ScoreRow[] } | null;
}
export function decodeScoreboard(payload: Uint8Array): ScoreRow[];
export function reasonText(reason: number): string;    // §3.8.1 enum → prose

// input.ts
export type Action = "moveN"|"moveE"|"moveS"|"moveW"|"fireN"|"fireE"|"fireS"|"fireW"|"turbo";
export const CLASSIC_PRESET: Record<string, Action>;   // KeyboardEvent.code → Action
export class InputController {
  constructor(bindings: Record<string, Action>);
  intent(): ClientInputIntent;        // resolves held keys → {dir, turbo}
  setBindings(b: Record<string, Action>): { ok: true } | { ok: false; conflict: string };
}

// settings.ts
export interface Settings {
  bindings: Record<string, Action>;
  colorBlind: boolean;
  highContrast: boolean;
  masterVolume: number;     // 0..1
  servers: string[];        // saved server URLs
}
export function loadSettings(): Settings;          // defaults if absent/corrupt
export function saveSettings(s: Settings): void;
export function resetSettings(): void;             // clears the localStorage key
```

---

## 6. Rendering (§7.3)

### 6.1 Pipeline (per `requestAnimationFrame`)

Mirrors SPEC §7.3 exactly:

1. Compute the interpolated world at `t = now − 100 ms` (lerp the two
   most recent snapshots; this is the existing `interp.ts` output).
2. Override the local player (`selfId`) with `selfPredicted` from the
   prediction buffer — no interpolation lag for self.
3. Clear the canvas; blit the visible portion of the cached maze.
4. Draw entities sorted ascending by world `y` (pseudo-depth).
5. Draw projectiles with a short 2-segment trail for legibility.
6. Draw the HUD (§8).
7. `requestAnimationFrame` the next frame.

### 6.2 Camera

The camera centers the local player. `worldToScreen` maps subtile
world coords to canvas pixels at `TILE_PX = 32` and
`PX_PER_SUBTILE = TILE_PX / SUBTILE_PER_TILE`. When the local player
is near a map edge the camera clamps so the viewport never shows
outside the maze (letterboxed by floor color). The DoD #5 test pins
"self is centered ± half a tile" away from edges.

### 6.3 Animation phase (deterministic)

Animation is a **pure function of `(entityId, renderTick)`** — never of
wall-clock time — so a frozen `renderTick` yields identical pixels
(golden-test requirement, §12):

- **Walk cycle:** `walkFrame(id, tick, moving)` returns the 4-frame
  index; cycles every `WALK_FRAME_TICKS`; `moving=false` → frame 0.
  Direction comes from `dir8FromFacing(entity.facing)`.
- **Muzzle flash:** an `EntitySpawn` event whose spawned entity is a
  projectile arms a flash overlay on the firing actor for
  `MUZZLE_FLASH_TICKS`, drawn along the facing axis.
- **Death poof:** an `EntityKill` event arms an expanding-ring poof on
  the target for `DEATH_POOF_TICKS`.
- **Spawn-invuln shimmer:** entities with `FlagBits.SpawnInvuln` blink
  at a fixed `renderTick` cadence.

Overlays live in a small `Map<entityId, {kind, armedAtTick}>` and are
purged when `renderTick − armedAtTick` exceeds the overlay duration.

### 6.4 Sprites (vector primitives — deviation from §7.4)

Entities are drawn with Canvas2D vector primitives, not PNG sheets:

- Player: filled circle body + facing wedge; team/own color from the
  palette; the walk frame nudges leg-stub offsets.
- Snipe: diamond body + facing wedge in the "hostile" palette slot.
- Generator: square with 3 damage states keyed off `hp` ratio.
- Projectile: small filled dot + trail.

Rationale: no committed art exists, vector primitives are fully
deterministic across machines (critical for ≤ 2 % golden diffs), and at
≤ ~80 visible entities the draw cost is negligible. Swapping in PNG
sheets later is a localised change behind the `Renderer` surface.
Flagged as open question §19.1.

### 6.5 Maze cache

On `setMap`, the full maze is rasterised once to an `OffscreenCanvas`
(fallback: a detached `<canvas>` when `OffscreenCanvas` is absent).
Frames blit the visible sub-rect; tiles are never re-rasterised unless
`setMap` is called again. DoD #6 pins this.

---

## 7. Audio (§7.4)

### 7.1 Cue router

`AudioEngine.onEvent` maps decoded `Event` (0x04) frames to cues:

| Event | Cue |
|---|---|
| `EntitySpawn` (spawned kind = projectile) | `shoot` |
| `EntityHit` | `hit` |
| `EntityKill` (target kind = player) | `scream` |
| `EntityKill` (target kind = snipe) | `hit` |
| `GeneratorDestroyed` | `generator` |
| `PlayerJoin` / `PlayerRejoin` | `spawn` |
| all other kinds | silent |

`onMatchOver(reason)` plays `victory` for `PVE_COMPLETE` /
`LAST_STANDING` / `TIMER` (a result), and is silent for
`ALL_ELIMINATED` / `SERVER_ERROR`.

### 7.2 Synthesis + sink

The production `AudioSink` lazily creates one `AudioContext` (on first
user gesture, per browser autoplay policy) and synthesises each cue
from oscillator + noise bursts with a fixed envelope — no binary
assets (deviation from §7.4, open question §19.2). Gain is
`getMasterVolume()` clamped to `[0, 1]`; volume `0` short-circuits
before any node is created. Tests inject a fake sink that records
`(cue, gain)` calls so DoD #10/#11 assert mapping and volume gating
without audio hardware.

---

## 8. HUD (§7.3 step 6)

### 8.1 Readout

Top-left: HP (number, never color-only per §7.5), lives (icons +
number), score. Driven by the local player's `Scoreboard` row and the
self entity's `hp`.

### 8.2 Scoreboard overlay

`decodeScoreboard` parses the 0x0B frame; rows are ordered descending
by score, ties ascending by id. The overlay is shown while `Tab` is
held (`keydown` → show, `keyup` → hide) and is suppressed from the
browser's focus traversal (`preventDefault` on `Tab` while in-match).
DoD #12/#13.

### 8.3 Minimap

A fixed-size rect (bottom-right) scales world coords to minimap pixels;
plots the local player (distinct marker) and visible entities. Walls
are drawn from a downscaled maze cache. DoD #15.

### 8.4 End-of-match dialog

On `MatchOver` (0x08), the dialog shows `reasonText(reason)`, the
winner nick (or "No single winner" for `ALL_ELIMINATED` /
`SERVER_ERROR` / ties where `winner_id == 0`), and the final
scoreboard from the frame's entry list. A "Back to lobby" button
returns to the `POST_MATCH → LOBBY` state (existing `main.ts`
transition). DoD #14.

---

## 9. Input & rebinds (§7.5, SPEC line 363)

### 9.1 Classic preset

Default `CLASSIC_PRESET` (keyed on `KeyboardEvent.code`): arrows →
move-N/E/S/W, `KeyW/KeyD/KeyS/KeyA` → fire-N/E/S/W, `Space` → turbo.
Held keys resolve to a `Dir8` intent: two orthogonal move keys produce
the diagonal (e.g. ArrowUp+ArrowRight → `NE`); opposing keys cancel.
Fire keys resolve `fireDir` independently. DoD #16.

### 9.2 Rebinding rules

`setBindings` rejects a binding map where one physical key maps to both
a fire-N and a move-N action (SPEC line 363) — returns
`{ok:false, conflict}` and the caller keeps the prior map. Accepted
binds persist via `settings.ts`. DoD #17.

---

## 10. Settings (§7.5)

### 10.1 Store

`settings.ts` reads/writes a single `localStorage` key
(`isnipes.settings`, JSON). `loadSettings` returns typed defaults when
the key is absent or corrupt (try/catch around `JSON.parse`).
`resetSettings` removes the key (opt-out → defaults). DoD #18.

### 10.2 Palettes & accessibility

`palette.ts` exposes `default`, `colorBlind` (no info by red/green
alone), and a `highContrast` modifier (thicker entity outlines) per
§7.5. The `Renderer` takes a `Palette`; toggling rebuilds the maze
cache (colors changed). HP is always shown as a number so no critical
info is color-only.

### 10.3 Server-list

A saved list of server URLs (deduped, capped at 8). The lobby connect
flow offers the list; the current server is remembered. Validation:
`ws://` / `wss://` (or bare host, scheme inferred from page) only.

### 10.4 Level selector

The lobby renders the 26 × 9 picker from the Phase 6 `level_presets`
envelope (already delivered once after `welcome`). Selecting a cell
shows its preview (difficulty bucket, player lives, generators, max
snipes, description) and sets `createRoom.level`. DoD #19.

---

## 11. Determinism rules

Phase 7 touches **no** `internal/sim` code; the Phase 1/3/5
fingerprint goldens are unchanged (DoD #1). The render layer is *visual*
determinism, a separate concern from sim determinism:

- Animation phase is a pure function of `(entityId, renderTick)` — no
  `Date.now()`, no `Math.random()` in the draw path.
- Golden scenes render exactly one frame at a fixed `renderTick` from
  canned byte fixtures with `deviceScaleFactor = 1` and a fixed
  viewport, so pixels are reproducible across runs on the same engine.
- HUD text uses a procedurally-drawn bitmap glyph set (not system
  fonts) to remove font-rendering variance from golden diffs.

---

## 12. Golden screenshot harness

### 12.1 Scenes

Five reference scenes, each backed by a byte fixture in
`web/tests/fixtures/scenes.ts`:

1. **empty-lobby** — connected, zero rooms.
2. **full-lobby** — N rooms incl. one "my room"; level picker open.
3. **in-match-hud** — `MapInit` + one `Snapshot` (self + snipe +
   generator + projectile) + `Scoreboard`; HUD visible.
4. **scoreboard** — same as (3) with the `Tab` overlay shown.
5. **end-screen** — `MatchOver{LAST_STANDING}` dialog with a 2-player
   final scoreboard.

### 12.2 Screenshot route

`browser.ts` accepts `?scene=<name>` (test-only): it bypasses the live
WS, feeds the canned fixture frames through the *real* render/hud path,
renders one deterministic frame at a fixed `renderTick`, and sets
`data-testid="scene-ready"` when painting completes. `golden.spec.ts`
navigates to each `?scene=`, waits for `scene-ready`, and asserts
`expect(page).toHaveScreenshot(name.png, { maxDiffPixelRatio: 0.02 })`
on Chromium. DoD #20.

### 12.3 Baselines & CI split

Baselines are committed under
`web/tests/e2e/golden.spec.ts-snapshots/` (Chromium/linux). The PR gate
is Chromium ≤ 2 %. The nightly cross-browser ≤ 0.5 % job
(Chromium + Firefox + WebKit) is **deferred-to-operator** — it needs
Firefox/WebKit binaries the PR runner may lack, and is non-blocking per
SPEC §8/§9.

---

## 13. Wire protocol notes

**No wire change.** `schemaChecksum = 0x42607394` unchanged;
`internal/proto/checksum.go` and `web/src/proto.ts` are NOT edited
(DoD #2). Phase 7 only *consumes* frames already defined: `MapInit`,
`Snapshot`, `Event`, `Scoreboard`, `MatchOver`, plus the lobby
`level_presets` JSON envelope from Phase 6.

---

## 14. Concurrency rules

Client-only, single-threaded JS event loop. The render loop
(`requestAnimationFrame`), the WS message callback, and keyboard
listeners all run on the main thread; no workers in v1. The
`OffscreenCanvas` is used for caching only (no worker transfer). No new
server concurrency surface.

---

## 15. Test plan

### 15.1 render.test.ts
- `TestRender_CameraFollowsLocalPlayer` — DoD #5.
- `TestRender_MazeOffscreenCacheReusedAcrossFrames` — DoD #6 (spy on
  the tile rasteriser; assert called once across two frames).
- `TestRender_EntitiesSortedByY` — DoD #7.
- `TestRender_WorldToScreenRoundTrip` — pixel math is exact at
  `TILE_PX`/`PX_PER_SUBTILE`.
- `TestRender_CameraClampsAtMapEdge` — viewport never exits the maze.

### 15.2 anim.test.ts
- `TestAnim_WalkCyclePhaseDeterministic` — DoD #8.
- `TestAnim_MuzzleFlashOnFire` — DoD #9.
- `TestAnim_DeathPoofOnKill` — DoD #9.
- `TestAnim_OverlaysDecayAndPurge` — overlays removed after duration.

### 15.3 audio.test.ts (fake sink)
- `TestAudio_EventCueMapping` — DoD #10 (table-driven over EventKind).
- `TestAudio_MasterVolumeGatesOutput` — DoD #11.
- `TestAudio_MatchOverVictoryVsSilent` — reason → victory/silence.

### 15.4 hud.test.ts
- `TestHUD_ScoreboardDecodeAndOrder` — DoD #12.
- `TestHUD_TabHoldTogglesScoreboard` — DoD #13.
- `TestHUD_EndDialogFromMatchOver` — DoD #14.
- `TestHUD_MinimapPlotsEntities` — DoD #15.
- `TestHUD_ReasonTextEnum` — all 5 §3.8.1 reasons → prose.

### 15.5 input.test.ts
- `TestInput_ClassicDefaultPreset` — DoD #16.
- `TestInput_DiagonalCombos` — orthogonal pairs → 8-dir; opposing
  cancel.
- `TestInput_RebindRejectsConflict` — DoD #17.

### 15.6 settings.test.ts
- `TestSettings_RoundTripLocalStorage` — DoD #18.
- `TestSettings_DefaultsOnCorrupt` — bad JSON → defaults, no throw.
- `TestSettings_ResetClearsKey`.

### 15.7 lobby selector (extend lobby.test.ts)
- `TestLobby_LevelSelectorRendersPresets` — DoD #19.

### 15.8 e2e/golden.spec.ts
- `TestGolden_FiveScenesChromium` — DoD #20 (≤ 2 % each).

---

## 16. Testdata / fixtures

No new Go testdata. New TS fixtures in `web/tests/fixtures/scenes.ts`
are byte arrays built with the existing `proto.ts` encoders (or
hand-encoded and asserted against decoders), documented in
`fixtures/README.md` so a future maintainer can regenerate them. The
committed golden PNGs live beside `golden.spec.ts`.

---

## 17. Risks

- **Golden-diff flakiness** is the central risk. Mitigations: pin
  `deviceScaleFactor=1` + fixed viewport; render a single deterministic
  frame from canned fixtures (no live WS timing); vector primitives +
  bitmap font (no system-font variance); ≤ 2 % tolerance on Chromium
  only for the PR gate; cross-browser strict diffs are nightly.
- **Baseline drift on intentional visual changes** — updating a scene
  requires regenerating the committed PNG. `fixtures/README.md`
  documents `playwright test --update-snapshots`.
- **WebAudio autoplay policy** — `AudioContext` must be created after a
  user gesture; the engine lazily inits on first input and no-ops
  before then (and entirely under the fake sink in tests).
- **`OffscreenCanvas` support** — fallback to a detached `<canvas>`.
- **`Tab` key hijacking** — suppressing `Tab` focus traversal in-match
  must not leak into the lobby (accessibility). Scoped to the
  `IN_MATCH` state only.
- **Color-only information** — §7.5 forbids it; HP-as-number and the
  color-blind palette are the mitigations, asserted indirectly via the
  color-blind golden scene rendering (palette swap changes pixels).

---

## 18. Determinism / no-regression guarantees

- `go test -race ./...` byte-identical to pre-Phase-7 (no Go edits).
- `schemaChecksum` `0x42607394` (DoD #2).
- All P4–P6 vitest suites pass unchanged; Phase 7 adds suites, edits
  none of the prediction/interp/proto tests.
- The Phase 6 `deeplink.spec.ts` continues to pass (the match-view DOM
  hooks it relies on — `match-view`, `data-match-connected` — are
  preserved; Phase 7 only adds rendering inside that wrapper).

---

## 19. Open questions

Flagged for codex review / author decision before implementation.
Defaults listed.

1. **Vector primitives vs PNG sprite sheets (§7.4).** Default: vector
   primitives — deterministic across machines, no committed binary art,
   sufficient at this scale. Alternative: commit PNG sheets + a sprite
   loader (heavier, font/asset variance complicates golden diffs).
2. **Procedural WebAudio vs jsfxr `.wav` files (§7.4).** Default:
   procedural synthesis — testable, no binary assets. Alternative:
   commit jsfxr-generated `.wav`s (binary assets, but matches §7.4
   verbatim).
3. **esbuild vs Vite (§7.1).** Default: keep esbuild (Phase 6's
   choice). SPEC says Vite; the deviation is already in `main` and Vite
   buys nothing for a single-entry bundle. Documented, not reverted.
4. **Golden baseline OS/engine.** Default: Chromium-on-linux baselines
   committed; the PR runner must match. If contributors are on macOS,
   a 2 % tolerance usually absorbs sub-pixel AA differences, but a CI
   "render on a pinned container" approach is the robust fix (Phase 8
   CI hardening).
5. **In-match chat overlay.** Default: not rendered in Phase 7 (P6
   §6.4 deferred wiring the 0x05 frame). Lobby chat only. v1.1
   candidate.
6. **Minimap scope (full maze vs AOI).** Default: plot the full maze
   outline + only AOI-visible entities (the client only *has* AOI
   entities). Fog-of-war beyond AOI is implicit.
7. **Nick editing location.** Default: a nick field in the settings
   panel (Phase 6 auto-assigns `Player-xxxxx`; Phase 7 lets the user
   edit + persist it). Alternative: the §9.1 pre-connect prompt.

---

## 20. Definition of done (canonical checklist)

| # | Item | Verified by |
|---:|---|---|
| 1 | `go test -race -count=1 ./...` green & unchanged; P1/P3/P5 fingerprint goldens byte-identical | CI |
| 2 | `schemaChecksum` unchanged (`0x42607394`); `proto.test.ts` unmodified | `TestSchemaChecksumValue` |
| 3 | `npm -C web test` (vitest) green incl. new suites | CI |
| 4 | `npm -C web run test:e2e` green (deeplink + golden, Chromium) | CI |
| 5 | `TestRender_CameraFollowsLocalPlayer` | §15.1 |
| 6 | `TestRender_MazeOffscreenCacheReusedAcrossFrames` | §15.1 |
| 7 | `TestRender_EntitiesSortedByY` | §15.1 |
| 8 | `TestAnim_WalkCyclePhaseDeterministic` | §15.2 |
| 9 | `TestAnim_MuzzleFlashOnFire` + `TestAnim_DeathPoofOnKill` | §15.2 |
| 10 | `TestAudio_EventCueMapping` | §15.3 |
| 11 | `TestAudio_MasterVolumeGatesOutput` | §15.3 |
| 12 | `TestHUD_ScoreboardDecodeAndOrder` | §15.4 |
| 13 | `TestHUD_TabHoldTogglesScoreboard` | §15.4 |
| 14 | `TestHUD_EndDialogFromMatchOver` | §15.4 |
| 15 | `TestHUD_MinimapPlotsEntities` | §15.4 |
| 16 | `TestInput_ClassicDefaultPreset` | §15.5 |
| 17 | `TestInput_RebindRejectsConflict` | §15.5 |
| 18 | `TestSettings_RoundTripLocalStorage` | §15.6 |
| 19 | `TestLobby_LevelSelectorRendersPresets` | §15.7 |
| 20 | `golden.spec.ts` ≤ 2 % per-pixel on Chromium for all 5 scenes | §15.8 |
| 21 | Coverage ≥ 70 % statements for new `web/src` modules | `vitest --coverage` |

Items 1–21 are gate-able in CI. The nightly cross-browser strict-diff
job (≤ 0.5 % across Chromium/Firefox/WebKit) is deferred-to-operator
and non-PR-blocking per SPEC §8 Phase 7 and §9.

The binary wire protocol is unchanged from Phase 6. No `internal/**` or
`cmd/**` Go file is edited. `web/src/proto.ts`, `sim.ts`,
`prediction.ts`, `interp.ts`, and `netClient.ts` are unchanged.
