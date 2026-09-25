package curator

import (
	"encoding/json"
	"strings"
	"testing"

	"imbue/internal/evidence"
)

func TestImageExclusionRetainsTextAndOriginalEvidence(t *testing.T) {
	raw := json.RawMessage(`{"result":{"content":[{"type":"text","text":"項目を残す\n表示を整理"},{"type":"image","mimeType":"image/png","data":"SECRET_BASE64"}],"_meta":{"codex/toolSurface":{"screenshot":{"url":"data:image/png;base64,U0VDUkVU","tabId":"1"},"backend":"chrome"}}},"number":9007199254740993}`)
	p := Packet{Events: []evidence.Event{{ID: "img", Kind: "tool.call", Payload: raw}}}
	clean, err := excludePacketImages(p)
	if err != nil {
		t.Fatal(err)
	}
	b := string(clean.Events[0].Payload)
	if strings.Contains(b, "SECRET_BASE64") || strings.Contains(b, "data:image") || !strings.Contains(b, "項目を残す") || !strings.Contains(b, "9007199254740993") || !strings.Contains(b, "image_omitted") {
		t.Fatalf("incorrect image exclusion: %s", b)
	}
	if string(p.Events[0].Payload) != string(raw) {
		t.Fatal("immutable source mutated")
	}
	again, err := excludePacketImages(clean)
	if err != nil || string(again.Events[0].Payload) != b {
		t.Fatal("image exclusion is not idempotent")
	}
	plain := json.RawMessage(`{ "text": "原文の空白も保持", "n": 1 }`)
	out, err := excludeImages(plain)
	if err != nil || string(out) != string(plain) {
		t.Fatal("non-image raw modified")
	}
}

func TestImageDataURLsInsideTextAreExcluded(t *testing.T) {
	raw := json.RawMessage(`{"text":"before data:image/png;base64,QUJDREVGRw== after"}`)
	b, err := excludeImages(raw)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "QUJDREVGRw") || !strings.Contains(string(b), "before") || !strings.Contains(string(b), "after") {
		t.Fatal("embedded image was not isolated")
	}
}

func TestImageRemovalHappensBeforeFragmentation(t *testing.T) {
	raw, _ := json.Marshal(map[string]any{"type": "image", "data": strings.Repeat("A", 500000), "mimeType": "image/png"})
	p := Packet{Events: []evidence.Event{{ID: "u", ConversationID: "a", TurnID: "t", Kind: "user.message", Payload: json.RawMessage(`{"text":"この画面を見て"}`)}, {ID: "i", ConversationID: "a", TurnID: "t", Kind: "tool.call", Payload: raw}}, AllowedEvidenceIDs: []string{"u", "i"}}
	parts, err := Split(p, 8192)
	if err != nil {
		t.Fatal(err)
	}
	if len(parts) != 1 {
		t.Fatalf("image caused %d parts", len(parts))
	}
	if strings.Contains(string(parts[0].Events[1].Payload), strings.Repeat("A", 100)) {
		t.Fatal("image bytes reached input")
	}
}

func TestSplitKeepsWholeTurnsAndBoundaryFeedback(t *testing.T) {
	p := Packet{Events: []evidence.Event{}}
	for _, turn := range []string{"1", "2", "3", "4"} {
		for _, kind := range []string{"user.message", "agent.message"} {
			id := turn + kind
			raw, _ := json.Marshal(map[string]string{"text": strings.Repeat(turn, 1800), "phase": "final"})
			p.Events = append(p.Events, evidence.Event{ID: id, ConversationID: "a", TurnID: turn, Kind: kind, Payload: raw})
			p.AllowedEvidenceIDs = append(p.AllowedEvidenceIDs, id)
		}
	}
	parts, err := Split(p, 12000)
	if err != nil {
		t.Fatal(err)
	}
	if len(parts) < 2 {
		t.Fatal("fixture must split")
	}
	ownership := map[string]int{}
	for i, part := range parts {
		if packetWireSize(part) > 12000 {
			t.Fatal("budget exceeded")
		}
		for _, ev := range part.Events {
			if old, ok := ownership[ev.TurnID]; ok && old != i {
				t.Fatal("fitting turn split")
			}
			ownership[ev.TurnID] = i
		}
	}
	// The first part must show the next turn's user feedback as context.
	nextTurn := parts[1].Events[0].TurnID
	found := false
	for _, ev := range parts[0].ContextEvents {
		found = found || ev.TurnID == nextTurn && ev.Kind == "user.message"
	}
	if !found {
		t.Fatal("boundary feedback missing")
	}
}
