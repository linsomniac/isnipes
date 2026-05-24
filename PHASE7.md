# Phase 7 — Polish & feature completeness

This document is the buildable, testable expansion of §8 Phase 7 of
[`SPEC.md`](./SPEC.md). It assumes Phases 1–6 are on `main`:
`internal/sim` is the deterministic 30 Hz simulation; `internal/proto`
defines the binary wire schema (`schemaChecksum = 0x42607394`);
`internal/match` runs match actors (lives, dead-cam, scoring,
`Scoreboard`/`MatchOver`, DC-grace reconnect); `internal/net` carries
frames; `internal/lobby` is the full lobby (chat / kick / level
validation / GC / delta-push / deep-link / `level_presets`);
`cmd/isnipes` boots all of it and serves the client; and `web/` ships a
*headless* client core (`proto.ts`, `sim.ts`, `prediction.ts`,
`interp.ts`, `netClient.ts`, `lobby.ts`, `main.ts`) plus a *minimal*
browser shell (`browser.ts`) with a Playwright harness.

Anywhere this document conflicts with `SPEC.md`, `SPEC.md` is canonical
and this document is wrong; please file an issue. Section references
like "§7.3" point into `SPEC.md`; "P4 §x", "P5 §x", "P6 §x" point into
the earlier phase docs.

---

## 1. Scope and definition of done

**Scope.** Phase 7 turns the headless, DOM-minimal client into a
playable, accessible game and wires the one remaining in-match feature
that SPEC §3.9 / §3.10.1 require but earlier phases deferred (in-match
chat). It is **client-heavy** with a single **bounded, additive
server change** (the Chat-frame relay, §7). The hard invariants are:

- **`internal/sim` is not edited.** The Phase 1/3/5 determinism
  fingerprints stay byte-identical.
- **`schemaChecksum` stays `0x42607394`.** `internal/proto/checksum.go`
  and `web/src/proto.ts` are not edited. The Chat (`0x05`) and
  `Event/ChatRelay` (`0x0C`) frame types the chat relay uses are
  **already** in the schema, so wiring them changes no checksum.

Phase 7 ships:

- **Rendering** (`web/src/render.ts`) — Canvas2D camera-follow render
  of maze + entities + projectiles, self drawn from prediction and
  others from the §7.3 interpolation buffer. Static maze cached on an
  `OffscreenCanvas`; entities y-sorted for pseudo-depth.
- **Maze unpacking** (`web/src/maze.ts`) — decodes the `packing = 1`
  (2-bits-per-tile, little-endian) `packed_tiles` from `MapInit` into a
  `MazeView` (SPEC §4.3.4). `proto.ts::decodeMapInit` only returns the
  raw `packedTiles`; this module does the bit unpacking.
- **Animations** (`web/src/anim.ts`) — 8-direction 4-frame walk cycle,
  muzzle flash on fire, death poof on kill, spawn-invuln shimmer.
  Animation phase is a pure function of `(entityId, renderTick)` so it
  is deterministic under a frozen clock (golden-test requirement).
- **Entity registry** (`web/src/registry.ts`) — a presentation-side
  map of `entityId → {kind, lastSeenTick, x, y, facing}` rebuilt from
  every `Snapshot`. Because `Event` (0x04) frames carry only
  `{kind, actor, target, reason}` (no entity kind), animation and audio
  resolve an event's actor/target *kind* through this registry, with a
  defined fallback when the id is outside AOI or already removed.
- **Audio** (`web/src/audio.ts`) — procedurally-synthesised WebAudio
  cues: shoot, hit, scream (player death), generator collapse, spawn,
  victory. Driven by `Event` (0x04) + `MatchOver` (0x08), resolving
  kinds via the registry. A swappable sink lets vitest assert
  event→cue mapping without hardware.
- **HUD** (`web/src/hud.ts`) — HP / lives / score readout, minimap,
  hold-`Tab` scoreboard overlay (from the `Scoreboard` 0x0B frame), the
  in-match chat overlay (§7), and the end-of-match dialog (from
  `MatchOver` 0x08 + the cached `Scoreboard` id→nick map).
- **Input** (`web/src/input.ts`) — keyboard handling with both the
  **Classic** (default: arrows move, WASD fire, Space turbo) and
  **Modern** (WASD move, arrows fire, Shift turbo) presets per
  SPEC §3.10, plus chat (`T`/`Enter`/`Esc`) and menu (`Esc`) actions.
  Rebindable; bindings persist in `localStorage`.
- **In-match chat** (client overlay + bounded server relay) — SPEC §3.9
  (dead-cam chat enabled) and §3.10.1 (`T` opens, `Enter` sends, `Esc`
  cancels). See §7.
- **Settings** (`web/src/settings.ts`) — preset + rebinds, color-blind
  palette toggle, high-contrast toggle, master-volume slider, editable
  nick, and a validated server-list. Round-trips via `localStorage`;
  cleared on opt-out.
- **Level selector** — `lobby.ts` gains a `level_presets` parser and an
  `onLevelPresets` callback (Phase 6 delivers the envelope but the
  client currently ignores it); `browser.ts` renders the 26 × 9 picker
  with preset previews.
- **App state transitions** — `main.ts` currently declares
  `AppState` but never leaves `LOBBY`; Phase 7 implements
  `LOBBY → IN_MATCH → POST_MATCH → LOBBY`.
- **Golden screenshots** (`web/tests/e2e/golden.spec.ts`) plus a
  **real-WS integration test** (`web/tests/e2e/play.spec.ts`).

**Definition of done.** All of the following pass on `main`:

1. `go test -race -count=1 ./...` green, including the new
   `internal/match` chat-relay tests; the Phase 1/3/5 determinism
   fingerprint goldens are byte-identical (no `internal/sim` edit).
2. `schemaChecksum` is `0x42607394`; `TestSchemaChecksumValue` and
   `web/tests/proto.test.ts` pass unmodified; a CI path-diff guard
   fails the build if any **frozen file** changes (`internal/sim/**`,
   `internal/proto/checksum.go`, `web/src/proto.ts`, `web/src/sim.ts`,
   `web/src/prediction.ts`, `web/src/interp.ts`, `web/src/netClient.ts`).
3. `npm -C web test` (vitest) green: all P4–P6 suites still pass, plus
   the new render / maze / anim / registry / audio / hud / input /
   settings suites.
4. `npm -C web run test:e2e` green: `deeplink.spec.ts` (P6),
   `golden.spec.ts`, and `play.spec.ts` all pass on Chromium.
5. `TestRender_CameraFollowsLocalPlayer` — the local player's predicted
   entity is centered (± half a tile) in the viewport away from edges.
6. `TestRender_MazeOffscreenCacheReusedAcrossFrames` — the static maze
   rasterises once per `setMap`; a second same-map frame does not
   re-rasterise tiles.
7. `TestRender_EntitiesSortedByY` — draw order is ascending world `y`.
8. `TestMaze_UnpackPacking1` — `unpackTiles(packed, w, h)` decodes
   2-bit little-endian tiles; round-trips a known fixture; rejects a
   short buffer.
9. `TestAnim_WalkCyclePhaseDeterministic` — `walkFrame(id, tick, moving)`
   is pure; cycles every `WALK_FRAME_TICKS`; idle → frame 0.
10. `TestAnim_MuzzleFlashOnFire` + `TestAnim_DeathPoofOnKill` — overlays
    arm from registry-resolved events and decay after their durations.
11. `TestRegistry_ResolveEventEntityKinds` — actor/target kinds resolve
    from the last `Snapshot`; an id absent from the registry resolves to
    `kind = unknown` and animation/audio degrade gracefully (no throw,
    documented fallback cue/none).
12. `TestAudio_EventCueMapping` — each `EventKind` / `MatchOver` reason
    maps to the correct cue via the fake sink and the registry; unmapped
    or unresolved kinds are silent.
13. `TestAudio_MasterVolumeGatesOutput` — master volume 0 emits no cue;
    > 0 scales gain; read live from `settings.ts`.
14. `TestHUD_ScoreboardDecodeAndOrder` — a `Scoreboard` (0x0B) decodes
    to `{id, nick, lives, score}` rows ordered desc score, ties asc id.
15. `TestHUD_TabHoldTogglesScoreboard` — `keydown Tab` shows / `keyup
    Tab` hides; `Tab` is suppressed from focus traversal **only** while
    `IN_MATCH`.
16. `TestHUD_EndDialogFromMatchOver` — the dialog renders the §3.8.1
    reason text and the winner **nick resolved from the cached
    `Scoreboard` id→nick map** (fallback `"Player <id>"` when uncached);
    "No single winner" when `winner_id == 0`.
17. `TestHUD_MinimapPlotsEntities` — plots the local player + visible
    entities at map-scaled coords within the minimap rect.
18. `TestHUD_InMatchChatOverlay` — a relayed in-match chat message
    renders in the overlay with its sender attribution; the overlay is
    available in dead-cam (§3.9).
19. `TestInput_ClassicAndModernPresets` — both presets resolve held
    keys to the correct `{dir, turbo, fire}` intent; diagonals via
    adjacent-key combos; opposing keys cancel.
20. `TestInput_RebindRejectsOverlap` — a binding where the move-key set
    and fire-key set intersect is rejected (SPEC §3.10.3); the prior map
    is retained.
21. `TestApp_MatchStateTransitions` — `App` moves
    `LOBBY → IN_MATCH` on `matchStarted`, `IN_MATCH → POST_MATCH` on
    `MatchOver`, and `POST_MATCH → LOBBY` on the back action.
22. `TestLobby_LevelPresetsCallback` — a `level_presets` envelope fires
    `onLevelPresets` with 234 parsed `LevelPreset` entries; the picker
    renders 26 × 9 cells and the selected cell's preview shows
    difficulty / lives / generators.
23. `TestSettings_RoundTripLocalStorage` — preset + rebinds +
    color-blind + high-contrast + master-volume + nick + server-list
    serialize to and restore from `localStorage`; reset clears the key;
    corrupt JSON → defaults (no throw).
24. `TestSettings_ServerListUrlValidation` — server URLs are parsed with
    `URL`, normalized to origin only, userinfo/fragments/query rejected,
    `ws://` rejected from an `https:` page; only the validated origin is
    stored.
25. `TestScene_ProductionIgnoresSceneParam` — the `?scene=` golden
    harness is inert in a production bundle (gated by an injected
    test-only dependency, not present in `browser.ts`'s prod boot).
26. `golden.spec.ts` ≤ 2 % per-pixel diff on Chromium for all five
    scenes (empty lobby, full lobby, in-match HUD, scoreboard, end
    screen) against committed baselines.
27. `play.spec.ts` — a real match WS: two contexts start a match;
    each asserts the canvas paints non-empty after `MapInit`+`Snapshot`,
    and a synthesized key event produces an outbound `Input` (0x01)
    frame observed by the server (or a server-driven position change).
28. Per-file coverage ≥ 70 % statements for **every** new `web/src`
    module (`render.ts`, `maze.ts`, `anim.ts`, `registry.ts`,
    `audio.ts`, `hud.ts`, `input.ts`, `settings.ts`, `palette.ts`),
    via `vitest --coverage` (`@vitest/coverage-v8`); `internal/match`
    stays ≥ 70 %.

Items 1–28 are gate-able in CI. The nightly cross-browser strict-diff
job (≤ 0.5 % across Chromium/Firefox/WebKit) is **deferred-to-operator**
(non-PR-blocking) per SPEC §8 Phase 7 and §9.

---

## 2. Out of scope

Explicitly **not** built in Phase 7:

- **Any wire-schema change.** `schemaChecksum` frozen; `proto.ts` and
  `checksum.go` untouched.
- **Any `internal/sim` change.** Determinism fingerprints frozen.
- **Sprite-sheet PNG art.** SPEC §7.4 sketches PNG sheets; Phase 7 draws
  entities with deterministic Canvas2D vector primitives (§6.4, open
  question §19.1). No binary art is committed.
- **jsfxr binary sound files.** SPEC §7.4 sketches `jsfxr` `.wav`s;
  Phase 7 synthesises cues at runtime (§7.3 audio, open question §19.2).
  No binary audio is committed.
- **Load / perf / Docker / deploy** — Phase 8.
- **Matchmaking ranking, accounts, persistence** — non-goals per
  SPEC §1.2.
- **Mobile / touch controls** — keyboard only in v1.
- **Team chat (scope 2)** — SPEC §541 reserves it for "future"; Phase 7
  ships lobby chat (scope 0) and match chat (scope 1) only.
- **Replay viewer UI** — `scripts/replay.go` is Phase 8 tooling.

---

## 3. Prerequisites and assumptions

- Phases 1–6 are on `main`. The headless client core
  (`prediction.ts`, `interp.ts`, `netClient.ts`, `lobby.ts`,
  `main.ts`) is unit-tested; Phase 7 adds presentation + the chat relay
  and **must not** alter prediction/interp math.
- `interp_ticks = 2`; non-self entities render ~100 ms behind real time
  (SPEC §6 / lines 201, 887). Self is drawn from the prediction buffer
  with no interpolation lag.
- The build is **esbuild**, not Vite (Phase 6 deviated from SPEC §7.1;
  Phase 7 keeps esbuild — open question §19.3). `browser.ts` is the
  single bundled entry → `dist/app.js`.
- Wire facts Phase 7 consumes (all already defined, none changed):
  - Entity kinds: `Player=1, Generator=2, Projectile=3, Snipe=4`
    (`internal/sim/config.go`).
  - Entity flags: `Dead=0x01, SpawnInvuln=0x02, Turbo=0x04` (`proto.ts`).
  - `EventKind` (`proto.ts`): `EntitySpawn=0x01, EntityHit=0x02,
    EntityKill=0x03, GeneratorDestroyed=0x04, PlayerJoin=0x05,
    PlayerLeave=0x06, PlayerDC=0x07, PlayerRejoin=0x08,
    MatchStarting=0x09, MatchStarted=0x0a, MatchEnd=0x0b,
    ChatRelay=0x0c, RespawnPending=0x0d`. The `Event` payload is
    `{u8 kind, u32 actor, u32 target, u8 reason}` — **no entity kind and
    no text** (drives §6.3 registry and §7 chat design).
  - `Chat` (0x05): `u8 len, utf8 text` — C↔S (SPEC §4.3.2 line 495).
  - `Scoreboard` (0x0B): `u32 server_tick, u8 entry_count,
    [u32 player_id, u8 nick_len, utf8 nick, u8 lives, i32 score]×n`.
    The **only** frame carrying nicks.
  - `MatchOver` (0x08): `u32 final_tick, u8 reason, u32 winner_id_or_0,
    u8 entry_count, [u32 player_id, i32 score, u8 lives_remaining]×n`
    — **no nick** (drives §8.4 cached-nick design); reason enum
    `0=PVE_COMPLETE, 1=LAST_STANDING, 2=ALL_ELIMINATED, 3=TIMER,
    4=SERVER_ERROR`.
  - `MapInit` (0x09): `u32 seed, u16 width, u16 height, u8 packing,
    bytes packed_tiles`; `packing=1` = 2 bits/tile little-endian (SPEC
    §4.3.4). `proto.ts::decodeMapInit` returns `packedTiles` **raw**;
    `maze.ts` unpacks (§6.5).
- Existing `lobby.ts` callbacks: `onWelcome`, `onRoomListChange`,
  `onChatRelay`, `onMatchStarted`. Phase 7 **adds** `onLevelPresets`.
  `main.ts` has `state: AppState` but no transitions; Phase 7 adds them.
- Node ≥ 20; system Google Chrome reachable for Playwright
  (`channel: "chrome"`). New devDep: `@vitest/coverage-v8` (updates
  `web/package-lock.json`). No new runtime deps.

---

## 4. Package and file layout

New files in Phase 7:

```
web/src/
├── render.ts        # NEW — Canvas2D render pipeline (§6)
├── maze.ts          # NEW — MapInit packing=1 → MazeView unpacker (§6.5)
├── anim.ts          # NEW — pure animation-phase functions (§6.3)
├── registry.ts      # NEW — entityId → kind/pos resolver from snapshots (§6.6)
├── audio.ts         # NEW — WebAudio cue synth + event router (§7.3)
├── hud.ts           # NEW — HP/lives/score, minimap, scoreboard, chat, end dialog (§8)
├── input.ts         # NEW — keyboard → intent, Classic+Modern presets, rebinds (§9)
├── settings.ts      # NEW — localStorage-backed settings store (§10)
├── palette.ts       # NEW — default + color-blind + high-contrast palettes (§10.2)

internal/match/
├── chat.go          # NEW — in-match Chat(0x05) relay through the match actor (§7)
├── chat_test.go     # NEW

web/tests/
├── render.test.ts   # NEW
├── maze.test.ts     # NEW
├── anim.test.ts     # NEW
├── registry.test.ts # NEW
├── audio.test.ts    # NEW
├── hud.test.ts      # NEW
├── input.test.ts    # NEW
├── settings.test.ts # NEW
├── fixtures/
│   ├── scenes.ts    # NEW — canned MapInit/Snapshot/Scoreboard/MatchOver byte fixtures
│   └── README.md    # NEW — how the byte fixtures were generated/regenerated
└── e2e/
    ├── golden.spec.ts   # NEW — 5 reference-scene screenshot diffs (§12)
    └── play.spec.ts     # NEW — real-WS render + input integration (§12.4)

web/tests/e2e/golden.spec.ts-snapshots/   # NEW — committed Chromium baseline PNGs
```

Existing files modified by Phase 7:

```
web/src/
├── browser.ts   # wires render loop + audio + hud + input + settings + chat into
│                # the match section; renders the level selector + settings panel +
│                # nick-edit field in the lobby; adds the live state-transition glue.
│                # The ?scene= golden harness is mounted ONLY when a test-only dep
│                # is injected (§12.2) — the prod boot path does not reference it.
├── main.ts      # implements LOBBY→IN_MATCH→POST_MATCH→LOBBY; exposes the decoded
│                # match-frame stream (MapInit/Snapshot/Scoreboard/Event/MatchOver/
│                # Chat) to the presentation layer via callbacks. No prediction/
│                # interp/lobby logic change.
├── lobby.ts     # adds LevelPreset type + level_presets parsing + onLevelPresets
│                # callback; adds sendChat already exists (P6) — unchanged.
├── index.html   # canvas sizing + settings/hud/chat mount containers.

web/
├── package.json     # add `test:coverage` script + @vitest/coverage-v8 devDep.
├── package-lock.json# updated by the @vitest/coverage-v8 install.
├── playwright.config.ts  # pin deviceScaleFactor=1, fixed viewport; add golden +
│                         # play projects.
├── vitest.config.ts # add coverage (provider v8, include src/**, per-file thresholds).

.github/workflows/   # (or equivalent CI) add the frozen-file path-diff guard (DoD #2).
```

`internal/sim/**`, `internal/proto/**`, `internal/net/**`, `cmd/**`,
`web/src/proto.ts`, `web/src/sim.ts`, `web/src/prediction.ts`,
`web/src/interp.ts`, and `web/src/netClient.ts` are **NOT** edited. The
chat relay lives entirely in `internal/match` (it consumes the existing
`Chat`/`Event` frame types via `internal/net`'s existing read path).

---

## 5. Public API additions

```ts
// maze.ts
export function unpackTiles(packed: Uint8Array, w: number, h: number): Uint8Array; // tile codes
export function mazeViewFromMapInit(m: MapInit): MazeView; // (uses sim.ts MazeView shape)

// render.ts
export interface Camera { x: number; y: number; w: number; h: number; }
export interface RenderState {
  map: MazeView | null;
  selfId: number;
  selfPredicted: { x: number; y: number; facing: number; flags: number } | null;
  entities: Entity[];           // interpolated non-self entities (subtile coords)
  renderTick: number;           // drives animation phase; frozen in golden tests
}
export class Renderer {
  constructor(canvas: HTMLCanvasElement, palette: Palette);
  setMap(m: MazeView): void;     // (re)builds the OffscreenCanvas maze cache
  draw(s: RenderState, hud: HudModel): void;  // one frame; pure wrt inputs
  worldToScreen(cam: Camera, x: number, y: number): [number, number];
}

// registry.ts — populated from every Snapshot; the ONLY source of entity kind.
export interface RegEntry { kind: number; x: number; y: number; facing: number; lastTick: number; }
export class EntityRegistry {
  update(s: Snapshot): void;
  kindOf(id: number): number;     // returns 0 (unknown) if not currently known
  get(id: number): RegEntry | null;
}

// anim.ts — all pure, no clock, no DOM.
export const WALK_FRAME_TICKS = 4;
export const MUZZLE_FLASH_TICKS = 6;
export const DEATH_POOF_TICKS = 18;
export function walkFrame(entityId: number, renderTick: number, moving: boolean): 0|1|2|3;
export function dir8FromFacing(facing: number): 0|1|2|3|4|5|6|7;

// audio.ts
export interface AudioSink { play(cue: Cue, gain: number): void; }
export type Cue = "shoot"|"hit"|"scream"|"generator"|"spawn"|"victory";
export class AudioEngine {
  constructor(sink: AudioSink, reg: EntityRegistry, getMasterVolume: () => number);
  onEvent(kind: number, actor: number, target: number, reason: number): void;
  onMatchOver(reason: number): void;
}

// hud.ts
export interface ScoreRow { id: number; nick: string; lives: number; score: number; }
export interface ChatLine { fromId: number; fromNick: string; text: string; }
export interface HudModel {
  hp: number; lives: number; score: number;
  rows: ScoreRow[];                 // desc score, asc id
  nickById: Map<number, string>;    // cached from the latest Scoreboard
  showScoreboard: boolean;
  chat: ChatLine[];                 // ring buffer, newest last
  deadCam: boolean;
  endDialog: { reason: number; winnerId: number; rows: ScoreRow[] } | null;
}
export function decodeScoreboard(payload: Uint8Array): ScoreRow[];
export function reasonText(reason: number): string;
export function winnerLabel(winnerId: number, nickById: Map<number,string>): string;

// input.ts
export type Action =
  | "moveN"|"moveE"|"moveS"|"moveW"
  | "fireN"|"fireE"|"fireS"|"fireW"
  | "turbo"|"chatOpen"|"menu";
export type Preset = "classic"|"modern";
export const PRESETS: Record<Preset, Bindings>;
// Bindings map ACTION → physical key code (so two actions cannot share a key
// in the data model, and overlap is detectable — see §9.2).
export type Bindings = Record<Action, string>;   // Action → KeyboardEvent.code
export class InputController {
  constructor(bindings: Bindings);
  intent(): ClientInputIntent;      // resolves held keys → {dir, turbo, fireDir}
  setBindings(b: Bindings): { ok: true } | { ok: false; conflict: string };
}

// settings.ts
export interface Settings {
  preset: Preset;
  bindings: Bindings;
  colorBlind: boolean;
  highContrast: boolean;
  masterVolume: number;     // 0..1
  nick: string;
  servers: string[];        // validated, normalized origins
}
export function loadSettings(): Settings;          // defaults if absent/corrupt
export function saveSettings(s: Settings): void;
export function resetSettings(): void;             // clears the localStorage key
export function normalizeServerUrl(raw: string, pageProtocol: string): string | null;

// lobby.ts (additions)
export interface LevelPreset {
  letter: string; number: number; difficulty: string;
  playerLives: number; generators: number; maxSnipes: number; description: string;
}
// new callback on LobbyClient:
//   onLevelPresets: LobbyEventHandler<LevelPreset[]> | null
```

---

## 6. Rendering (§7.3)

### 6.1 Pipeline (per `requestAnimationFrame`)

Mirrors SPEC §7.3: (1) interpolate non-self world at `t = now − 100 ms`
(existing `interp.ts`); (2) override `selfId` with `selfPredicted`;
(3) clear + blit the cached visible maze; (4) draw entities sorted asc
world `y`; (5) draw projectiles with a short 2-segment trail; (6) draw
the HUD (§8); (7) schedule the next frame.

### 6.2 Camera

Centers the local player at `TILE_PX = 32`, `PX_PER_SUBTILE =
TILE_PX / SUBTILE_PER_TILE`, clamped so the viewport never shows outside
the maze. DoD #5 pins "self centered ± half a tile" away from edges.

### 6.3 Animation phase (deterministic)

Pure function of `(entityId, renderTick)`, never wall-clock (golden
requirement, §12): walk cycle (`walkFrame`, dir from
`dir8FromFacing(facing)`), muzzle flash (`MUZZLE_FLASH_TICKS`), death
poof (`DEATH_POOF_TICKS`), spawn-invuln shimmer (`FlagBits.SpawnInvuln`).
Overlays are armed from registry-resolved events (§6.6) and purged when
`renderTick − armedAtTick` exceeds their duration.

### 6.4 Sprites (vector primitives — deviation from §7.4)

Entities draw as Canvas2D vector primitives (player circle + facing
wedge; snipe diamond; generator square with 3 hp-keyed damage states;
projectile dot + trail). Rationale: no committed art, deterministic
pixels for ≤ 2 % golden diffs, negligible cost at ≤ ~80 entities;
swapping in PNG sheets later is localized behind `Renderer`. Open
question §19.1.

### 6.5 Maze unpacking + cache

`maze.ts::unpackTiles` decodes `packing = 1` (2 bits/tile, little-endian
within each byte; tile codes `0=WALL,1=FLOOR,2=SPAWN_PLAYER,
3=SPAWN_GENERATOR`) from `MapInit.packedTiles` into a `MazeView`
(SPEC §4.3.4). On `Renderer.setMap`, the full maze is rasterised once to
an `OffscreenCanvas` (fallback: detached `<canvas>`); frames blit the
visible sub-rect; tiles never re-rasterise unless `setMap` is called
again. DoD #6/#8.

### 6.6 Entity registry (kind resolution for events)

`Event` (0x04) frames carry only `{kind, actor, target, reason}` — no
entity kind. `EntityRegistry` is rebuilt from every `Snapshot`
(`entityId → {kind, x, y, facing, lastTick}`) and is the **only** source
of an event entity's kind. Animation/audio call `reg.kindOf(actor)` /
`reg.kindOf(target)`. When an id is **outside AOI or already removed**
(`kindOf` returns `0 = unknown`): the muzzle flash/death poof is skipped
(no anchor entity to draw on) and the audio cue uses the conservative
default in §7.3 (e.g. an `EntityKill` with an unknown target still plays
`hit`, never `scream`, since `scream` requires a confirmed player
target). DoD #11.

---

## 7. In-match chat (§3.9, §3.10.1)

### 7.1 Why a server change is required

SPEC §3.9 requires dead-cam players to chat; §3.10.1 binds `T`/`Enter`/
`Esc`. The match WS already defines `Chat` (0x05, C↔S) but the server
currently **ignores** it (`internal/net/server.go` drops unknown types;
`internal/match` accepts only `Input`/`Chat` slots but does not relay
chat). Phase 7 adds the relay. This is the **only** server change and
it touches **no wire schema** — `Chat` (0x05) and `Event/ChatRelay`
(0x0C) already exist in the descriptor, so `schemaChecksum` is unchanged.

### 7.2 Relay design

`internal/match/chat.go`: when the match actor receives a `Chat`
(0x05) `{text}` from a slot (including a dead-cam slot, per §3.9):

1. Validate `1 ≤ len(text) ≤ 255` (the frame's `u8 len` ceiling) and
   trim; reject empty (drop, no error frame — the match WS has no Error
   frame, §4.3.5).
2. Re-broadcast to every connected slot (incl. dead-cam) as the pair:
   - an `Event` (0x04) `{kind: ChatRelay(0x0C), actor: senderEntityID,
     target: 0, reason: 1 /* match scope, SPEC §541 */}` — so the client
     learns *who* sent it (the registry maps `actor` → nick via the
     cached `Scoreboard`), then
   - the `Chat` (0x05) `{text}` frame carrying the message body.
   The client correlates the two consecutive frames (relay event
   immediately followed by the chat text). **This pairing is the one
   genuinely under-specified wire detail — see open question §19.5.**
3. Per-slot best-effort: a full out-queue drops the message for that
   slot only.

A per-slot rate limit mirrors the lobby's (§P6 6.2): 4 msgs / 2 s.

### 7.3 Audio cue router

`AudioEngine.onEvent` resolves kinds through the registry (§6.6):

| Event (resolved) | Cue |
|---|---|
| `EntitySpawn`, `kindOf(actor or spawned)= Projectile` | `shoot` |
| `EntityHit` | `hit` |
| `EntityKill`, `kindOf(target) = Player` | `scream` |
| `EntityKill`, `kindOf(target) = Snipe` | `hit` |
| `EntityKill`, target unknown | `hit` (conservative) |
| `GeneratorDestroyed` | `generator` |
| `PlayerJoin` / `PlayerRejoin` | `spawn` |
| `ChatRelay` and all others | silent |

`onMatchOver(reason)` plays `victory` for `PVE_COMPLETE`/`LAST_STANDING`/
`TIMER`; silent for `ALL_ELIMINATED`/`SERVER_ERROR`.

The production sink lazily creates one `AudioContext` on first user
gesture (autoplay policy) and synthesises cues from oscillator/noise
bursts (no binary assets, §19.2). Gain = `getMasterVolume()` clamped to
`[0,1]`; `0` short-circuits before any node is created. Tests inject a
fake sink recording `(cue, gain)`. DoD #12/#13.

### 7.4 Client overlay

`hud.ts` keeps a chat ring buffer (`ChatLine[]`, cap 50). `T` opens an
input line (`input.ts` `chatOpen`); `Enter` sends a `Chat` frame via
`netClient`; `Esc` cancels. Incoming relay-event + chat-text pairs
append a `ChatLine{fromId, fromNick, text}` (nick from
`HudModel.nickById`, fallback `"Player <id>"`). The overlay renders in
both live and dead-cam states. DoD #18.

---

## 8. HUD (§7.3 step 6)

### 8.1 Readout
Top-left HP (number — never color-only, §7.5), lives (icons + number),
score, from the local player's `Scoreboard` row + self `hp`.

### 8.2 Scoreboard overlay
`decodeScoreboard` parses 0x0B; rows desc score, ties asc id. Shown
while `Tab` is held; `Tab` focus traversal suppressed **only** in
`IN_MATCH` (must not leak to lobby — accessibility). DoD #14/#15.

### 8.3 Minimap
Fixed bottom-right rect; world→minimap scaling plots the local player
(distinct marker) + visible entities; walls from a downscaled maze
cache. DoD #17.

### 8.4 Nick cache + end-of-match dialog
The HUD retains `nickById` from **every** `Scoreboard` frame (the only
nick source). On `MatchOver` (0x08) — which carries no nicks — the
dialog shows `reasonText(reason)`, `winnerLabel(winner_id, nickById)`
(the cached nick, fallback `"Player <id>"`, or `"No single winner"` when
`winner_id == 0`), and the final scoreboard built by joining the
`MatchOver` entry list `{id, score, lives}` against `nickById`. The
end-screen golden fixture therefore contains a `Scoreboard` frame
**before** the `MatchOver` frame (§12.1). A "Back to lobby" button
drives `POST_MATCH → LOBBY` (§main.ts, DoD #21). DoD #16.

---

## 9. Input & rebinds (§3.10)

### 9.1 Presets
`PRESETS.classic` (default): arrows → move, `KeyW/D/S/A` → fire,
`Space` → turbo, `KeyT` → chatOpen, `Escape` → menu. `PRESETS.modern`:
`KeyW/A/S/D` → move, arrows → fire, `ShiftLeft` → turbo, chat/menu as
Classic (SPEC §3.10.2). Held keys resolve to a `Dir8` move intent
(adjacent pair → diagonal, opposing → cancel) and an independent
`fireDir`. DoD #19.

### 9.2 Rebinding rules
Bindings are modeled `Action → code` so a key collision is detectable.
`setBindings` rejects any map where the **move-key set and the fire-key
set intersect** (SPEC §3.10.3: "Movement and shoot key sets must not
overlap") — returns `{ok:false, conflict}` and the caller keeps the
prior map. Turbo/chat/menu may not collide with move/fire either.
Accepted binds persist via `settings.ts`. DoD #20.

---

## 10. Settings (§7.5)

### 10.1 Store
One `localStorage` key (`isnipes.settings`, JSON). `loadSettings`
returns typed defaults when absent/corrupt (try/catch around
`JSON.parse`). `resetSettings` removes the key. DoD #23.

### 10.2 Palettes & accessibility
`palette.ts` exposes `default`, `colorBlind` (no info by red/green
alone), and a `highContrast` modifier (thicker outlines) per §7.5.
`Renderer` takes a `Palette`; toggling rebuilds the maze cache. HP is
always a number, so no critical info is color-only.

### 10.3 Server-list (security-hardened)
`normalizeServerUrl(raw, pageProtocol)`:
- Parse with the `URL` constructor; reject on throw.
- Accept only `ws:`/`wss:` (or a bare host → infer `wss:` on an
  `https:` page, `ws:` on `http:`).
- **Reject `ws:` when the page is `https:`** (mixed content).
- **Reject** any URL with userinfo (`user:pass@`), a fragment, or a
  query string.
- Store the **origin only** (`scheme://host[:port]`), deduped, cap 8.

Match-WS resolution: `matchStarted.gameSocketPath` is treated as a
**path** and resolved against the **selected lobby origin** (the one the
lobby WS connected to), never against an attacker-supplied absolute URL.
Reject an absolute or cross-origin `gameSocketPath` before sending the
`joinToken`. DoD #24.

### 10.4 Level selector
`lobby.ts` parses the Phase 6 `level_presets` envelope into
`LevelPreset[]` and fires `onLevelPresets`. `browser.ts` renders the
26 × 9 picker; selecting a cell shows its preview (difficulty bucket,
lives, generators, max snipes, description) and sets `createRoom.level`.
DoD #22.

---

## 11. Determinism & no-regression rules

- No `internal/sim` edit → P1/P3/P5 fingerprint goldens byte-identical.
- `schemaChecksum` `0x42607394`; `proto.ts`/`checksum.go` untouched; a
  CI path-diff guard fails the build if any frozen file (§4) changes.
- Animation phase is pure in `(entityId, renderTick)`; golden scenes
  render one frame at a fixed `renderTick` with `deviceScaleFactor = 1`
  and a fixed viewport; HUD text uses a procedurally-drawn bitmap glyph
  set (no system-font variance).
- The chat relay is additive: existing `internal/match` and
  `internal/net` tests continue to pass; new `chat_test.go` covers it.

---

## 12. Golden + integration harness

### 12.1 Scenes (golden.spec.ts)
Five scenes from byte fixtures in `web/tests/fixtures/scenes.ts`:
1. **empty-lobby** — connected, zero rooms.
2. **full-lobby** — N rooms incl. "my room"; level picker open.
3. **in-match-hud** — `MapInit` + `Snapshot` (self + snipe + generator +
   projectile) + `Scoreboard`; HUD visible.
4. **scoreboard** — (3) with the `Tab` overlay shown.
5. **end-screen** — `Scoreboard` **then** `MatchOver{LAST_STANDING}`
   (so the winner nick resolves, §8.4); end dialog shown.

### 12.2 Screenshot route (test-gated, not in prod)
The `?scene=<name>` harness feeds canned fixture frames through the
**real** maze/render/hud path and renders one deterministic frame at a
fixed `renderTick`, then sets `data-testid="scene-ready"`. It is mounted
**only** when a test-only module is present (e.g. a separate
`browser.test-entry.ts` bundled solely for the e2e build, or an injected
`window.__ISNIPES_TEST__` hook that the prod boot never sets). The
production `browser.ts` boot path contains no `?scene=` branch. DoD #25
asserts a prod bundle ignores `?scene=`.

### 12.3 Baselines & CI split
Baselines committed under `golden.spec.ts-snapshots/` (Chromium/linux).
PR gate: Chromium ≤ 2 % (`maxDiffPixelRatio: 0.02`). The nightly
cross-browser ≤ 0.5 % job is deferred-to-operator (needs Firefox/WebKit
binaries; non-blocking per SPEC §8/§9).

### 12.4 Real-WS integration (play.spec.ts)
Two browser contexts run the **live** flow (no `?scene=`): A creates a
room, B deep-links in, A starts; both reach `IN_MATCH`. Each asserts the
canvas paints non-empty after `MapInit`+`Snapshot` (sample a center
pixel ≠ background), then dispatches a movement key and asserts an
outbound `Input` (0x01) frame is sent (observed via a server-side
test counter exposed on a debug endpoint, or via a subsequent
`Snapshot` showing the self entity moved). This closes the gap where the
golden harness bypasses the live path. DoD #27.

---

## 13. Wire protocol notes
No schema change. `schemaChecksum = 0x42607394`. Phase 7 consumes
`MapInit`, `Snapshot`, `Event`, `Chat`, `Scoreboard`, `MatchOver`
(all pre-defined) and the lobby `level_presets` JSON envelope (P6). The
chat relay reuses `Chat` (0x05) + `Event/ChatRelay` (0x0C); both are
already in the descriptor.

---

## 14. Concurrency rules
Client: single-threaded JS event loop (`requestAnimationFrame`, WS
callback, key listeners); `OffscreenCanvas` for caching only, no worker
transfer. Server: the chat relay runs inside the existing match actor
goroutine (the actor already owns slot state); no new locks. The relay
broadcasts on the same per-slot out-channels the snapshot path uses.

---

## 15. Test plan

### 15.1 render.test.ts
- `TestRender_CameraFollowsLocalPlayer` (#5),
  `TestRender_MazeOffscreenCacheReusedAcrossFrames` (#6),
  `TestRender_EntitiesSortedByY` (#7),
  `TestRender_WorldToScreenRoundTrip`,
  `TestRender_CameraClampsAtMapEdge`.

### 15.2 maze.test.ts
- `TestMaze_UnpackPacking1` (#8), `TestMaze_RejectsShortBuffer`,
  `TestMaze_MazeViewBoundsClampToWall`.

### 15.3 anim.test.ts
- `TestAnim_WalkCyclePhaseDeterministic` (#9),
  `TestAnim_MuzzleFlashOnFire` + `TestAnim_DeathPoofOnKill` (#10),
  `TestAnim_OverlaysDecayAndPurge`.

### 15.4 registry.test.ts
- `TestRegistry_ResolveEventEntityKinds` (#11),
  `TestRegistry_UnknownIdReturnsZero`,
  `TestRegistry_RebuildsEachSnapshot`.

### 15.5 audio.test.ts (fake sink + registry)
- `TestAudio_EventCueMapping` (#12),
  `TestAudio_MasterVolumeGatesOutput` (#13),
  `TestAudio_MatchOverVictoryVsSilent`,
  `TestAudio_UnknownTargetPlaysHitNotScream`.

### 15.6 hud.test.ts
- `TestHUD_ScoreboardDecodeAndOrder` (#14),
  `TestHUD_TabHoldTogglesScoreboard` (#15),
  `TestHUD_EndDialogFromMatchOver` (#16),
  `TestHUD_MinimapPlotsEntities` (#17),
  `TestHUD_InMatchChatOverlay` (#18),
  `TestHUD_ReasonTextEnum`, `TestHUD_WinnerLabelFallback`.

### 15.7 input.test.ts
- `TestInput_ClassicAndModernPresets` (#19),
  `TestInput_DiagonalCombos`, `TestInput_RebindRejectsOverlap` (#20).

### 15.8 settings.test.ts
- `TestSettings_RoundTripLocalStorage` (#23),
  `TestSettings_DefaultsOnCorrupt`, `TestSettings_ResetClearsKey`,
  `TestSettings_ServerListUrlValidation` (#24).

### 15.9 lobby.test.ts (extend)
- `TestLobby_LevelPresetsCallback` (#22).

### 15.10 main.test.ts (extend)
- `TestApp_MatchStateTransitions` (#21).

### 15.11 internal/match/chat_test.go
- `TestMatch_ChatRelayBroadcastsToAllSlots` (incl. dead-cam sender, §7.2).
- `TestMatch_ChatRelayRateLimited`,
  `TestMatch_ChatRelayDropsEmpty`,
  `TestMatch_DeadCamMayChat` (§3.9).

### 15.12 e2e
- `TestGolden_FiveScenesChromium` (#26) — `golden.spec.ts`.
- `TestPlay_LiveRenderAndInput` (#27) — `play.spec.ts`.
- `TestScene_ProductionIgnoresSceneParam` (#25) — asserts a prod-bundle
  page at `?scene=in-match-hud` shows the normal connecting/lobby UI.

---

## 16. Testdata / fixtures
New TS byte fixtures in `web/tests/fixtures/scenes.ts` built with the
existing `proto.ts` encoders (or hand-encoded + asserted against
decoders), documented in `fixtures/README.md` (incl.
`playwright test --update-snapshots` for baselines). No new Go testdata.

---

## 17. Risks
- **Golden-diff flakiness** (central): pinned `deviceScaleFactor=1` +
  fixed viewport, single deterministic frame from canned fixtures,
  vector primitives + bitmap font, ≤ 2 % Chromium-only PR gate.
- **Baseline drift** on intentional visual change → regenerate the PNG
  (documented in `fixtures/README.md`).
- **`?scene=` shipping in prod** → mitigated by test-only gating (§12.2)
  + DoD #25.
- **Server-list / cross-origin token leak** → URL hardening + relative
  match-path resolution (§10.3) + DoD #24.
- **Chat relay sender attribution** → the relay-event/chat-text pairing
  (§7.2) is the one under-specified wire detail; open question §19.5.
- **WebAudio autoplay** → lazy `AudioContext` on first gesture; no-op
  before; fake sink in tests.
- **`OffscreenCanvas` absence** → detached `<canvas>` fallback.
- **`Tab` hijack leaking to lobby** → suppression scoped to `IN_MATCH`.
- **Color-only info** forbidden (§7.5) → HP-as-number + color-blind
  palette (asserted indirectly via the palette-swap golden pixels).

---

## 18. Determinism / no-regression guarantees
- `go test -race ./...` green incl. new `chat_test.go`; P1/P3/P5
  fingerprints byte-identical (no `internal/sim` edit).
- `schemaChecksum` `0x42607394` (DoD #2) + frozen-file CI guard.
- All P4–P6 vitest suites pass unchanged; Phase 7 only adds suites.
- P6 `deeplink.spec.ts` still passes (`match-view` /
  `data-match-connected` hooks preserved; Phase 7 renders *inside* the
  wrapper).

---

## 19. Open questions
Flagged for codex review / author decision. Defaults listed.

1. **Vector primitives vs PNG sprite sheets (§7.4).** Default: vector
   primitives (deterministic, no binary art). Alt: commit PNG sheets +
   loader (asset/AA variance complicates golden diffs).
2. **Procedural WebAudio vs jsfxr `.wav`s (§7.4).** Default: procedural
   (testable, no binary assets). Alt: commit `.wav`s (matches §7.4
   verbatim).
3. **esbuild vs Vite (§7.1).** Default: keep esbuild (Phase 6's choice;
   Vite buys nothing for a single-entry bundle). Documented, not
   reverted.
4. **Golden baseline engine.** Default: Chromium-on-linux baselines; the
   PR runner must match. A pinned-container render is the robust fix
   (Phase 8 CI hardening); 2 % usually absorbs sub-pixel AA otherwise.
5. **In-match chat sender attribution (wire).** SPEC defines `Chat`
   (0x05) `{len,text}` (no sender) and `Event/ChatRelay` (0x0C)
   `{kind,actor,target,reason}` (no text). Default: the server emits a
   `ChatRelay` event (`actor = senderEntityID`, `reason = 1` match
   scope) **immediately followed by** the `Chat` text frame, and the
   client correlates the consecutive pair; nick comes from the cached
   `Scoreboard`. This adds **no** new frame type and no checksum change.
   Alternative (rejected for v1): widen the `Chat` frame to carry a
   sender id — that *would* change the schema/checksum and is out of
   scope. **Operator: confirm the pairing approach before
   implementation of §7.2.**
6. **Minimap scope.** Default: full maze outline + only AOI-visible
   entities (the client only has AOI entities). Fog beyond AOI implicit.
7. **Modern-preset turbo key.** SPEC §3.10.2 says Shift; default binds
   `ShiftLeft` (not `ShiftRight`) — rebindable.

---

## 20. Definition of done (canonical checklist)

| # | Item | Verified by |
|---:|---|---|
| 1 | `go test -race ./...` green incl. match chat-relay; P1/P3/P5 fingerprints byte-identical | CI |
| 2 | `schemaChecksum` `0x42607394`; `proto.test.ts` unmodified; frozen-file CI path-diff guard | `TestSchemaChecksumValue` + CI |
| 3 | `npm -C web test` (vitest) green incl. new suites | CI |
| 4 | `npm -C web run test:e2e` green (deeplink + golden + play, Chromium) | CI |
| 5 | `TestRender_CameraFollowsLocalPlayer` | §15.1 |
| 6 | `TestRender_MazeOffscreenCacheReusedAcrossFrames` | §15.1 |
| 7 | `TestRender_EntitiesSortedByY` | §15.1 |
| 8 | `TestMaze_UnpackPacking1` | §15.2 |
| 9 | `TestAnim_WalkCyclePhaseDeterministic` | §15.3 |
| 10 | `TestAnim_MuzzleFlashOnFire` + `TestAnim_DeathPoofOnKill` | §15.3 |
| 11 | `TestRegistry_ResolveEventEntityKinds` (incl. unknown-id fallback) | §15.4 |
| 12 | `TestAudio_EventCueMapping` | §15.5 |
| 13 | `TestAudio_MasterVolumeGatesOutput` | §15.5 |
| 14 | `TestHUD_ScoreboardDecodeAndOrder` | §15.6 |
| 15 | `TestHUD_TabHoldTogglesScoreboard` | §15.6 |
| 16 | `TestHUD_EndDialogFromMatchOver` (winner nick via cached Scoreboard) | §15.6 |
| 17 | `TestHUD_MinimapPlotsEntities` | §15.6 |
| 18 | `TestHUD_InMatchChatOverlay` (incl. dead-cam) | §15.6 |
| 19 | `TestInput_ClassicAndModernPresets` | §15.7 |
| 20 | `TestInput_RebindRejectsOverlap` (move∩fire = ∅) | §15.7 |
| 21 | `TestApp_MatchStateTransitions` (LOBBY→IN_MATCH→POST_MATCH→LOBBY) | §15.10 |
| 22 | `TestLobby_LevelPresetsCallback` + picker renders | §15.9 |
| 23 | `TestSettings_RoundTripLocalStorage` | §15.8 |
| 24 | `TestSettings_ServerListUrlValidation` | §15.8 |
| 25 | `TestScene_ProductionIgnoresSceneParam` | §15.12 |
| 26 | `golden.spec.ts` ≤ 2 % per-pixel on Chromium, all 5 scenes | §15.12 |
| 27 | `play.spec.ts` real-WS render + input integration | §15.12 |
| 28 | Per-file coverage ≥ 70 % for every new `web/src` module; `internal/match` ≥ 70 % | `vitest --coverage` + CI |

Items 1–28 are gate-able in CI. The nightly cross-browser strict-diff
job (≤ 0.5 % across Chromium/Firefox/WebKit) is deferred-to-operator and
non-PR-blocking per SPEC §8 Phase 7 and §9.

The binary wire protocol is unchanged (`schemaChecksum = 0x42607394`).
`internal/sim/**`, `internal/proto/**`, `internal/net/**`, `cmd/**`,
`web/src/proto.ts`, `web/src/sim.ts`, `web/src/prediction.ts`,
`web/src/interp.ts`, and `web/src/netClient.ts` are not edited; the sole
server addition is the `internal/match` chat relay (§7).
