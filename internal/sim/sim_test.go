package sim

import (
	"encoding/binary"
	"encoding/hex"
	"errors"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestDeterminism_SameSeedSameInputs(t *testing.T) {
	cfg := Config{
		Seed:      0xCAFEBABE,
		Width:     60,
		Height:    40,
		PlayerIDs: []EntityID{1, 2, 3, 4},
	}
	s1, err := NewSim(cfg)
	if err != nil {
		t.Fatalf("s1: %v", err)
	}
	s2, err := NewSim(cfg)
	if err != nil {
		t.Fatalf("s2: %v", err)
	}
	// Scripted input list: 1000 ticks of cycling input.
	for i := 0; i < 1000; i++ {
		inputs := []PlayerInput{
			{PlayerID: 1, Dir: Dir((i % 8) + 1), Turbo: i%5 == 0, FireDir: Dir((i % 7) + 1), ClientTick: uint16(i)},
			{PlayerID: 2, Dir: Dir(((i + 2) % 8) + 1), ClientTick: uint16(i)},
		}
		ev1, err1 := s1.Tick(inputs)
		ev2, err2 := s2.Tick(inputs)
		if (err1 == nil) != (err2 == nil) {
			t.Fatalf("tick %d errs differ", i)
		}
		if !reflect.DeepEqual(ev1, ev2) {
			t.Fatalf("tick %d events differ: %v vs %v", i, ev1, ev2)
		}
		if s1.Fingerprint() != s2.Fingerprint() {
			t.Fatalf("tick %d fingerprints differ", i)
		}
	}
}

func TestDeterminism_GoldenFingerprint(t *testing.T) {
	cfg := Config{
		Seed:      0x42424242,
		Width:     60,
		Height:    40,
		PlayerIDs: []EntityID{1, 2, 3, 4},
	}
	s, err := NewSim(cfg)
	if err != nil {
		t.Fatalf("NewSim: %v", err)
	}
	inputsPath := filepath.Join("testdata", "replays", "baseline.inputs")
	hashPath := filepath.Join("testdata", "replays", "baseline.hash")

	if *updateGolden {
		// Generate inputs and write.
		inputs := generateBaselineInputs()
		if err := writeReplayInputs(inputsPath, inputs); err != nil {
			t.Fatalf("write inputs: %v", err)
		}
		for _, tick := range inputs {
			if _, err := s.Tick(tick); err != nil {
				t.Fatalf("tick err: %v", err)
			}
		}
		fp := s.Fingerprint()
		if err := os.WriteFile(hashPath, []byte(hex.EncodeToString(fp[:])+"\n"), 0o644); err != nil {
			t.Fatalf("write hash: %v", err)
		}
		return
	}

	inputs, err := readReplayInputs(inputsPath)
	if err != nil {
		t.Fatalf("read inputs: %v (run with -update)", err)
	}
	for i, tick := range inputs {
		if _, err := s.Tick(tick); err != nil {
			t.Fatalf("tick %d: %v", i, err)
		}
	}
	wantHex, err := os.ReadFile(hashPath)
	if err != nil {
		t.Fatalf("read hash: %v", err)
	}
	want := string(wantHex)
	if len(want) > 0 && want[len(want)-1] == '\n' {
		want = want[:len(want)-1]
	}
	fp := s.Fingerprint()
	got := hex.EncodeToString(fp[:])
	if want != got {
		t.Fatalf("fingerprint: want %s, got %s", want, got)
	}
}

// TestDeterminism_GoldenFingerprint_Level9 is the Phase 3 PvE golden
// replay (§16.7). Single-player level T9 (Brutal + max scaling),
// scripted inputs for 1200 ticks. Hash committed to
// testdata/replays/phase3_pve.hash.
func TestDeterminism_GoldenFingerprint_Level9(t *testing.T) {
	cfg := Config{
		Seed:        0x42424242,
		Width:       60,
		Height:      40,
		PlayerIDs:   []EntityID{1},
		LevelLetter: 'T',
		LevelNumber: 9,
	}
	s, err := NewSim(cfg)
	if err != nil {
		t.Fatalf("NewSim: %v", err)
	}
	inputsPath := filepath.Join("testdata", "replays", "phase3_pve.inputs")
	hashPath := filepath.Join("testdata", "replays", "phase3_pve.hash")

	if *updateGolden {
		inputs := generatePhase3Inputs()
		if err := writeReplayInputs(inputsPath, inputs); err != nil {
			t.Fatalf("write inputs: %v", err)
		}
		for _, tick := range inputs {
			if _, err := s.Tick(tick); err != nil {
				t.Fatalf("tick err: %v", err)
			}
		}
		fp := s.Fingerprint()
		if err := os.WriteFile(hashPath, []byte(hex.EncodeToString(fp[:])+"\n"), 0o644); err != nil {
			t.Fatalf("write hash: %v", err)
		}
		return
	}

	inputs, err := readReplayInputs(inputsPath)
	if err != nil {
		t.Fatalf("read inputs: %v (run with -update)", err)
	}
	for i, tick := range inputs {
		if _, err := s.Tick(tick); err != nil {
			t.Fatalf("tick %d: %v", i, err)
		}
	}
	wantHex, err := os.ReadFile(hashPath)
	if err != nil {
		t.Fatalf("read hash: %v", err)
	}
	want := string(wantHex)
	if len(want) > 0 && want[len(want)-1] == '\n' {
		want = want[:len(want)-1]
	}
	fp := s.Fingerprint()
	got := hex.EncodeToString(fp[:])
	if want != got {
		t.Fatalf("fingerprint: want %s, got %s", want, got)
	}
}

// phase5PvPSeed and phase5PvPConfig pin the Phase 5 §19 4-player PvP
// fixture: a non-PvE map (NoGenerators ⇒ no snipes) at level A1
// (PlayerLives = 9). The seed was chosen so the recorded "hunt p4"
// playthrough drives player 4 to zero lives (→ dead-cam) while the
// other three survive — exercising kills, respawns, the spawn-invuln
// window, and elimination.
const phase5PvPSeed uint32 = 0xDEADBEEF

func phase5PvPConfig() Config {
	return Config{
		Seed:         phase5PvPSeed,
		Width:        60,
		Height:       40,
		PlayerIDs:    []EntityID{1, 2, 3, 4},
		LevelLetter:  'A',
		LevelNumber:  1,
		NoGenerators: true,
	}
}

// TestDeterminism_Phase5_4PPvP is the Phase 5 DoD #5 golden replay. The
// committed phase5_4p_pvp.inputs is replayed against a fresh sim and the
// final fingerprint must match phase5_4p_pvp.hash byte-for-byte. Run
// with -update to regenerate both (the input stream is produced by a
// BFS-driven recorded playthrough; see generatePhase5Inputs).
func TestDeterminism_Phase5_4PPvP(t *testing.T) {
	s, err := NewSim(phase5PvPConfig())
	if err != nil {
		t.Fatalf("NewSim: %v", err)
	}
	inputsPath := filepath.Join("testdata", "replays", "phase5_4p_pvp.inputs")
	hashPath := filepath.Join("testdata", "replays", "phase5_4p_pvp.hash")

	if *updateGolden {
		inputs := generatePhase5Inputs(t)
		if err := writeReplayInputs(inputsPath, inputs); err != nil {
			t.Fatalf("write inputs: %v", err)
		}
		for _, tick := range inputs {
			if _, err := s.Tick(tick); err != nil {
				t.Fatalf("tick err: %v", err)
			}
		}
		fp := s.Fingerprint()
		if err := os.WriteFile(hashPath, []byte(hex.EncodeToString(fp[:])+"\n"), 0o644); err != nil {
			t.Fatalf("write hash: %v", err)
		}
		return
	}

	inputs, err := readReplayInputs(inputsPath)
	if err != nil {
		t.Fatalf("read inputs: %v (run with -update)", err)
	}
	for i, tick := range inputs {
		if _, err := s.Tick(tick); err != nil {
			t.Fatalf("tick %d: %v", i, err)
		}
	}
	wantHex, err := os.ReadFile(hashPath)
	if err != nil {
		t.Fatalf("read hash: %v", err)
	}
	want := string(wantHex)
	if len(want) > 0 && want[len(want)-1] == '\n' {
		want = want[:len(want)-1]
	}
	fp := s.Fingerprint()
	got := hex.EncodeToString(fp[:])
	if want != got {
		t.Fatalf("fingerprint: want %s, got %s", want, got)
	}
}

// phase5FixtureTicks is the length of the recorded PvP fixture. Longer
// than the §19 "~2000" estimate because driving one player through 9
// real PvP deaths (with 90-tick respawn + 60-tick spawn-invuln per
// cycle, plus walk-back time) needs the extra budget. See the spec-issue
// note in the phase loop scratchpad.
const phase5FixtureTicks = 5000

// generatePhase5Inputs records a deterministic 4-player PvP playthrough.
// Players 1–3 path (BFS, center-then-turn steering) to a fixed central
// rendezvous and fire at player 4; player 4 walks to the rendezvous and
// holds. The recorded input stream is static — replaying it on a fresh
// sim with the same seed reproduces identical state, so the fixture is
// byte-for-byte deterministic. Only invoked under -update.
func generatePhase5Inputs(t *testing.T) [][]PlayerInput {
	t.Helper()
	s, err := NewSim(phase5PvPConfig())
	if err != nil {
		t.Fatalf("NewSim: %v", err)
	}
	out := make([][]PlayerInput, phase5FixtureTicks)
	for i := 0; i < phase5FixtureTicks; i++ {
		out[i] = phase5TickInputs(s, i)
		if _, err := s.Tick(out[i]); err != nil {
			t.Fatalf("record tick %d: %v", i, err)
		}
	}
	return out
}

// phase5TickInputs computes one tick of the recorded playthrough from
// the live sim state. Deterministic: it only reads entity positions and
// the (immutable) maze, and iterates a fixed player-ID order.
func phase5TickInputs(s *Sim, i int) []PlayerInput {
	ids := [4]EntityID{1, 2, 3, 4}
	pos := make(map[EntityID]Entity, 4)
	for _, e := range s.Entities() {
		if e.Kind == KindPlayer && e.Flags&FlagDead == 0 {
			pos[e.ID] = e
		}
	}
	rx, ry := phase5Rendezvous(s)
	victim, haveVictim := pos[4]
	out := make([]PlayerInput, 0, 4)
	for j, id := range ids {
		e, alive := pos[id]
		if !alive {
			continue
		}
		tx, ty := int(e.X)/subtilePerTile, int(e.Y)/subtilePerTile
		dist := absInt(tx-rx) + absInt(ty-ry)
		var dir, fire Dir
		if dist > 1 {
			if nx, ny, ok := PathNext(s.maze, tx, ty, rx, ry, 4096); ok {
				dir = steerAlongPath(e.X, e.Y, tx, ty, nx, ny)
			}
		}
		if id == 4 {
			out = append(out, PlayerInput{PlayerID: id, Dir: dir, ClientTick: uint16(i)})
			continue
		}
		if haveVictim {
			fire = dir8Toward(e.X, e.Y, victim.X, victim.Y)
		} else {
			fire = Dir(((i + j*2) % 8) + 1)
		}
		out = append(out, PlayerInput{PlayerID: id, Dir: dir, FireDir: fire, ClientTick: uint16(i)})
	}
	return out
}

// phase5Rendezvous returns the walkable tile nearest the map centre.
func phase5Rendezvous(s *Sim) (int, int) {
	cx, cy := s.Width()/2, s.Height()/2
	for r := 0; r < s.Width()+s.Height(); r++ {
		for dy := -r; dy <= r; dy++ {
			for dx := -r; dx <= r; dx++ {
				x, y := cx+dx, cy+dy
				if x <= 0 || y <= 0 || x >= s.Width()-1 || y >= s.Height()-1 {
					continue
				}
				if s.maze.at(x, y) != TileWall {
					return x, y
				}
			}
		}
	}
	return cx, cy
}

// steerAlongPath converts a BFS next-tile into a CARDINAL movement Dir toward
// that tile's centre (dominant axis first). MAZE_REVAMP.md widened corridors to
// corridorWidth tiles but left 1-tile wall pillars at 4-cell junctions;
// cardinal-only motion keeps the 2-tile player off those pillars (diagonal
// motion clips them and stalls). The current tile (tx, ty) is unused now.
func steerAlongPath(x, y int32, tx, ty, nx, ny int) Dir {
	_, _ = tx, ty
	dx := int32(nx*subtilePerTile+subtilePerTile/2) - x
	dy := int32(ny*subtilePerTile+subtilePerTile/2) - y
	adx, ady := dx, dy
	if adx < 0 {
		adx = -adx
	}
	if ady < 0 {
		ady = -ady
	}
	if adx >= ady {
		if dx > 0 {
			return DirE
		}
		if dx < 0 {
			return DirW
		}
	}
	if dy > 0 {
		return DirS
	}
	if dy < 0 {
		return DirN
	}
	return DirIdle
}

func absInt(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// phase5LSSeed pins the LAST_STANDING fixture. It is a 4-player free-
// for-all (Config.LevelLetter == 0 ⇒ defaultPvPLives = 3 lives each, no
// snipes, no generators). Player 1 is a "hunter" that chases and kills
// the other three (who hold their spawn positions) until each is
// eliminated, leaving player 1 as the sole survivor — the natural
// LAST_STANDING end the match-actor integration test asserts on.
const phase5LSSeed uint32 = 0xDEADBEEF

func phase5LSConfig() Config {
	return Config{
		Seed:      phase5LSSeed,
		Width:     60,
		Height:    40,
		PlayerIDs: []EntityID{1, 2, 3, 4},
		// LevelLetter == 0 ⇒ free-for-all: 3 lives, no snipes. NoGenerators
		// mirrors what startOrAbort sets for a zero-level PvP match in
		// production (match.go), so the fixture exercises the real PvP map.
		NoGenerators: true,
	}
}

// phase5LSMaxTicks caps the recorder. Driving three victims through
// three deaths each (9 kills) needs a generous budget because the
// respawn picker (§10.5) places each victim on the spawn tile *farthest*
// from the lone hunter, so the hunter has to walk back across the map
// after every kill.
const phase5LSMaxTicks = 20000

// TestDeterminism_Phase5_LastStanding records / replays the LAST_STANDING
// fixture. Under -update it regenerates phase5_last_standing.{inputs,hash}
// from the BFS-driven hunt; otherwise it replays the committed inputs and
// asserts both the byte-for-byte fingerprint and the scenario invariant
// (players 2, 3, 4 eliminated; player 1 survives). The match-actor side
// (internal/match) replays the same .inputs to verify the natural
// LAST_STANDING MatchOver.
func TestDeterminism_Phase5_LastStanding(t *testing.T) {
	s, err := NewSim(phase5LSConfig())
	if err != nil {
		t.Fatalf("NewSim: %v", err)
	}
	inputsPath := filepath.Join("testdata", "replays", "phase5_last_standing.inputs")
	hashPath := filepath.Join("testdata", "replays", "phase5_last_standing.hash")

	if *updateGolden {
		inputs := generatePhase5LSInputs(t)
		if err := writeReplayInputs(inputsPath, inputs); err != nil {
			t.Fatalf("write inputs: %v", err)
		}
		for _, tick := range inputs {
			if _, err := s.Tick(tick); err != nil {
				t.Fatalf("tick err: %v", err)
			}
		}
		fp := s.Fingerprint()
		if err := os.WriteFile(hashPath, []byte(hex.EncodeToString(fp[:])+"\n"), 0o644); err != nil {
			t.Fatalf("write hash: %v", err)
		}
		t.Logf("recorded %d ticks; eliminated 2=%v 3=%v 4=%v survivor1=%v",
			len(inputs), s.Eliminated(2), s.Eliminated(3), s.Eliminated(4), !s.Eliminated(1))
		return
	}

	inputs, err := readReplayInputs(inputsPath)
	if err != nil {
		t.Fatalf("read inputs: %v (run with -update)", err)
	}
	for i, tick := range inputs {
		if _, err := s.Tick(tick); err != nil {
			t.Fatalf("tick %d: %v", i, err)
		}
	}
	wantHex, err := os.ReadFile(hashPath)
	if err != nil {
		t.Fatalf("read hash: %v", err)
	}
	want := string(wantHex)
	if len(want) > 0 && want[len(want)-1] == '\n' {
		want = want[:len(want)-1]
	}
	fp := s.Fingerprint()
	if got := hex.EncodeToString(fp[:]); want != got {
		t.Fatalf("fingerprint: want %s, got %s", want, got)
	}
	// Scenario invariant: the hunt leaves exactly one survivor.
	for _, id := range []EntityID{2, 3, 4} {
		if !s.Eliminated(id) {
			t.Errorf("victim %d not eliminated; lives=%d", id, s.LivesRemaining(id))
		}
	}
	if s.Eliminated(1) {
		t.Error("hunter (player 1) was eliminated; fixture no longer yields a LAST_STANDING survivor")
	}
}

// generatePhase5LSInputs records the hunter-vs-three-idle-victims
// playthrough and returns the input stream truncated a few ticks past
// the third elimination. Victims emit no input (a player absent from a
// tick's input set holds position — sim.go step 3), so only the hunter's
// per-tick input is recorded, keeping the fixture compact. Only invoked
// under -update.
func generatePhase5LSInputs(t *testing.T) [][]PlayerInput {
	t.Helper()
	s, err := NewSim(phase5LSConfig())
	if err != nil {
		t.Fatalf("NewSim: %v", err)
	}
	out := make([][]PlayerInput, 0, 6000)
	trailing := 0
	for i := 0; i < phase5LSMaxTicks; i++ {
		in := phase5LSTickInputs(s, i)
		out = append(out, in)
		if _, err := s.Tick(in); err != nil {
			t.Fatalf("record tick %d: %v", i, err)
		}
		if s.Eliminated(2) && s.Eliminated(3) && s.Eliminated(4) {
			// A few trailing ticks so the replay reaches the same terminal
			// state cleanly (and the match-actor end-eval has a tick to fire).
			trailing++
			if trailing >= 3 {
				break
			}
		}
	}
	if !(s.Eliminated(2) && s.Eliminated(3) && s.Eliminated(4)) {
		t.Fatalf("recorder did not eliminate all three victims within %d ticks", phase5LSMaxTicks)
	}
	return out
}

// cellOfTile returns the maze cell containing an arbitrary tile.
func cellOfTile(tx, ty int) cellPos {
	return cellPos{(tx - 1) / mazePitch, (ty - 1) / mazePitch}
}

// nextCell BFS's the cell graph (open links) from `from` toward `to` and
// returns the next cell to move to. Cell-to-cell motion through the
// corridorWidth-wide openings is collision-safe for the 2-tile player, unlike
// greedy tile-following past the 1-tile junction pillars.
func nextCell(m *maze, from, to cellPos, cellsX, cellsY int) (cellPos, bool) {
	if from == to {
		return to, true
	}
	parent := map[cellPos]cellPos{from: from}
	q := []cellPos{from}
	for len(q) > 0 {
		cur := q[0]
		q = q[1:]
		if cur == to {
			break
		}
		for _, d := range cellDirs {
			n := cellPos{cur.CX + d.CX, cur.CY + d.CY}
			if n.CX < 0 || n.CY < 0 || n.CX >= cellsX || n.CY >= cellsY {
				continue
			}
			if _, seen := parent[n]; seen {
				continue
			}
			if !linked(m, cur, n) {
				continue
			}
			parent[n] = cur
			q = append(q, n)
		}
	}
	if _, ok := parent[to]; !ok {
		return cellPos{}, false
	}
	step := to
	for parent[step] != from {
		step = parent[step]
	}
	return step, true
}

// cellCenterSub returns a cell's centre in subtile coordinates.
func cellCenterSub(c cellPos) (int32, int32) {
	ct := cellCenter(c)
	return int32(ct.X*subtilePerTile + subtilePerTile/2), int32(ct.Y*subtilePerTile + subtilePerTile/2)
}

// steerToCell moves the hunter from cell hc toward adjacent cell nc, aligning
// on the axis perpendicular to the link FIRST (so the 2-tile body fits through
// the corridorWidth-wide opening) before stepping across. Both cell centres
// share the perpendicular coordinate, so aligning to hc's centre also aligns to
// the opening.
func steerToCell(hx, hy int32, hc, nc cellPos) Dir {
	const tol = subtilePerTile / 2 // ≤ (corridorWidth-2)/2 tiles of slack
	ccx, ccy := cellCenterSub(hc)
	ncx, ncy := cellCenterSub(nc)
	if nc.CX != hc.CX { // horizontal link → align Y, then move E/W
		if hy-ccy > tol {
			return DirN
		}
		if ccy-hy > tol {
			return DirS
		}
		if ncx > hx {
			return DirE
		}
		return DirW
	}
	// vertical link → align X, then move N/S
	if hx-ccx > tol {
		return DirW
	}
	if ccx-hx > tol {
		return DirE
	}
	if ncy > hy {
		return DirS
	}
	return DirN
}

// phase5LSTickInputs computes one tick of the hunt: player 1 navigates
// cell-to-cell toward the nearest living victim and, once in the victim's cell,
// aligns on the cross axis and fires down the shared row/column. Victims sit at
// cell centres (spawn / respawn). Deterministic — reads only entity positions
// and the immutable maze, with a fixed victim-ID order for tie-breaking.
func phase5LSTickInputs(s *Sim, i int) []PlayerInput {
	pos := make(map[EntityID]Entity, 4)
	for _, e := range s.Entities() {
		if e.Kind == KindPlayer && e.Flags&FlagDead == 0 {
			pos[e.ID] = e
		}
	}
	hunter, alive := pos[1]
	if !alive {
		return nil
	}
	hx, hy := int(hunter.X)/subtilePerTile, int(hunter.Y)/subtilePerTile
	var target Entity
	bestDist := 1 << 30
	haveTarget := false
	for _, id := range []EntityID{2, 3, 4} {
		v, ok := pos[id]
		if !ok {
			continue
		}
		vx, vy := int(v.X)/subtilePerTile, int(v.Y)/subtilePerTile
		if d := absInt(hx-vx) + absInt(hy-vy); d < bestDist {
			bestDist, target, haveTarget = d, v, true
		}
	}
	if !haveTarget {
		return []PlayerInput{{PlayerID: 1, ClientTick: uint16(i)}}
	}
	tx, ty := int(target.X)/subtilePerTile, int(target.Y)/subtilePerTile
	cellsX := (s.maze.W - 1) / mazePitch
	cellsY := (s.maze.H - 1) / mazePitch
	hc := cellOfTile(hx, hy)
	vc := cellOfTile(tx, ty)
	var dir, fire Dir
	if hc == vc {
		// Same room: align on the axis of smaller separation and fire down the
		// other. fireAlign < snipeHalfExt/playerHalfExt so the shot crosses the
		// target AABB.
		const fireAlign = 64
		dx := int(target.X) - int(hunter.X)
		dy := int(target.Y) - int(hunter.Y)
		if absInt(dx) >= absInt(dy) { // horizontal shot: align Y, fire E/W
			switch {
			case dy > fireAlign:
				dir = DirS
			case dy < -fireAlign:
				dir = DirN
			case dx > 0:
				fire = DirE
			default:
				fire = DirW
			}
		} else { // vertical shot: align X, fire N/S
			switch {
			case dx > fireAlign:
				dir = DirE
			case dx < -fireAlign:
				dir = DirW
			case dy > 0:
				fire = DirS
			default:
				fire = DirN
			}
		}
	} else if nc, ok := nextCell(s.maze, hc, vc, cellsX, cellsY); ok {
		dir = steerToCell(hunter.X, hunter.Y, hc, nc)
	}
	return []PlayerInput{{PlayerID: 1, Dir: dir, FireDir: fire, ClientTick: uint16(i)}}
}

// TestDeterminism_SnipeAI satisfies DoD #6: two sims with identical
// Config (incl. LevelLetter/Number) and identical inputs evolve to
// identical fingerprints at every tick.
func TestDeterminism_SnipeAI(t *testing.T) {
	cfg := Config{
		Seed:        0xC0FFEE,
		Width:       60,
		Height:      40,
		PlayerIDs:   []EntityID{1},
		LevelLetter: 'C',
		LevelNumber: 5,
	}
	s1, err := NewSim(cfg)
	if err != nil {
		t.Fatal(err)
	}
	s2, err := NewSim(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 600; i++ {
		inputs := []PlayerInput{
			{PlayerID: 1, Dir: Dir((i % 8) + 1), Turbo: i%9 == 0, FireDir: Dir(((i + 2) % 9)), ClientTick: uint16(i)},
		}
		ev1, err1 := s1.Tick(inputs)
		ev2, err2 := s2.Tick(inputs)
		if (err1 == nil) != (err2 == nil) {
			t.Fatalf("tick %d errs differ", i)
		}
		if !reflect.DeepEqual(ev1, ev2) {
			t.Fatalf("tick %d events differ:\n%v\n vs\n%v", i, ev1, ev2)
		}
		if s1.Fingerprint() != s2.Fingerprint() {
			t.Fatalf("tick %d fingerprints differ", i)
		}
	}
}

// generatePhase3Inputs scripts 1200 ticks of single-player PvE action
// (movement + occasional fire). Inputs are deterministic.
func generatePhase3Inputs() [][]PlayerInput {
	const totalTicks = 1200
	out := make([][]PlayerInput, totalTicks)
	for i := 0; i < totalTicks; i++ {
		out[i] = []PlayerInput{
			{
				PlayerID:   1,
				Dir:        Dir((i % 8) + 1),
				Turbo:      i%17 == 0,
				FireDir:    Dir(((i + 1) % 9)),
				ClientTick: uint16(i),
			},
		}
	}
	return out
}

func generateBaselineInputs() [][]PlayerInput {
	const totalTicks = 600
	out := make([][]PlayerInput, totalTicks)
	for i := 0; i < totalTicks; i++ {
		out[i] = []PlayerInput{
			{PlayerID: 1, Dir: Dir((i % 8) + 1), Turbo: i%7 == 0, FireDir: Dir(((i + 3) % 9))},
			{PlayerID: 2, Dir: Dir(((i + 2) % 8) + 1), FireDir: Dir(((i + 1) % 9))},
			{PlayerID: 3, Dir: Dir(((i + 4) % 8) + 1)},
			{PlayerID: 4, Dir: Dir(((i + 6) % 8) + 1), FireDir: Dir(((i + 5) % 9))},
		}
		for j := range out[i] {
			out[i][j].ClientTick = uint16(i)
		}
	}
	return out
}

// Replay binary layout (test-internal):
//   u32 tick_count
//   per tick:
//     u8 input_count
//     per input:
//       u32 PlayerID
//       u8  Dir
//       u8  Turbo (0/1)
//       u8  FireDir
//       u16 ClientTick

func writeReplayInputs(path string, ticks [][]PlayerInput) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	var buf [4]byte
	binary.LittleEndian.PutUint32(buf[:], uint32(len(ticks)))
	if _, err := f.Write(buf[:4]); err != nil {
		return err
	}
	for _, tick := range ticks {
		if len(tick) > 255 {
			return errors.New("too many inputs in one tick")
		}
		if _, err := f.Write([]byte{byte(len(tick))}); err != nil {
			return err
		}
		for _, inp := range tick {
			binary.LittleEndian.PutUint32(buf[:], uint32(inp.PlayerID))
			f.Write(buf[:4])
			var turbo byte
			if inp.Turbo {
				turbo = 1
			}
			f.Write([]byte{byte(inp.Dir), turbo, byte(inp.FireDir)})
			binary.LittleEndian.PutUint16(buf[:2], inp.ClientTick)
			f.Write(buf[:2])
		}
	}
	return nil
}

func readReplayInputs(path string) ([][]PlayerInput, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(data) < 4 {
		return nil, errors.New("replay too short")
	}
	off := 0
	tickCount := binary.LittleEndian.Uint32(data[off:])
	off += 4
	out := make([][]PlayerInput, tickCount)
	for i := uint32(0); i < tickCount; i++ {
		if off >= len(data) {
			return nil, errors.New("replay truncated")
		}
		n := int(data[off])
		off++
		out[i] = make([]PlayerInput, n)
		for j := 0; j < n; j++ {
			pid := binary.LittleEndian.Uint32(data[off:])
			off += 4
			d := data[off]
			off++
			turbo := data[off] == 1
			off++
			fd := data[off]
			off++
			ct := binary.LittleEndian.Uint16(data[off:])
			off += 2
			out[i][j] = PlayerInput{
				PlayerID:   EntityID(pid),
				Dir:        Dir(d),
				Turbo:      turbo,
				FireDir:    Dir(fd),
				ClientTick: ct,
			}
		}
	}
	return out, nil
}

func TestEntityIDsStableUnderRespawn(t *testing.T) {
	cfg := Config{
		Seed:         1,
		Width:        60,
		Height:       40,
		PlayerIDs:    []EntityID{1, 2},
		NoGenerators: true,
	}
	s, err := NewSim(cfg)
	if err != nil {
		t.Fatalf("NewSim: %v", err)
	}
	OverrideMazeForTest(s, 60, 40, allFloorMaze(60, 40))
	seedSpawnTiles(s, []tilePos{{2, 2}, {58, 38}, {30, 20}})
	PlacePlayerForTest(s, 1, int32(1*subtilePerTile+subtilePerTile/2), int32(10*subtilePerTile+subtilePerTile/2))
	PlacePlayerForTest(s, 2, int32(4*subtilePerTile+subtilePerTile/2), int32(10*subtilePerTile+subtilePerTile/2))
	_, _ = s.Tick([]PlayerInput{{PlayerID: 1, FireDir: DirE}})
	// Wait for kill.
	for i := 0; i < 100; i++ {
		evs, _ := s.Tick(nil)
		for _, e := range evs {
			if e.Kind == EventEntityKill && e.Target == 2 {
				goto killed
			}
		}
	}
	t.Fatalf("p2 not killed")
killed:
	// Wait for respawn.
	for i := 0; i < 100; i++ {
		_, _ = s.Tick(nil)
		e, ok := EntityRawForTest(s, 2)
		if ok && e.Flags&FlagDead == 0 {
			if e.ID != 2 {
				t.Fatalf("ID changed: %d", e.ID)
			}
			return
		}
	}
	t.Fatalf("never respawned")
}

func TestNewSimRejectsEmptyPlayerIDs(t *testing.T) {
	_, err := NewSim(Config{Seed: 1, Width: 60, Height: 40, PlayerIDs: nil})
	if !errors.Is(err, ErrNoPlayers) {
		t.Fatalf("err = %v, want ErrNoPlayers", err)
	}
}

func TestNewSimRejectsOutOfHeadroomPlayerIDs(t *testing.T) {
	_, err := NewSim(Config{
		Seed: 1, Width: 60, Height: 40,
		PlayerIDs: []EntityID{EntityID(math.MaxUint32 - 10)},
	})
	if !errors.Is(err, ErrPlayerIDNoHeadroom) {
		t.Fatalf("err = %v, want ErrPlayerIDNoHeadroom", err)
	}
}

func TestNewSimRejectsDuplicates(t *testing.T) {
	_, err := NewSim(Config{Seed: 1, Width: 60, Height: 40, PlayerIDs: []EntityID{1, 1}})
	if !errors.Is(err, ErrDuplicatePlayerID) {
		t.Fatalf("err = %v, want ErrDuplicatePlayerID", err)
	}
}

func TestNewSimRejectsZeroID(t *testing.T) {
	_, err := NewSim(Config{Seed: 1, Width: 60, Height: 40, PlayerIDs: []EntityID{0}})
	if !errors.Is(err, ErrZeroPlayerID) {
		t.Fatalf("err = %v, want ErrZeroPlayerID", err)
	}
}

func TestNewSimRejectsInvalidMapSize(t *testing.T) {
	_, err := NewSim(Config{Seed: 1, Width: 10, Height: 10, PlayerIDs: []EntityID{1}})
	if !errors.Is(err, ErrInvalidMapSize) {
		t.Fatalf("err = %v, want ErrInvalidMapSize", err)
	}
}

func TestTickReturnsErrIDExhausted(t *testing.T) {
	cfg := Config{
		Seed:         1,
		Width:        60,
		Height:       40,
		PlayerIDs:    []EntityID{1},
		NoGenerators: true,
	}
	s, err := NewSim(cfg)
	if err != nil {
		t.Fatalf("NewSim: %v", err)
	}
	OverrideMazeForTest(s, 60, 40, allFloorMaze(60, 40))
	PlacePlayerForTest(s, 1, int32(10*subtilePerTile+subtilePerTile/2), int32(20*subtilePerTile+subtilePerTile/2))
	ForceExhaustedForTest(s)
	// Drive a tick with fire request.
	tick0 := s.ServerTick()
	evs, err := s.Tick([]PlayerInput{{PlayerID: 1, FireDir: DirE}})
	if !errors.Is(err, ErrIDExhausted) {
		t.Fatalf("err = %v, want ErrIDExhausted", err)
	}
	if evs != nil {
		t.Fatalf("evs non-nil: %v", evs)
	}
	if s.ServerTick() != tick0 {
		t.Fatalf("serverTick advanced: %d -> %d", tick0, s.ServerTick())
	}
	// Two more calls also return ErrIDExhausted.
	for i := 0; i < 2; i++ {
		evs, err := s.Tick(nil)
		if !errors.Is(err, ErrIDExhausted) {
			t.Fatalf("subsequent tick: err = %v", err)
		}
		if evs != nil {
			t.Fatalf("subsequent tick: evs non-nil")
		}
	}
}

func TestNoRespawnRemovesDeadPlayer(t *testing.T) {
	cfg := Config{
		Seed:         1,
		Width:        60,
		Height:       40,
		PlayerIDs:    []EntityID{1, 2},
		NoGenerators: true,
		NoRespawn:    true,
	}
	s, err := NewSim(cfg)
	if err != nil {
		t.Fatalf("NewSim: %v", err)
	}
	OverrideMazeForTest(s, 60, 40, allFloorMaze(60, 40))
	PlacePlayerForTest(s, 1, int32(1*subtilePerTile+subtilePerTile/2), int32(10*subtilePerTile+subtilePerTile/2))
	PlacePlayerForTest(s, 2, int32(4*subtilePerTile+subtilePerTile/2), int32(10*subtilePerTile+subtilePerTile/2))
	_, _ = s.Tick([]PlayerInput{{PlayerID: 1, FireDir: DirE}})
	// Wait for kill.
	var sawHit, sawKill bool
	for i := 0; i < 100; i++ {
		evs, _ := s.Tick(nil)
		for _, e := range evs {
			if e.Kind == EventEntityHit && e.Target == 2 {
				sawHit = true
			}
			if e.Kind == EventEntityKill && e.Target == 2 {
				sawKill = true
			}
		}
		if sawKill {
			if !sawHit {
				t.Fatalf("kill without preceding hit")
			}
			// B should be absent now.
			if _, ok := EntityRawForTest(s, 2); ok {
				t.Fatalf("B still in store after kill under NoRespawn")
			}
			break
		}
	}
	if !sawKill {
		t.Fatalf("never killed")
	}
	// Tick more; B never reappears.
	for i := 0; i < 200; i++ {
		evs, _ := s.Tick(nil)
		for _, e := range evs {
			if e.Kind == EventEntitySpawn && e.Target == 2 {
				t.Fatalf("unexpected spawn for B")
			}
		}
		if _, ok := EntityRawForTest(s, 2); ok {
			t.Fatalf("B reappeared")
		}
	}
}

func TestLastInputTickPersistsAcrossTicks(t *testing.T) {
	cfg := Config{
		Seed:         1,
		Width:        60,
		Height:       40,
		PlayerIDs:    []EntityID{1},
		NoGenerators: true,
	}
	s, err := NewSim(cfg)
	if err != nil {
		t.Fatalf("NewSim: %v", err)
	}
	for i := 0; i < 5; i++ {
		_, _ = s.Tick([]PlayerInput{{PlayerID: 1, ClientTick: uint16(i + 100)}})
		if got := s.LastInputTick(1); got != uint16(i+100) {
			t.Fatalf("after tick %d: got %d, want %d", i, got, i+100)
		}
	}
	// Tick with no input. LastInputTick should stay.
	_, _ = s.Tick(nil)
	if got := s.LastInputTick(1); got != 104 {
		t.Fatalf("after silent tick: got %d, want 104", got)
	}
}
