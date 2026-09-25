package curator

import (
	"encoding/json"
	"testing"

	"imbue/internal/evidence"
)

func TestEvidenceAliasesRoundTrip(t *testing.T) {
	p := Packet{
		Repository: "/repo",
		Events: []evidence.Event{
			{ID: "2db8dce76f31f0ff34004ea37f4a730609ceab24f0537a050c023c69f77dc12e", Kind: "tool.command"},
			{ID: "user-message-id", Kind: "user.message", Payload: json.RawMessage(`{"text":"範囲を限定して"}`)},
		},
		ContextEvents: []evidence.Event{{ID: "user-message-id", Kind: "user.message"}},
		AllowedEvidenceIDs: []string{
			"2db8dce76f31f0ff34004ea37f4a730609ceab24f0537a050c023c69f77dc12e",
			"user-message-id",
		},
	}

	modelPacket, aliases := aliasEvidenceIDs(p)
	if got := modelPacket.Events[0].ID; got != "E000001" {
		t.Fatalf("first alias = %q", got)
	}
	if got := modelPacket.Events[1].ID; got != "E000002" {
		t.Fatalf("second alias = %q", got)
	}
	if got := modelPacket.ContextEvents[0].ID; got != "E000002" {
		t.Fatalf("repeated ID received a different alias: %q", got)
	}
	if p.Events[0].ID == modelPacket.Events[0].ID {
		t.Fatal("original packet was mutated")
	}

	result := Result{Patches: []Patch{{
		Operation: "create",
		Changes: Changes{
			Title:                 "変更範囲",
			Relation:              "preference_refinement",
			Repository:            "/repo",
			Judgment:              "依頼した変更の範囲を守る",
			Conditions:            []string{},
			Exceptions:            []string{},
			EligibilityCandidates: []string{"Eval"},
			Status:                "active",
		},
		GroundedInEventIDs: []string{"E000002", "E000001"},
	}}}
	if err := Validate(result, modelPacket); err != nil {
		t.Fatalf("aliased result did not validate: %v", err)
	}
	if err := restoreEvidenceIDs(&result, aliases); err != nil {
		t.Fatal(err)
	}
	if got := result.Patches[0].GroundedInEventIDs[0]; got != "user-message-id" {
		t.Fatalf("restored ID = %q", got)
	}
	if err := Validate(result, p); err != nil {
		t.Fatalf("restored result did not validate: %v", err)
	}
}

func TestEvidenceAliasesRejectUnknownReference(t *testing.T) {
	result := Result{Patches: []Patch{{GroundedInEventIDs: []string{"E999999"}}}}
	if err := restoreEvidenceIDs(&result, map[string]string{"E000001": "real"}); err == nil {
		t.Fatal("unknown alias was accepted")
	}
}

func TestCommandEvidenceIsRetainedOutsideCurationInput(t *testing.T) {
	if includedInCuration(evidence.Event{Kind: "tool.command"}) {
		t.Fatal("command evidence was admitted to Luna input")
	}
	for _, kind := range []string{"user.message", "agent.message", "artifact.diff", "turn.completed"} {
		if !includedInCuration(evidence.Event{Kind: kind}) {
			t.Fatalf("%s was unexpectedly excluded", kind)
		}
	}
}
