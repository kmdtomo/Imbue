package curator

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"imbue/internal/evidence"
)

func testExperience(id string) *Experience {
	return &Experience{Situation: "既存の画面に対する変更依頼", Observations: []Observation{{Fact: "ユーザーが変更範囲を限定した", EventIDs: []string{id}}}, Hypothesis: "この変更では依頼範囲の維持を優先する可能性がある", AlternativeInterpretations: []string{}, Unknowns: []string{"他の仕事への適用は不明"}}
}

func TestModelInputPreservesRawAndConversationBoundaries(t *testing.T) {
	raw := json.RawMessage(`{"type":"UserMessage","content":[{"type":"text","text":"項目は残して\n見た目だけシンプルに"}],"extra":{"keep":true}}`)
	p := Packet{Events: []evidence.Event{
		{ID: "new", ConversationID: "A", TurnID: "same", OccurredAt: "2026-09-18T10:00:00+09:00", Kind: "user.message", Payload: raw},
		{ID: "other", ConversationID: "B", TurnID: "same", OccurredAt: "2026-09-18T00:30:00Z", Kind: "agent.message", Payload: json.RawMessage(`{"text":"別の仕事"}`)},
	}, ContextEvents: []evidence.Event{{ID: "old", ConversationID: "A", TurnID: "old", OccurredAt: "2026-09-18T00:00:00Z", Kind: "agent.message", Payload: json.RawMessage(`{"text":"前の案"}`)}}}
	m := modelInput(p)
	if m.Timeline[0].EvidenceID != "old" || m.Timeline[1].EvidenceID != "other" || m.Timeline[2].EvidenceID != "new" {
		t.Fatalf("wrong temporal order: %+v", m.Timeline)
	}
	if m.Timeline[0].Origin != "context" || m.Timeline[2].ConversationID != "A" || m.Timeline[1].ConversationID != "B" {
		t.Fatal("lost provenance")
	}
	if string(m.Timeline[2].Raw) != string(raw) {
		t.Fatal("raw payload rewritten")
	}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if err = json.Unmarshal(b, &wire); err != nil {
		t.Fatal(err)
	}
	steps := wire["timeline"].([]any)
	if _, ok := steps[2].(map[string]any)["raw"].(map[string]any); !ok {
		t.Fatal("raw was encoded as a string")
	}
	if p.Events[0].ID != "new" {
		t.Fatal("source mutated")
	}
}

func TestSplitDoesNotAttachOtherConversationAsPreviousContext(t *testing.T) {
	p := Packet{Events: []evidence.Event{
		{ID: "a", ConversationID: "A", TurnID: "same", Kind: "user.message", Payload: json.RawMessage(`{"text":"Aの依頼"}`)},
		{ID: "b", ConversationID: "B", TurnID: "same", Kind: "user.message", Payload: json.RawMessage(`{"text":"Bの依頼"}`)},
		{ID: "tool", ConversationID: "A", TurnID: "next", Kind: "tool.call", Payload: json.RawMessage(`{"result":"` + strings.Repeat("long output ", 3000) + `"}`)},
	}, AllowedEvidenceIDs: []string{"a", "b", "tool"}}
	parts, err := Split(p, 8192)
	if err != nil {
		t.Fatal(err)
	}
	checked := false
	for _, part := range parts {
		onlyTool := len(part.Events) > 0
		for _, ev := range part.Events {
			onlyTool = onlyTool && ev.ID == "tool"
		}
		if onlyTool {
			checked = true
			found := false
			for _, e := range part.ContextEvents {
				if e.ConversationID == "B" {
					t.Fatal("unrelated conversation attached as preceding context")
				}
				found = found || e.ID == "a"
			}
			if !found {
				t.Fatal("related conversation context missing")
			}
		}
	}
	if !checked {
		t.Fatal("fixture did not produce isolated fragments")
	}
}

func TestExperienceGroundingAndAliasRestoration(t *testing.T) {
	p := Packet{SchemaVersion: SchemaVersion, Repository: "/repo", Events: []evidence.Event{{ID: "real", Kind: "user.message"}}, AllowedEvidenceIDs: []string{"real"}}
	ap, aliases := aliasEvidenceIDs(p)
	patch := Patch{Operation: "create", Changes: Changes{Title: "範囲", Judgment: "範囲を守る判断", Relation: "correction", Repository: "/repo", Status: "active", Conditions: []string{}, Exceptions: []string{}, EligibilityCandidates: []string{}}, GroundedInEventIDs: []string{"E000001"}}
	r := Result{Patches: []Patch{patch}}
	if Validate(r, ap) == nil {
		t.Fatal("v2 accepted summary-only case")
	}
	r.Patches[0].Changes.Experience = testExperience("E999999")
	if Validate(r, ap) == nil {
		t.Fatal("invented observation reference accepted")
	}
	r.Patches[0].Changes.Experience = testExperience("E000001")
	r.Patches[0].Changes.Experience.Hypothesis = "" // A grounded episode need not imply a broader preference.
	if err := Validate(r, ap); err != nil {
		t.Fatal(err)
	}
	if err := restoreEvidenceIDs(&r, aliases); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(r.Patches[0].Changes.Experience.Observations[0].EventIDs, []string{"real"}) {
		t.Fatal("nested references not restored")
	}
	if err := Validate(r, p); err != nil {
		t.Fatal(err)
	}
}

func TestOutputSchemaRequiresExperience(t *testing.T) {
	var s map[string]any
	if err := json.Unmarshal([]byte(Schema), &s); err != nil {
		t.Fatal(err)
	}
	changes := s["properties"].(map[string]any)["patches"].(map[string]any)["items"].(map[string]any)["properties"].(map[string]any)["changes"].(map[string]any)
	required := changes["required"].([]any)
	found := false
	for _, v := range required {
		found = found || v == "experience"
	}
	if !found {
		t.Fatal("experience not required by runtime schema")
	}
}
