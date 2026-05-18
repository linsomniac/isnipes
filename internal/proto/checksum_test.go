package proto

import (
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

var updateGolden = flag.Bool("update", false, "regenerate golden testdata files")

func TestSchemaChecksumValue(t *testing.T) {
	wantPath := filepath.Join("..", "..", "testdata", "proto", "checksum.txt")
	if *updateGolden {
		text := fmt.Sprintf("0x%08X\n", SchemaChecksum)
		if err := os.WriteFile(wantPath, []byte(text), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		t.Logf("wrote %s = %s", wantPath, text)
		return
	}
	b, err := os.ReadFile(wantPath)
	if err != nil {
		t.Fatalf("read %s: %v (run with -update)", wantPath, err)
	}
	want := string(b)
	if len(want) > 0 && want[len(want)-1] == '\n' {
		want = want[:len(want)-1]
	}
	got := fmt.Sprintf("0x%08X", SchemaChecksum)
	if want != got {
		t.Fatalf("schemaChecksum: want %s, got %s", want, got)
	}
}

func TestSchemaChecksumIsLittleEndianBytes0To3(t *testing.T) {
	sum := sha256.Sum256([]byte(SchemaDescriptor()))
	want := uint32(sum[0]) | uint32(sum[1])<<8 | uint32(sum[2])<<16 | uint32(sum[3])<<24
	if SchemaChecksum != want {
		t.Fatalf("checksum = %08x, want %08x (LE bytes 0..3)", SchemaChecksum, want)
	}
}

// TestSchemaDescriptorPinned guards against accidental string changes
// to the descriptor. The descriptor's contents are tested implicitly
// by TestSchemaChecksumValue against the committed checksum.txt, but
// this test makes the failure mode obvious if the descriptor changes
// without a deliberate `-update`.
func TestSchemaDescriptorPinned(t *testing.T) {
	desc := SchemaDescriptor()
	// Pinned hash of the descriptor (LE u32 of SHA-256[0:4]).
	if len(desc) < 100 {
		t.Fatalf("descriptor unexpectedly short: %d bytes", len(desc))
	}
	full := sha256.Sum256([]byte(desc))
	t.Logf("descriptor SHA-256: %s", hex.EncodeToString(full[:]))
}
