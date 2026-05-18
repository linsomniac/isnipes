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
