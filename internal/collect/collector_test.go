package collect

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"imbue/internal/config"
	"imbue/internal/evidence"
)

type memorySink struct{ events map[string]evidence.Event }

func (s *memorySink) Ingest(_ context.Context, b evidence.Batch) error {
	for _, e := range b.Events {
		s.events[e.ID] = e
	}
	return nil
}
func record(typ string, p any) string {
	b, _ := json.Marshal(map[string]any{"timestamp": "2026-09-17T00:00:00Z", "type": typ, "payload": p})
	return string(b) + "\n"
}
func event(t, turn string) string {
	return record("event_msg", map[string]any{"type": t, "turn_id": turn})
}
func item(t, turn, text string) string {
	return record("event_msg", map[string]any{"type": "item_completed", "turn_id": turn, "item": map[string]any{"type": t, "content": text}})
}
func setup(t *testing.T) (*Collector, string, string) {
	t.Helper()
	dir := t.TempDir()
	repo, _ := filepath.EvalSymlinks(dir)
	c := config.Default(filepath.Join(repo, "imbue"), repo)
	c.CodexHome = filepath.Join(repo, "codex")
	path := filepath.Join(c.CodexHome, "sessions", "rollout-test.jsonl")
	os.MkdirAll(filepath.Dir(path), 0700)
	meta := record("session_meta", map[string]any{"id": "conversation-a", "cwd": repo, "cli_version": SupportedVersion, "source": "vscode"})
	os.WriteFile(path, []byte(meta), 0600)
	cl, e := New(c)
	if e != nil {
		t.Fatal(e)
	}
	return cl, path, meta
}
func appendFile(t *testing.T, path, s string) {
	t.Helper()
	f, e := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	if _, e = f.WriteString(s); e != nil {
		t.Fatal(e)
	}
}
func TestPartialReplayAndExplicitTurns(t *testing.T) {
	cl, p, _ := setup(t)
	ctx := context.Background()
	appendFile(t, p, event("task_started", "t1")+item("UserMessage", "t1", "hello")+item("CommandExecution", "t1", "test result")+item("AgentMessage", "t1", "done")+event("task_complete", "t1")+event("task_complete", "t1"))
	incomplete := event("task_started", "t2")
	appendFile(t, p, incomplete[:len(incomplete)-4])
	r, e := cl.Scan(ctx)
	if e != nil || len(r.Warnings) != 0 || r.Events != 6 {
		t.Fatalf("scan: %+v %v", r, e)
	}
	sink := &memorySink{events: map[string]evidence.Event{}}
	if _, e = Flush(ctx, cl.Config.Root, sink); e != nil {
		t.Fatal(e)
	}
	r, e = cl.Scan(ctx)
	if e != nil || r.Events != 0 {
		t.Fatalf("partial line consumed: %+v %v", r, e)
	}
	appendFile(t, p, incomplete[len(incomplete)-4:]+item("Reasoning", "t2", "private internal reasoning")+event("task_complete", "t2"))
	r, e = cl.Scan(ctx)
	if e != nil || r.Events != 2 {
		t.Fatalf("resume: %+v %v", r, e)
	}
	Flush(ctx, cl.Config.Root, sink)
	os.RemoveAll(filepath.Join(cl.Config.Root, "state", "cursors"))
	cl.Scan(ctx)
	Flush(ctx, cl.Config.Root, sink)
	if len(sink.events) != 8 {
		t.Fatalf("replay duplicates: %d", len(sink.events))
	}
	for _, e := range sink.events {
		if strings.Contains(string(e.Payload), "private internal") {
			t.Fatal("reasoning persisted")
		}
	}
}
func TestSecretAndPathExcludedBeforeSpool(t *testing.T) {
	cl, p, _ := setup(t)
	appendFile(t, p, event("task_started", "t")+item("UserMessage", "t", "token sk-12345678901234567890")+item("CommandExecution", "t", "cat /project/.env password=topsecret"))
	r, e := cl.Scan(context.Background())
	if e != nil || len(r.Warnings) != 0 {
		t.Fatalf("%+v %v", r, e)
	}
	files, _ := filepath.Glob(filepath.Join(cl.Config.Root, "spool/events/*.json"))
	b, _ := os.ReadFile(files[0])
	if strings.Contains(string(b), "12345678901234567890") || strings.Contains(string(b), "topsecret") {
		t.Fatal("secret reached spool")
	}
	if !strings.Contains(string(b), "evidence.excluded") {
		t.Fatal("missing exclusion marker")
	}
}
func TestScopeSubagentsAndUnknownVersion(t *testing.T) {
	for _, tt := range []struct {
		name, version, source string
		excluded              bool
		warning               bool
	}{{"subagent", SupportedVersion, `{"subagent":{"parent_thread_id":"x"}}`, false, false}, {"excluded", SupportedVersion, `"vscode"`, true, false}, {"version", "0.46.0", `"vscode"`, false, true}} {
		t.Run(tt.name, func(t *testing.T) {
			cl, p, _ := setup(t)
			if tt.excluded {
				cl.Config.ExcludeSessions = []string{"conversation-a"}
			}
			text := record("session_meta", map[string]any{"id": "conversation-a", "cwd": cl.Config.Repository, "cli_version": tt.version, "source": json.RawMessage(tt.source)}) + event("task_started", "t") + event("task_complete", "t")
			os.WriteFile(p, []byte(text), 0600)
			r, e := cl.Scan(context.Background())
			if e != nil || r.Events != 0 || (len(r.Warnings) > 0) != tt.warning {
				t.Fatalf("%+v %v", r, e)
			}
		})
	}
}
func TestObserveAllDiscoversNewWorkspaceAndSkipsHistory(t *testing.T) {
	cl, _, _ := setup(t)
	other := t.TempDir()
	other, _ = filepath.EvalSymlinks(other)
	cl.Config.ObserveAll = true
	activation := time.Date(2026, 9, 18, 2, 0, 0, 0, time.UTC)
	cl.Config.ObserveAllSince = activation.Format(time.RFC3339Nano)
	oldPath := filepath.Join(cl.Config.CodexHome, "sessions", "rollout-old.jsonl")
	newPath := filepath.Join(cl.Config.CodexHome, "sessions", "rollout-new.jsonl")
	oldMeta := recordAt(activation.Add(-time.Hour), "session_meta", map[string]any{"id": "old", "cwd": other, "cli_version": LegacySupportedVersion, "source": "vscode"})
	newMeta := recordAt(activation.Add(time.Second), "session_meta", map[string]any{"id": "new", "cwd": other, "cli_version": SupportedVersion, "source": "vscode"})
	if e := os.WriteFile(oldPath, []byte(oldMeta+event("task_started", "old-turn")+event("task_complete", "old-turn")), 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(newPath, []byte(newMeta+event("task_started", "new-turn")+event("task_complete", "new-turn")), 0600); e != nil {
		t.Fatal(e)
	}
	r, e := cl.Scan(context.Background())
	if e != nil || len(r.Warnings) != 0 || r.Events != 2 {
		t.Fatalf("first scan: %+v %v", r, e)
	}
	appendFile(t, oldPath, event("task_started", "future")+event("task_complete", "future"))
	r, e = cl.Scan(context.Background())
	if e != nil || len(r.Warnings) != 0 || r.Events != 2 {
		t.Fatalf("future scan: %+v %v", r, e)
	}
	files, _ := filepath.Glob(filepath.Join(cl.Config.Root, "spool", "events", "*.json"))
	var seenOldHistory bool
	for _, path := range files {
		b, _ := os.ReadFile(path)
		if strings.Contains(string(b), "old-turn") {
			seenOldHistory = true
		}
		if !strings.Contains(string(b), other) {
			t.Fatal("discovered workspace was not preserved")
		}
	}
	if seenOldHistory {
		t.Fatal("pre-activation history was imported")
	}
}

func TestObserveAllSilentlyIgnoresUnsupportedHistory(t *testing.T) {
	cl, _, _ := setup(t)
	other := t.TempDir()
	cl.Config.ObserveAll = true
	activation := time.Date(2026, 9, 18, 2, 0, 0, 0, time.UTC)
	cl.Config.ObserveAllSince = activation.Format(time.RFC3339Nano)
	path := filepath.Join(cl.Config.CodexHome, "sessions", "rollout-unsupported-history.jsonl")
	meta := recordAt(activation.Add(-time.Hour), "session_meta", map[string]any{"id": "unsupported-history", "cwd": other, "cli_version": "0.1.0", "source": "vscode"})
	if e := os.WriteFile(path, []byte(meta+event("task_started", "old")), 0600); e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 2; i++ {
		r, e := cl.Scan(context.Background())
		if e != nil || len(r.Warnings) != 0 || r.Events != 0 {
			t.Fatalf("scan %d: %+v %v", i, r, e)
		}
	}
}

func TestResumedRolloutSegmentsHaveIndependentCursorsAndEvidenceIDs(t *testing.T) {
	cl, firstPath, _ := setup(t)
	cl.Config.ObserveAll = true
	cl.Config.ObserveAllSince = ""
	secondPath := filepath.Join(cl.Config.CodexHome, "sessions", "rollout-resumed.jsonl")
	firstMeta := recordAt(time.Date(2026, 9, 18, 1, 0, 0, 0, time.UTC), "session_meta", map[string]any{"id": "conversation-a", "cwd": cl.Config.Repository, "cli_version": SupportedVersion, "source": "vscode"})
	secondMeta := recordAt(time.Date(2026, 9, 18, 2, 0, 0, 0, time.UTC), "session_meta", map[string]any{"id": "conversation-a", "cwd": cl.Config.Repository, "cli_version": SupportedVersion, "source": "vscode"})
	if e := os.WriteFile(firstPath, []byte(firstMeta+event("task_started", "first")+event("task_complete", "first")), 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(secondPath, []byte(secondMeta+event("task_started", "second")+event("task_complete", "second")), 0600); e != nil {
		t.Fatal(e)
	}
	r, e := cl.Scan(context.Background())
	if e != nil || len(r.Warnings) != 0 || r.Events != 4 {
		t.Fatalf("segments collided: %+v %v", r, e)
	}
	sink := &memorySink{events: map[string]evidence.Event{}}
	if _, e = Flush(context.Background(), cl.Config.Root, sink); e != nil {
		t.Fatal(e)
	}
	if len(sink.events) != 4 {
		t.Fatalf("evidence IDs collided: %d", len(sink.events))
	}
	r, e = cl.Scan(context.Background())
	if e != nil || len(r.Warnings) != 0 || r.Events != 0 {
		t.Fatalf("cursor did not persist: %+v %v", r, e)
	}
}

func recordAt(at time.Time, typ string, p any) string {
	b, _ := json.Marshal(map[string]any{"timestamp": at.Format(time.RFC3339Nano), "type": typ, "payload": p})
	return string(b) + "\n"
}
func TestMissingBoundaryNotInvented(t *testing.T) {
	cl, _, _ := setup(t)
	cur := Cursor{}
	e, err := cl.normalize([]byte(event("task_complete", "")), "c", "source", 0, &cur)
	if err != nil || e.Kind != "boundary.missing" {
		t.Fatalf("%+v %v", e, err)
	}
}
