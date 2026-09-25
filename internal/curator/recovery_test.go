package curator

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"imbue/internal/collect"
	"imbue/internal/evidence"
	"imbue/internal/local"
)

type offlineSink struct{}

func (offlineSink) Ingest(context.Context, evidence.Batch) error {
	return errors.New("database offline")
}
func TestIntegrationSpoolCrashRecovery(t *testing.T) {
	w, _ := integrationWorker(t)
	ctx := context.Background()
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	w.Config.Repository = dir
	w.Config.ObserveAll = false
	w.Config.ObserveAllSince = ""
	w.Config.CodexHome = filepath.Join(dir, "codex")
	path := filepath.Join(w.Config.CodexHome, "sessions", "rollout-fixture.jsonl")
	os.MkdirAll(filepath.Dir(path), 0700)
	rec := func(typ string, v any) string {
		b, _ := json.Marshal(map[string]any{"type": typ, "payload": v, "timestamp": "2026-09-17T00:00:00Z"})
		return string(b) + "\n"
	}
	input := rec("session_meta", map[string]any{"id": "spooled", "cwd": dir, "cli_version": collect.SupportedVersion, "source": "vscode"}) + rec("event_msg", map[string]any{"type": "task_started", "turn_id": "one"}) + rec("event_msg", map[string]any{"type": "item_completed", "turn_id": "one", "item": map[string]any{"type": "UserMessage", "content": "API key sk-12345678901234567890"}}) + rec("event_msg", map[string]any{"type": "task_complete", "turn_id": "one"})
	os.WriteFile(path, []byte(input), 0600)
	cl, _ := collect.New(w.Config)
	if _, e := cl.Scan(ctx); e != nil {
		t.Fatal(e)
	}
	files, _ := filepath.Glob(filepath.Join(w.Config.Root, "spool/events/*.json"))
	if len(files) != 1 {
		t.Fatal("missing spool")
	}
	b, _ := os.ReadFile(files[0])
	if strings.Contains(string(b), "12345678901234567890") {
		t.Fatal("secret in offline buffer")
	}
	if _, e := collect.Flush(ctx, w.Config.Root, offlineSink{}); e == nil {
		t.Fatal("expected DB outage")
	}
	if _, e := os.Stat(files[0]); e != nil {
		t.Fatal("buffer lost on failure")
	}
	// A new collector process resumes from the durable cursor while the unacknowledged batch survives.
	cl, _ = collect.New(w.Config)
	r, e := cl.Scan(ctx)
	if e != nil || r.Events != 0 {
		t.Fatalf("cursor failed: %+v %v", r, e)
	}
	if _, e = collect.Flush(ctx, w.Config.Root, w.Store); e != nil {
		t.Fatal(e)
	}
	// Simulate a crash after DB commit but before unlinking the batch.
	if e = local.AtomicWrite(files[0], b); e != nil {
		t.Fatal(e)
	}
	if _, e = collect.Flush(ctx, w.Config.Root, w.Store); e != nil {
		t.Fatal(e)
	}
	cs, _ := w.Store.Q.ListConversations(ctx)
	if len(cs) != 1 || cs[0].CompletedTurns != 1 {
		t.Fatalf("duplicate turn after replay: %+v", cs)
	}
	var n int
	w.Store.Pool.QueryRow(ctx, "SELECT count(*) FROM evidence").Scan(&n)
	if n != 3 {
		t.Fatalf("duplicate evidence after replay: %d", n)
	}
}
func TestSplitKeepsUserContextWithToolFragments(t *testing.T) {
	p := Packet{Events: []evidence.Event{{ID: "user", TurnID: "one", Kind: "user.message", Payload: json.RawMessage(`{"text":"変更の範囲を限定して"}`)}, {ID: "tool", TurnID: "one", Kind: "tool.command", Payload: json.RawMessage(`{"output":"` + strings.Repeat("long test result ", 5000) + `"}`)}}, AllowedEvidenceIDs: []string{"user", "tool"}}
	parts, e := Split(p, 4096)
	if e != nil {
		t.Fatal(e)
	}
	for _, part := range parts {
		found := false
		for _, ev := range append(part.Events, part.ContextEvents...) {
			if ev.ID == "user" {
				found = true
			}
		}
		if !found {
			t.Fatal("tool fragment separated from its user instruction")
		}
	}
}

func TestIntegrationCurrentScopeAppliesToQueuedJobs(t *testing.T) {
	w, f := integrationWorker(t)
	ctx := context.Background()
	addTurns(t, w, "private", 1, 10)
	if _, e := w.Enqueue(ctx, "private", false); e != nil {
		t.Fatal(e)
	}
	w.Config.ExcludeSessions = []string{"private"}
	if _, e := w.RunOnce(ctx); e == nil {
		t.Fatal("excluded conversation was processed")
	}
	if f.calls != 0 {
		t.Fatal("excluded data reached Luna")
	}
	c, _ := w.Store.Q.GetConversation(ctx, "private")
	if c.ProcessedSeq != 0 {
		t.Fatal("excluded job advanced cursor")
	}
}
