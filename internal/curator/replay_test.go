package curator

import (
	"encoding/json"
	"os"
	"testing"
)

// Offline replay uses an explicitly supplied local fixture; no inference or DB
// writes occur. The report contains only counts and byte sizes, never evidence.
func TestOfflineInputReplay(t *testing.T) {
	path := os.Getenv("IMBUE_INPUT_REPLAY")
	if path == "" {
		t.Skip("set IMBUE_INPUT_REPLAY to an offline packet fixture")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Packet   Packet   `json:"packet"`
		OldParts []Packet `json:"old_parts"`
	}
	if err = json.Unmarshal(b, &fixture); err != nil {
		t.Fatal(err)
	}
	before := 0
	for _, p := range fixture.OldParts {
		before += packetWireSize(p)
	}
	parts, err := Split(fixture.Packet, 180000)
	if err != nil {
		t.Fatal(err)
	}
	after := 0
	for _, p := range parts {
		n := packetWireSize(p)
		if n > 180000 {
			t.Fatal("budget exceeded")
		}
		after += n
	}
	report := map[string]any{"old_parts": len(fixture.OldParts), "new_parts": len(parts), "old_input_bytes": before, "new_input_bytes": after, "reduction_percent": 100 * (1 - float64(after)/float64(before))}
	result, _ := json.MarshalIndent(report, "", "  ")
	t.Log(string(result))
	if path := os.Getenv("IMBUE_INPUT_REPORT"); path != "" {
		if err = os.WriteFile(path, result, 0600); err != nil {
			t.Fatal(err)
		}
	}
}
