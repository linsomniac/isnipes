# Lobby Rework Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Restyle the isnipes lobby into an on-theme "Command Deck", add built-in How-to-Play instructions, remove finished games from the room list, and show the returning player a "Last Match" recap.

**Architecture:** Three workstreams. (1) Server (Go): wire match-end → the already-correct `Lobby.handleMatchEnded` via a new public `MatchEnded` poster + a `Registry.OnMatchEnded` hook, so finished rooms are removed and members' `roomID` cleared. (2) Client pure helper (TS/vitest): `buildLastMatchView` turning the `MatchOver` the client already decodes into a recap view-model. (3) Client DOM (TS): a scoped injected `<style>` + restructured `buildDOM()` lobby (Command Deck layout, cheat bar + expandable guide, Last Match card). No frozen files are touched.

**Tech Stack:** Go (actor-model lobby/match), TypeScript + Canvas2D/DOM web client, esbuild bundling, Go testing, vitest, Playwright (e2e + golden screenshots).

**Spec:** `docs/superpowers/specs/2026-06-05-lobby-rework-design.md`

**Hard constraints:**
- Do NOT edit frozen files: `internal/sim/**`, `internal/proto/**`, and web mirrors `web/src/{prediction,interp,netClient,proto,sim}.ts`. Verify with `make check-frozen`.
- Preserve every existing `data-testid` (e2e depends on `lobby`, `create-room`, `nick-input`, `my-room`, `my-room-id`, `room-players`, `start-btn`, `level-cell`, `room-row`, `join-room`, settings toggles, …).
- The lobby must stay in normal document flow with a sized shell (no `position:fixed` on lobby content) to avoid the known zero-height `document.body` collapse that breaks Playwright visibility.

---

## Phase 1 — Server: remove finished games

### Task 1: `Lobby.MatchEnded` public poster

**Files:**
- Modify: `internal/lobby/lobby.go` (add a method near `Disconnect`/`Touch`, ~line 211)
- Test: `internal/lobby/matchend_poster_test.go` (create)

Context: `handleMatchEnded` (lobby.go:654) already closes the room, deletes it, clears each member's `roomID`, and re-broadcasts the list. It is just never invoked in production because nothing posts `ctlMatchEnded`. There is no public poster (unlike `Touch`/`Disconnect`). The existing test `TestLobby_MatchEndedClosesRoom` (coverage_test.go:159) proves the handler by posting `l.in <- ctlMatchEnded{…}` directly.

- [ ] **Step 1: Write the failing test**

Create `internal/lobby/matchend_poster_test.go`:

```go
package lobby

import (
	"testing"
	"time"

	"github.com/jafo/isnipes/internal/proto"
)

// TestLobby_MatchEndedPoster: the public MatchEnded poster drives the same
// cleanup as a raw ctlMatchEnded — the room is removed and each member's
// roomID cleared — proving the production caller (the registry OnMatchEnded
// hook) reaches handleMatchEnded without touching l.in directly.
func TestLobby_MatchEndedPoster(t *testing.T) {
	l, _, done, _ := newTestLobby(t)
	defer func() { l.Stop(); <-done }()
	a, outA := connect(t, l)
	helloAndDrain(t, l, a, outA, "Alice")
	b, outB := connect(t, l)
	helloAndDrain(t, l, b, outB, "Bob")
	rid := createAndJoinRoom(t, l, a, b, outA, outB)
	sendEnvelope(t, l, a.ID, proto.LobbyStartMatch, proto.StartMatch{RoomID: rid})
	ms, ok := drainUntil(t, outA, proto.LobbyMatchStarted, 500*time.Millisecond)
	if !ok {
		t.Fatal("no matchStarted")
	}
	matchID := ms.Payload.(proto.MatchStarted).MatchID

	// Public poster (what the registry hook calls), not l.in <- … directly.
	l.MatchEnded(matchID)

	// snapshotRooms round-trips through the actor, so the MatchEnded posted
	// just above (same FIFO inbox) is already processed by the time we read.
	if _, exists := snapshotRooms(t, l)[rid]; exists {
		t.Fatalf("room %s still listed after MatchEnded", rid)
	}
	// roomID cleared: A can no longer leave the (gone) room.
	expectError(t, l, a.ID, outA, proto.LobbyLeaveRoom,
		proto.LeaveRoom{}, proto.LobbyErrNoRoom)
}

// TestLobby_MatchEndedAfterStop: MatchEnded on a stopped lobby is a no-op
// (no panic, no blocking send on the drained actor).
func TestLobby_MatchEndedAfterStop(t *testing.T) {
	l, _, done, _ := newTestLobby(t)
	l.Stop()
	<-done
	l.MatchEnded("whatever") // must not panic or block
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/lobby/ -run TestLobby_MatchEnded -v`
Expected: FAIL — compile error `l.MatchEnded undefined (type *Lobby has no field or method MatchEnded)`.

- [ ] **Step 3: Write minimal implementation**

In `internal/lobby/lobby.go`, add this method directly after `Disconnect` (the block ending at ~line 211):

```go
// MatchEnded notifies the lobby that a match has finished so its hosting room
// is closed and removed from the listing (see handleMatchEnded). Posted by the
// match registry's OnMatchEnded hook. Non-blocking and safe to drop on
// shutdown — a stopped lobby has no rooms left to clean up. Mirrors the
// Disconnect/Touch poster shape.
func (l *Lobby) MatchEnded(matchID string) {
	if l.stopped.Load() {
		return
	}
	select {
	case l.in <- ctlMatchEnded{MatchID: matchID}:
	case <-l.done:
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/lobby/ -run TestLobby_MatchEnded -v`
Expected: PASS (both `TestLobby_MatchEndedPoster` and `TestLobby_MatchEndedAfterStop`, plus the pre-existing `TestLobby_MatchEndedClosesRoom`).

- [ ] **Step 5: Commit**

```bash
git add internal/lobby/lobby.go internal/lobby/matchend_poster_test.go
git commit -m "$(cat <<'EOF'
feat(lobby): add MatchEnded poster to drive handleMatchEnded

Public, non-blocking poster (mirrors Touch/Disconnect) that enqueues
ctlMatchEnded so the already-correct handleMatchEnded runs. Wired to the match
registry in a later task; on its own this is the missing entry point that lets a
finished match close + remove its room and clear members' roomID.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 2: `Registry.OnMatchEnded` hook

**Files:**
- Modify: `internal/match/registry.go` (add config field ~line 32; invoke in `Create` goroutine ~line 79–82)
- Test: `internal/match/matchend_hook_test.go` (create)

Context: matches end in `Registry.Create`'s goroutine: `go func(){ m.Run(); r.RemoveEnded(mc.MatchID) }()`. The analogous existing hook `OnActiveMatchesDelta` (tested by `TestRegistry_ActiveMatchesGauge`, instrument_test.go:260) fires from this same goroutine.

- [ ] **Step 1: Write the failing test**

Create `internal/match/matchend_hook_test.go`:

```go
package match

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jafo/isnipes/internal/sim"
)

// TestRegistry_OnMatchEnded — the hook fires exactly once per match, with the
// match's ID, when its Run loop returns (here via StopAll). The hook runs on
// the match Run goroutine, so IDs are collected through a buffered channel.
// Mirrors TestRegistry_ActiveMatchesGauge.
func TestRegistry_OnMatchEnded(t *testing.T) {
	const n = 3
	ended := make(chan string, n)
	reg := NewRegistry(RegistryConfig{
		MaxConcurrentMatches: 16,
		OnMatchEnded:         func(id string) { ended <- id },
	})
	want := map[string]bool{}
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("end-%d", i)
		want[id] = true
		if _, err := reg.Create(MatchConfig{
			MatchID:     id,
			PlayerSlots: []PendingJoin{{PlayerID: sim.EntityID(1), Token: fmt.Sprintf("t-%d", i)}},
		}); err != nil {
			t.Fatalf("Create %d: %v", i, err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := reg.StopAll(ctx); err != nil {
		t.Fatalf("StopAll: %v", err)
	}
	got := map[string]bool{}
	for i := 0; i < n; i++ {
		select {
		case id := <-ended:
			if got[id] {
				t.Fatalf("OnMatchEnded fired twice for %s", id)
			}
			got[id] = true
		case <-time.After(2 * time.Second):
			t.Fatalf("OnMatchEnded fired only %d/%d times", i, n)
		}
	}
	for id := range want {
		if !got[id] {
			t.Fatalf("OnMatchEnded never fired for %s", id)
		}
	}
}

// TestRegistry_OnMatchEnded_NilSafe — a registry without the hook still drains
// cleanly (no panic on the nil callback).
func TestRegistry_OnMatchEnded_NilSafe(t *testing.T) {
	reg := NewRegistry(RegistryConfig{MaxConcurrentMatches: 4})
	if _, err := reg.Create(MatchConfig{
		MatchID:     "nohook",
		PlayerSlots: []PendingJoin{{PlayerID: sim.EntityID(1), Token: "x"}},
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := reg.StopAll(ctx); err != nil {
		t.Fatalf("StopAll: %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/match/ -run TestRegistry_OnMatchEnded -v`
Expected: FAIL — `unknown field OnMatchEnded in struct literal of type RegistryConfig`.

- [ ] **Step 3: Write minimal implementation**

In `internal/match/registry.go`, add to `RegistryConfig` (after `OnActiveMatchesDelta`, ~line 32):

```go
	// OnMatchEnded is invoked with the MatchID once a match's Run loop has
	// returned (right after RemoveEnded). The lobby wires this to
	// Lobby.MatchEnded so the hosting room is closed and removed. Called from
	// the match Run goroutine; nil-safe.
	OnMatchEnded func(matchID string)
```

Then update the `Create` goroutine (registry.go:79–82) to invoke it:

```go
	go func() {
		m.Run()
		r.RemoveEnded(mc.MatchID)
		if r.cfg.OnMatchEnded != nil {
			r.cfg.OnMatchEnded(mc.MatchID)
		}
	}()
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/match/ -run TestRegistry_OnMatchEnded -v`
Expected: PASS (both subtests).

- [ ] **Step 5: Commit**

```bash
git add internal/match/registry.go internal/match/matchend_hook_test.go
git commit -m "$(cat <<'EOF'
feat(match): add Registry.OnMatchEnded hook fired when a match Run loop returns

Nil-safe callback invoked with the MatchID right after RemoveEnded, on the
match Run goroutine (same site as OnActiveMatchesDelta). Wired to the lobby in
the next task so finished matches clean up their room.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 3: Wire match-end → lobby in the server entrypoint

**Files:**
- Modify: `cmd/isnipes/main.go:86–98`

Context: at the production construction site `registry` is built *before* `lob`. Pre-declare `lob` so the registry's `OnMatchEnded` closure can capture it; `lob` is assigned before the server accepts connections, so no match can end before the closure target is non-nil. (The load-test harness `internal/loadtest/driver.go:101` leaves `OnMatchEnded` nil — nil-safe, no change.)

- [ ] **Step 1: Apply the wiring change**

Replace the `registry := match.NewRegistry(...)` / `lob := lobby.NewLobby(...)` block (main.go:86–98) with:

```go
	// lob is referenced by the registry's OnMatchEnded hook below, so it is
	// declared first and the closure captures it by reference. lob is assigned
	// immediately after (before the HTTP server starts), so no match can end —
	// and fire the hook — until lob is non-nil.
	var lob *lobby.Lobby
	registry := match.NewRegistry(match.RegistryConfig{
		MaxConcurrentMatches: *maxMatches,
		TickSampler:          metrics.TickHistogram(),
		OnTickOverBudget:     metrics.IncTickOverBudget,
		OnSnapshotDrop:       metrics.IncSnapshotDrop,
		OnJoinedDelta:        metrics.AddJoinedPlayers,
		OnActiveMatchesDelta: metrics.AddActiveMatches,
		// Match end → lobby: close & remove the hosting room (stale-room fix).
		OnMatchEnded: func(id string) { lob.MatchEnded(id) },
	})
	lob = lobby.NewLobby(lobby.Config{
		Registry:      registry,
		MOTD:          *motd,
		ServerVersion: version,
	})
```

- [ ] **Step 2: Build + vet**

Run: `go build ./... && go vet ./cmd/... ./internal/lobby/... ./internal/match/...`
Expected: no output (success).

- [ ] **Step 3: Run the server test suites**

Run: `go test ./internal/lobby/... ./internal/match/...`
Expected: PASS (`ok` for both packages).

- [ ] **Step 4: Manual smoke (record result in the commit/PR, not blocking automation)**

```bash
npm -C web run build
go run ./cmd/isnipes --web-dist web/dist --insecure-origin --addr :8080
```
Open three browser windows on http://localhost:8080. In #1 create a room; in #2 open the invite link and Join; #3 just watches the lobby and sees the room. In #1 click Start; play until the match ends (e.g. both players die → "All players eliminated", or one survives). After it ends, confirm window #3's room list no longer shows that room, and that #1/#2 can create/join a new room (no "already in a room" error).

- [ ] **Step 5: Commit**

```bash
git add cmd/isnipes/main.go
git commit -m "$(cat <<'EOF'
fix(server): remove finished games from the lobby on match end

Wire Registry.OnMatchEnded -> Lobby.MatchEnded at the entrypoint via a
captured-by-reference lobby pointer. Finished rooms now disappear from every
client's list immediately and members' roomID is cleared (also fixes a latent
"stuck in your old room" bug), instead of lingering ~30s until the empty-room GC.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Phase 2 — Client: recap view-model (pure helper, TDD)

### Task 4: `buildLastMatchView` in hud.ts

**Files:**
- Modify: `web/src/hud.ts` (add export after `buildEndDialog`, ~line 146)
- Test: `web/tests/hud.test.ts` (add a `describe` block; extend the import)

Context: `hud.ts` already exports `EndDialog` (`{reason, winnerId, rows:[{id,nick,lives,score}]}`), `reasonText`, and `ScoreRow`. `rows` are already nick-joined and score-ordered by `buildEndDialog`. `hud.ts` is NOT frozen.

- [ ] **Step 1: Write the failing test**

In `web/tests/hud.test.ts`, add `buildLastMatchView` to the existing import from `"../src/hud.js"`, and append:

```ts
describe("buildLastMatchView", () => {
  const rows = [
    { id: 1, nick: "Cyan", lives: 2, score: 120 },
    { id: 2, nick: "Lime", lives: 0, score: 80 },
  ];
  test("resolves winner nick from rows + prose reason", () => {
    const v = buildLastMatchView({ reason: 1, winnerId: 1, rows });
    expect(v.winner).toBe("Cyan");
    expect(v.reason).toBe("Last one standing");
    expect(v.rows).toHaveLength(2);
  });
  test("winnerId 0 -> no single winner", () => {
    const v = buildLastMatchView({ reason: 2, winnerId: 0, rows });
    expect(v.winner).toBe("No single winner");
    expect(v.reason).toBe("All players eliminated");
  });
  test("unknown winner id falls back to Player <id>", () => {
    const v = buildLastMatchView({ reason: 1, winnerId: 99, rows });
    expect(v.winner).toBe("Player 99");
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `npm -C web test -- hud`
Expected: FAIL — `buildLastMatchView` is not exported / not a function.

- [ ] **Step 3: Write minimal implementation**

In `web/src/hud.ts`, after `buildEndDialog` (~line 146), add:

```ts
export interface LastMatchView {
  reason: string; // prose end reason
  winner: string; // winner display name, or "No single winner"
  rows: ScoreRow[]; // ordered desc score, asc id (from the EndDialog)
}

// buildLastMatchView turns a finished match's EndDialog into the lobby
// "Last Match" recap view-model. The winner name is resolved from the
// already-nick-joined rows (no separate nick map needed).
export function buildLastMatchView(d: EndDialog): LastMatchView {
  const winner =
    d.winnerId === 0
      ? "No single winner"
      : (d.rows.find((r) => r.id === d.winnerId)?.nick ?? `Player ${d.winnerId}`);
  return { reason: reasonText(d.reason), winner, rows: d.rows };
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `npm -C web test -- hud`
Expected: PASS (the new `buildLastMatchView` block + existing hud tests).

- [ ] **Step 5: Commit**

```bash
git add web/src/hud.ts web/tests/hud.test.ts
git commit -m "$(cat <<'EOF'
feat(hud): add buildLastMatchView recap view-model

Pure helper that maps a finished match's EndDialog to the lobby "Last Match"
card model (prose reason, winner resolved from rows, ordered score rows).
Reuses MatchOver data the client already decodes; no protocol change.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Phase 3 — Client: Command Deck visual rework + instructions + recap

> Phase 3 edits `web/src/browser.ts`, which is integration-tested by Playwright (not vitest). Verification per task is: `npm -C web run build` (TS compiles), `npm -C web test` stays green, and the e2e/golden + manual checks in Task 8. Preserve all existing `data-testid`s.

### Task 5: Inject scoped lobby styles

**Files:**
- Modify: `web/src/browser.ts` (add `injectLobbyStyles()` near `injectMatchStyles`, ~line 224; call it in `boot()` after `injectMatchStyles()`, ~line 752)

These styles target classes/ids the Task 6 restructure adds, so applying this task first is harmless (no matching elements yet) and lets Task 6 render themed immediately.

- [ ] **Step 1: Add the style injector**

In `web/src/browser.ts`, immediately after `injectMatchStyles()` (the function ending ~line 224), add:

```ts
// injectLobbyStyles installs the Command Deck theme once, scoped to #lobby
// (the match view has its own injected styles). The lobby stays in normal
// document flow (min-height shell) so document.body keeps a non-zero height —
// a position:fixed lobby would collapse body to 0px and break Playwright
// visibility / scene-ready markers.
function injectLobbyStyles(): void {
  if (document.getElementById("isnipes-lobby-style")) return;
  const style = document.createElement("style");
  style.id = "isnipes-lobby-style";
  style.textContent = `
body { margin: 0; background: #07070b; }
#lobby.lobby-shell {
  --panel: rgba(18,22,32,.72); --line: #234a66; --line-dim: #1d3247;
  --cyan: #4fd1ff; --cyan-bright: #8af0ff; --neon: #1aa0e6;
  --text: #cfe3ff; --text-dim: #86b9d8; --muted: #6f7a92;
  --lime: #8aff80; --yellow: #ffd166;
  position: relative; min-height: 100vh; box-sizing: border-box;
  margin: 0; padding: 18px 16px 28px;
  background: radial-gradient(1200px 600px at 50% -10%, #10131c 0%, #0a0a0f 60%);
  color: var(--text);
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
}
#lobby.lobby-shell.crt::after {
  content: ""; position: fixed; inset: 0; pointer-events: none; z-index: 50;
  background: repeating-linear-gradient(0deg, rgba(0,0,0,.16) 0, rgba(0,0,0,.16) 1px, transparent 1px, transparent 3px);
  opacity: .5;
}
#lobby .deck { max-width: 980px; margin: 0 auto; display: flex; flex-direction: column; gap: 12px; }
#lobby .deck-hdr { display: flex; align-items: flex-end; justify-content: space-between; gap: 12px; flex-wrap: wrap; border-bottom: 1px solid var(--line-dim); padding-bottom: 10px; }
#lobby .brand { margin: 0; font-weight: 800; letter-spacing: 5px; font-size: 30px; color: var(--cyan-bright); text-shadow: 0 0 10px var(--neon), 0 0 22px var(--neon); }
#lobby .tag { margin-top: 2px; font-size: 10px; letter-spacing: 3px; color: var(--muted); }
#lobby .ident { display: flex; align-items: center; gap: 8px; font-size: 12px; }
#lobby .conn { color: var(--muted); }
#lobby .conn.live { color: var(--lime); }
#lobby #nick { background: #0e1622; border: 1px solid var(--line); border-radius: 5px; color: var(--text); font: inherit; font-size: 12px; padding: 4px 8px; width: 14ch; }
#lobby .deck-cols { display: flex; gap: 12px; align-items: flex-start; }
#lobby .deck-right { display: flex; flex-direction: column; gap: 12px; flex: 1; min-width: 0; }
#lobby .play { flex: 1.15; min-width: 0; }
#lobby .panel { border: 1px solid var(--line); border-radius: 10px; background: var(--panel); padding: 12px 13px; }
#lobby .panel.glow { border-color: var(--neon); box-shadow: 0 0 14px rgba(26,160,230,.25) inset; }
#lobby .ph { margin: 0 0 8px; font-size: 10px; letter-spacing: 2px; text-transform: uppercase; color: #5fb0e6; display: flex; align-items: center; justify-content: space-between; }
#lobby button { font-family: inherit; cursor: pointer; }
#lobby #create-room, #lobby #start-btn { display: inline-block; margin-top: 2px; font-size: 13px; font-weight: 700; color: #04121c; background: linear-gradient(#7fe9ff, var(--neon)); border: none; border-radius: 6px; padding: 8px 14px; letter-spacing: .5px; box-shadow: 0 0 10px rgba(26,160,230,.5); }
#lobby #start-btn:disabled { filter: grayscale(.7) brightness(.7); cursor: not-allowed; box-shadow: none; }
#lobby [data-testid="join-room"], #lobby .ghost-btn { font-size: 11px; color: var(--cyan); background: transparent; border: 1px solid #2f6088; border-radius: 5px; padding: 2px 9px; }
#lobby [data-testid="join-room"]:disabled { color: var(--muted); border-color: #26384a; cursor: not-allowed; }
#lobby #my-room { margin-top: 10px; font-size: 13px; line-height: 1.7; }
#lobby #my-room-id { color: var(--cyan-bright); font-weight: 700; }
#lobby [data-testid="invite"] { font-size: 11px; color: var(--text-dim); margin-top: 4px; }
#lobby [data-testid="room-link"] { color: var(--cyan); word-break: break-all; }
#lobby [data-testid="start-hint"] { color: var(--muted); font-size: 11px; }
#lobby #level-picker { display: grid; grid-template-columns: repeat(9, 1fr); gap: 4px; margin-top: 6px; max-height: 140px; overflow-y: auto; padding-right: 4px; }
#lobby [data-testid="level-cell"] { font-size: 10px; text-align: center; padding: 5px 0; border: 1px solid #294f6c; border-radius: 4px; color: #9fd6f0; background: #0e1c28; }
#lobby [data-testid="level-cell"]:hover { border-color: var(--cyan); }
#lobby [data-testid="level-cell"].on { background: #15405a; border-color: var(--cyan); color: #d6f3ff; box-shadow: 0 0 7px var(--neon); }
#lobby [data-testid="level-preview"] { font-size: 11px; color: var(--text-dim); margin-top: 8px; border-top: 1px dashed #21384c; padding-top: 7px; }
#lobby #room-list { list-style: none; margin: 0; padding: 0; font-size: 12px; }
#lobby [data-testid="room-row"] { display: flex; align-items: center; justify-content: space-between; gap: 8px; padding: 4px 0; border-bottom: 1px solid var(--line-dim); }
#lobby [data-testid="no-rooms"] { color: var(--muted); font-style: italic; padding: 4px 0; }
#lobby [data-testid="last-match"] .winner { color: var(--yellow); font-weight: 700; }
#lobby [data-testid="last-match"] .reason { color: var(--text-dim); font-size: 11px; margin: 2px 0 6px; }
#lobby [data-testid="last-match"] .score-row { display: flex; justify-content: space-between; font-size: 12px; color: #bcd2e8; padding: 1px 0; }
#lobby .x-btn { background: transparent; border: none; color: var(--muted); font-size: 13px; }
#lobby .x-btn:hover { color: var(--cyan); }
#lobby .cheat { display: flex; gap: 16px; flex-wrap: wrap; align-items: center; font-size: 12px; color: #bcd2e8; }
#lobby .key { color: #04121c; background: #9fd6f0; border-radius: 3px; padding: 0 5px; font-weight: 700; font-size: 11px; }
#lobby [data-testid="howto-toggle"] { font-size: 10px; color: var(--cyan); background: transparent; border: 1px solid #2f6088; border-radius: 5px; padding: 1px 8px; letter-spacing: 1px; }
#lobby [data-testid="howto-full"] { margin-top: 10px; font-size: 12px; line-height: 1.6; color: var(--text-dim); border-top: 1px solid var(--line-dim); padding-top: 9px; }
#lobby [data-testid="howto-full"] h4 { margin: 10px 0 4px; color: #7fc6f0; font-size: 11px; letter-spacing: 1.5px; text-transform: uppercase; }
#lobby [data-testid="howto"] { margin: 4px 0 0; padding-left: 18px; }
#lobby .settings-details { border: 1px solid var(--line-dim); border-radius: 10px; background: rgba(14,18,26,.6); padding: 4px 12px; }
#lobby .settings-details > summary { cursor: pointer; font-size: 11px; letter-spacing: 2px; text-transform: uppercase; color: #5fb0e6; padding: 7px 0; }
#lobby .settings-details[open] > summary { border-bottom: 1px solid var(--line-dim); margin-bottom: 8px; }
#lobby [data-testid="settings"] h3 { display: none; }
#lobby [data-testid="settings"] label { display: inline-flex; align-items: center; gap: 6px; font-size: 12px; color: var(--text-dim); margin: 0 12px 8px 0; }
#lobby [data-testid="server-input"] { background: #0e1622; border: 1px solid var(--line); border-radius: 5px; color: var(--text); font: inherit; font-size: 12px; padding: 3px 7px; }
#lobby [data-testid="server-list"] { list-style: none; margin: 6px 0 0; padding: 0; font-size: 11px; }
#lobby [data-testid="server-select"] { background: transparent; border: none; color: var(--text-dim); }
@media (max-width: 720px) { #lobby .deck-cols { flex-direction: column; } }
`;
  document.head.append(style);
}
```

- [ ] **Step 2: Call it at boot**

In `boot()`, change `injectMatchStyles();` (~line 752) to:

```ts
  injectMatchStyles();
  injectLobbyStyles();
```

- [ ] **Step 3: Build to verify it compiles**

Run: `npm -C web run build`
Expected: build succeeds, writes `web/dist/app.js`.

- [ ] **Step 4: Commit**

```bash
git add web/src/browser.ts
git commit -m "$(cat <<'EOF'
feat(lobby): inject scoped Command Deck stylesheet

Adds injectLobbyStyles() (mirrors injectMatchStyles) with the neon/CRT theme
scoped to #lobby. Targets the structure the next task introduces; no behavior
change yet. Lobby stays in normal flow (min-height shell) to avoid body
zero-height collapse.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 6: Restructure the lobby DOM into the Command Deck (incl. instructions)

**Files:**
- Modify: `web/src/browser.ts` — `UI` interface (~58–91), `buildDOM()` (~93–188): add cheat-bar/full-guide helpers, move nick to header, wrap sections in panels, fold the how-to steps into the expandable guide, make settings collapsible.

- [ ] **Step 1: Add two helper builders**

In `web/src/browser.ts`, add above `buildDOM` (e.g. after the `text()` helper, ~line 198):

```ts
// buildCheatBar renders the always-visible How-to-Play strip (controls + scoring).
function buildCheatBar(): HTMLElement {
  const cheat = el("div", { class: "cheat", "data-testid": "howto-cheat" });
  const cap = (k: string, label: string): HTMLElement => {
    const s = el("span", {});
    s.append(el("span", { class: "key" }, k), text(" " + label));
    return s;
  };
  cheat.append(
    cap("↑↓←→", "move"), cap("W A S D", "fire"), cap("Space", "turbo"),
    cap("T", "chat"), cap("Tab", "scores"),
    el("span", { class: "score-line" }, "score  snipe +1 · gen +10 · kill +25 · death −5"),
  );
  return cheat;
}

// buildHowtoFull fills the expandable guide. startSteps is the existing
// "how to start a game" <ol> (data-testid="howto"), folded in as Getting started.
function buildHowtoFull(host: HTMLElement, startSteps: HTMLElement): void {
  const sec = (title: string, body: HTMLElement | string): void => {
    host.append(el("h4", {}, title));
    host.append(typeof body === "string" ? el("p", {}, body) : body);
  };
  sec("Objective",
    "Destroy every generator and snipe while at least one player survives — or be the last " +
    "player standing. If the 10-minute timer runs out, the highest score wins.");
  const controls = el("div", {});
  controls.append(
    el("div", {}, "Classic (default): move with arrows, fire with W/A/S/D, turbo = Space."),
    el("div", {}, "Modern: move with W/A/S/D, fire with arrows, turbo = Shift."),
    el("div", {}, "Vi-keys h/j/k/l always move. Chat: T (Enter sends, Esc cancels). Fullscreen: F."),
  );
  sec("Controls", controls);
  sec("Scoring", "Snipe +1 · Generator +10 · Player kill +25 · Your death −5.");
  sec("Lives & respawn",
    "Lives = 10 minus the level number (A1 = 9 lives, A9 = 1). Respawn takes 3s with 2s of spawn " +
    "invulnerability. At 0 lives you become a spectator and can still chat.");
  sec("Tips",
    "Generators are high value and keep spawning snipes — clear them first. You can't fire while " +
    "turboing. Higher letters (T–Z) are brutal: tougher generators, faster, smarter snipes.");
  host.append(el("h4", {}, "Getting started"), startSteps);
}
```

- [ ] **Step 2: Add new fields to the `UI` interface**

In the `UI` interface (browser.ts ~58–91), add these members (anywhere in the interface body):

```ts
  connStatus: HTMLElement;
  lastMatch: HTMLElement;
```

- [ ] **Step 3: Drop the nick from the settings panel**

In `buildDOM`, the `settingsPanel.append(...)` call (browser.ts:135–141) currently includes `labeled("Nick", nickInput)`. Remove that one argument so the block reads:

```ts
  settingsPanel.append(
    el("h3", {}, "Settings"),
    labeled("Preset", presetSelect),
    labeled("Color-blind", cbToggle), labeled("High contrast", hcToggle),
    labeled("Retro FX", rfxToggle),
    labeled("Volume", volSlider), labeled("Server", serverInput), serverAddBtn, serverList,
  );
```

- [ ] **Step 4: Make `howtoBox` an empty panel (its old contents move into the guide)**

Replace the `howtoBox` creation (browser.ts:150–151):

```ts
  const howtoBox = el("section", { "data-testid": "howto-box" });
  howtoBox.append(el("h2", {}, "How to start a game"), howto);
```

with just:

```ts
  const howtoBox = el("section", { class: "panel howto", "data-testid": "howto-box" });
```

(`howto` — the `<ol>` created just above at line 143 with its four `<li>` steps — stays as-is; it gets appended into the guide in Step 5.)

- [ ] **Step 5: Replace the lobby assembly block**

Replace the lobby creation/assembly (browser.ts:153–158):

```ts
  const lobby = el("section", { id: "lobby", "data-testid": "lobby", hidden: "true" });
  lobby.append(
    el("h1", {}, "Lobby"), howtoBox, createBtn, myRoom,
    el("h2", {}, "Levels"), picker, pickerPreview,
    el("h2", {}, "Rooms"), roomList, settingsPanel,
  );
```

with:

```ts
  // ---- header: brand + connection + nick ----
  const connStatus = el("span", { class: "conn", "data-testid": "conn-status" }, "● connecting…");
  const brandWrap = el("div", {});
  brandWrap.append(
    el("h1", { class: "brand" }, "ISNIPES"),
    el("div", { class: "tag" }, "MAZE · SNIPES · LAST ONE STANDING"),
  );
  const ident = el("div", { class: "ident" });
  ident.append(connStatus, el("span", {}, "nick"), nickInput);
  const header = el("header", { class: "deck-hdr", "data-testid": "lobby-header" });
  header.append(brandWrap, ident);

  // ---- left: play panel ----
  const playPanel = el("section", { class: "panel glow play", "data-testid": "play-panel" });
  playPanel.append(
    el("div", { class: "ph" }, "▶ Play"),
    createBtn, myRoom,
    el("div", { class: "ph" }, "Level"), picker, pickerPreview,
  );

  // ---- right: live games + last match ----
  const gamesPanel = el("section", { class: "panel games" });
  gamesPanel.append(el("div", { class: "ph" }, "Live Games"), roomList);
  const lastMatch = el("section", { class: "panel", "data-testid": "last-match", hidden: "true" });
  const deckRight = el("div", { class: "deck-right" });
  deckRight.append(gamesPanel, lastMatch);

  const deckCols = el("div", { class: "deck-cols" });
  deckCols.append(playPanel, deckRight);

  // ---- how to play: cheat bar + expandable guide ----
  const howtoToggle = el("button", { class: "ghost-btn", "data-testid": "howto-toggle" }, "More ▾") as HTMLButtonElement;
  const howtoFull = el("div", { "data-testid": "howto-full", hidden: "true" });
  buildHowtoFull(howtoFull, howto);
  const howtoHead = el("div", { class: "ph" });
  howtoHead.append(text("How to Play"), howtoToggle);
  howtoBox.append(howtoHead, buildCheatBar(), howtoFull);
  howtoToggle.onclick = () => {
    const opening = howtoFull.hidden;
    howtoFull.hidden = !opening;
    howtoToggle.textContent = opening ? "Less ▴" : "More ▾";
  };

  // ---- settings (collapsible) ----
  const settingsDetails = el("details", { class: "settings-details" });
  settingsDetails.append(el("summary", {}, "⚙ Settings"), settingsPanel);

  // ---- assemble ----
  const deck = el("div", { class: "deck" });
  deck.append(header, deckCols, howtoBox, settingsDetails);
  const lobby = el("section", { id: "lobby", class: "lobby-shell", "data-testid": "lobby", hidden: "true" });
  lobby.append(deck);
```

- [ ] **Step 6: Register the new UI fields**

In the `const ui: UI = { ... }` object literal (browser.ts ~177–182), add `connStatus` and `lastMatch` to the listed properties, e.g. append them to the object:

```ts
  const ui: UI = {
    connecting, lobby, createBtn, nickInput, myRoom, myRoomId, myRoomPlayers, myRoomLink,
    startBtn, startHint, roomList, picker, pickerPreview, serverInput, serverList, cbToggle,
    hcToggle, rfxToggle, volSlider, presetSelect, match, matchView, canvas, minimap, stats, scoreboard,
    chatBox, chatInput, endDialog, backBtn, status, respawnOverlay, connStatus, lastMatch,
  };
```

- [ ] **Step 7: Build to verify it compiles**

Run: `npm -C web run build`
Expected: build succeeds. (If TS complains about an unused/missing `UI` field, ensure both `connStatus` and `lastMatch` are in the interface and the object.)

- [ ] **Step 8: Verify e2e testids still work (the critical flow)**

Run: `npm -C web run test:e2e -- deeplink play` (locally: add `--config=playwright.local.config.ts`).
Expected: `deeplink.spec.ts` and `play.spec.ts` PASS (lobby visible, create-room/my-room/my-room-id/room-players/start-btn all found and clickable). The `golden.spec.ts` will fail here — expected, re-baselined in Task 8.

- [ ] **Step 9: Commit**

```bash
git add web/src/browser.ts
git commit -m "$(cat <<'EOF'
feat(lobby): Command Deck layout + built-in How-to-Play

Restructure buildDOM into a neon header (brand + connection + nick), a Play
panel, a Live Games + Last Match right column, a How-to-Play panel (always-on
cheat bar + expandable full guide folding in the old start steps), and a
collapsible Settings panel. All existing data-testids preserved; nick moves to
the header. Goldens re-baselined separately.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 7: Last Match recap card + connection/scanline wiring

**Files:**
- Modify: `web/src/browser.ts` — import from hud.js; `MatchRunner.lastResult()`; `renderLastMatch()`; `boot()` handlers (`onWelcome`, `backBtn.onclick`, `onMatchStarted`, retro-fx class); `applySettingsHandlers` (retro-fx class).

- [ ] **Step 1: Extend the hud import**

In `web/src/browser.ts`, the import from `"./hud.js"` (lines 24–27) lists hud helpers. Add `buildLastMatchView`, `type LastMatchView`, and `type EndDialog`:

```ts
import {
  decodeScoreboard, decodeMatchOver, buildEndDialog, appendChat, plotMinimap,
  reasonText, winnerLabel, buildLastMatchView,
  type HudModel, emptyHudModel, type ChatLine, type LastMatchView, type EndDialog,
} from "./hud.js";
```

- [ ] **Step 2: Expose the last result on MatchRunner**

In `class MatchRunner`, add a method (e.g. right after `stop()`, ~line 652):

```ts
  // lastResult returns the finished match's end dialog (winner + final scores),
  // or null if the match never produced a MatchOver. Used to render the lobby
  // "Last Match" recap on return.
  lastResult(): EndDialog | null {
    return this.hud.endDialog;
  }
```

- [ ] **Step 3: Add the recap renderer**

Add a module-level function (e.g. after `renderPreview`, ~line 340):

```ts
// renderLastMatch shows/hides the lobby "Last Match" recap card. Pass null to
// clear it (dismissed, or when a new match starts).
function renderLastMatch(ui: UI, view: LastMatchView | null): void {
  if (!view) {
    ui.lastMatch.hidden = true;
    ui.lastMatch.innerHTML = "";
    return;
  }
  ui.lastMatch.innerHTML = "";
  const head = el("div", { class: "ph" });
  const dismiss = el("button", { class: "x-btn", "data-testid": "last-match-dismiss", title: "Dismiss" }, "✕") as HTMLButtonElement;
  dismiss.onclick = () => renderLastMatch(ui, null);
  head.append(text("Last Match"), dismiss);
  ui.lastMatch.append(
    head,
    el("div", { class: "winner", "data-testid": "last-match-winner" }, `🏆 ${view.winner}`),
    el("div", { class: "reason" }, view.reason),
  );
  for (const r of view.rows) {
    const row = el("div", { class: "score-row", "data-testid": "last-match-row" });
    row.append(el("span", {}, r.nick), el("span", {}, `${r.score}  (${r.lives}♥)`));
    ui.lastMatch.append(row);
  }
  ui.lastMatch.hidden = false;
}
```

- [ ] **Step 4: Wire connection status + recap into boot()**

In `boot()`:

(a) `onWelcome` (line 812) — mark the header connected:
```ts
  app.onWelcome = () => {
    ui.connecting.hidden = true; ui.lobby.hidden = false;
    ui.connStatus.textContent = "● connected"; ui.connStatus.classList.add("live");
  };
```

(b) `backBtn.onclick` (lines 806–810) — capture the result before stopping, then render the card:
```ts
  ui.backBtn.onclick = () => {
    const res = runner?.lastResult() ?? null;
    runner?.stop(); runner = null;
    app.backToLobby();
    ui.match.hidden = true; ui.lobby.hidden = false; ui.endDialog.hidden = true;
    renderLastMatch(ui, res ? buildLastMatchView(res) : null);
  };
```

(c) `onMatchStarted` (lines 838–842) — clear any stale recap when a new match begins:
```ts
  app.onMatchStarted = (ms: MatchStarted) => {
    ui.lobby.hidden = true; ui.match.hidden = false; ui.endDialog.hidden = true;
    renderLastMatch(ui, null);
    runner = new MatchRunner(ui, settings, lobbyOrigin, () => app.endMatch());
    runner.connect(ms, schemaChecksum);
  };
```

(d) Apply the initial Retro-FX scanline class. After `const ui = buildDOM(settings);` (line 753) add:
```ts
  ui.lobby.classList.toggle("crt", settings.retroFx);
```

- [ ] **Step 5: Keep the scanline class in sync with the Retro FX toggle**

In `applySettingsHandlers`, the `ui.rfxToggle.onchange` handler (lines 858–862) — add the lobby class toggle:
```ts
  ui.rfxToggle.onchange = () => {
    settings.retroFx = ui.rfxToggle.checked;
    saveSettings(settings);
    getRunner()?.setRetroFx(settings.retroFx);
    ui.lobby.classList.toggle("crt", settings.retroFx);
  };
```

- [ ] **Step 6: Build + unit tests**

Run: `npm -C web run build && npm -C web test`
Expected: build succeeds; vitest all green (no browser.ts unit tests, hud tests still pass).

- [ ] **Step 7: Manual check**

Rebuild + run the server (`npm -C web run build` then `go run ./cmd/isnipes --web-dist web/dist --insecure-origin --addr :8080`). In two windows, play a 2-player match to its end, click "Back to lobby" in one, and confirm the **Last Match** card appears (winner + score rows), the ✕ dismisses it, and starting a new match clears it. Toggle Retro FX in Settings and confirm lobby scanlines turn on/off.

- [ ] **Step 8: Commit**

```bash
git add web/src/browser.ts
git commit -m "$(cat <<'EOF'
feat(lobby): Last Match recap card + connection/scanline wiring

On return from a match, render a dismissible "Last Match" card (winner + final
scoreboard) from the MatchOver the client already decoded; clear it when a new
match starts. Mark the header connected on welcome and gate lobby CRT scanlines
on the Retro FX setting.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 8: Re-baseline goldens + full verification

**Files:**
- Modify (regenerate): `web/tests/e2e/golden.spec.ts-snapshots/empty-lobby.png`, `full-lobby.png`
- Possibly modify: `web/src/scenes.ts` (only if the lobby golden scene fails to render under the new structure)

Context: the lobby visual changed, so the two lobby goldens must be re-baselined. Running Playwright locally needs the gitignored `web/playwright.local.config.ts` chrome override (NixOS). The Last Match card is hidden in the golden lobby scenes (no prior match), so `scenes.ts` should need no change.

- [ ] **Step 1: Confirm the full e2e suite, expecting only goldens to differ**

Run: `npm -C web run test:e2e` (locally `--config=playwright.local.config.ts`).
Expected: `deeplink`, `play`, `scene_guard` PASS; `golden.spec.ts` FAILS on `empty-lobby` / `full-lobby` only (visual diff). If a lobby golden scene errors (not just diffs), inspect `renderLobbyScene` (scenes.ts:690–697) and adjust it minimally so the new lobby structure renders deterministically; rebuild and re-run.

- [ ] **Step 2: Regenerate the lobby baselines**

Run: `npm -C web run test:e2e -- --update-snapshots` (locally `npx playwright test --config=playwright.local.config.ts --update-snapshots`).
Expected: `empty-lobby.png` and `full-lobby.png` rewritten; suite green.

- [ ] **Step 3: Re-run e2e clean to confirm green**

Run: `npm -C web run test:e2e` (locally with the local config).
Expected: all specs PASS against the new baselines.

- [ ] **Step 4: Full guard + suite sweep**

Run:
```bash
make check-frozen
go test ./internal/lobby/... ./internal/match/...
npm -C web test
```
Expected: frozen guard passes (no frozen files changed); Go lobby/match tests PASS; vitest PASS.

- [ ] **Step 5: Commit**

```bash
git add web/tests/e2e/golden.spec.ts-snapshots/empty-lobby.png web/tests/e2e/golden.spec.ts-snapshots/full-lobby.png web/src/scenes.ts
git commit -m "$(cat <<'EOF'
test(lobby): re-baseline lobby goldens for the Command Deck redesign

empty-lobby / full-lobby snapshots updated to the new neon layout. deeplink /
play / scene_guard unchanged; frozen guard clean.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
)"
```

(If `scenes.ts` was not modified in Step 1, drop it from the `git add`.)

---

## Self-review checklist (completed by plan author)

**Spec coverage:**
- Visual rework (Command Deck, neon theme, scoped styles, testid preservation, zero-height guard) → Tasks 5, 6.
- Instructions (cheat bar + expandable guide, accurate content) → Task 6 (Steps 1, 5).
- Finished games removed (server `MatchEnded` poster + `OnMatchEnded` hook + wiring) → Tasks 1, 2, 3.
- "Last Match" recap (pure view-model + card render, no protocol change) → Tasks 4, 7.
- Testing/goldens/frozen guard → Task 8.
- Out of scope (global results board, spectator scroll) → intentionally not implemented.

**Placeholder scan:** No TBD/TODO; all code blocks complete; commands have expected output.

**Type/name consistency:** `MatchEnded(matchID string)`, `OnMatchEnded func(matchID string)`, `buildLastMatchView(d: EndDialog): LastMatchView`, `LastMatchView{reason,winner,rows}`, `renderLastMatch(ui, view)`, `lastResult(): EndDialog | null`, UI fields `connStatus`/`lastMatch`, classes `lobby-shell`/`crt`/`deck`/`panel`/`cheat`, testids `conn-status`/`howto-toggle`/`howto-full`/`howto-cheat`/`last-match`/`last-match-dismiss`/`last-match-winner`/`last-match-row` — consistent across tasks.
