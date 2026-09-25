package curator

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"imbue/internal/config"
	"imbue/internal/evidence"
	"imbue/internal/local"
	"imbue/internal/store"
	"imbue/internal/store/db"
)

type fakeRunner struct {
	calls int
	hook  func()
	bad   bool
}

func (f *fakeRunner) Run(_ context.Context, p Packet) (Result, json.RawMessage, error) {
	f.calls++
	if f.hook != nil {
		f.hook()
	}
	id := p.AllowedEvidenceIDs[0]
	for _, ev := range p.Events {
		if ev.Kind == "user.message" {
			id = ev.ID
			break
		}
	}
	if f.bad {
		id = "invented"
	}
	return Result{Patches: []Patch{{Operation: "create", Changes: Changes{Experience: testExperience(id), Title: "変更範囲", Relation: "preference_refinement", Repository: p.Repository, Judgment: "依頼した変更の範囲を守る", Conditions: []string{}, Exceptions: []string{}, EligibilityCandidates: []string{"Eval"}, Status: "active"}, GroundedInEventIDs: []string{id}}}}, json.RawMessage(`{"input_tokens":1,"output_tokens":1}`), nil
}
func integrationWorker(t *testing.T) (*Worker, *fakeRunner) {
	t.Helper()
	home := os.Getenv("IMBUE_TEST_HOME")
	if home == "" {
		t.Skip("set IMBUE_TEST_HOME to a configured local PostgreSQL instance")
	}
	c, e := config.Load(home)
	if e != nil {
		t.Fatal(e)
	}
	dsn, e := c.DSN()
	if e != nil {
		t.Fatal(e)
	}
	ctx := context.Background()
	admin, e := pgxpool.New(ctx, dsn)
	if e != nil {
		t.Fatal(e)
	}
	schema := "test_" + local.ID()
	if _, e = admin.Exec(ctx, "CREATE SCHEMA "+schema); e != nil {
		admin.Close()
		t.Fatal(e)
	}
	pc, e := pgxpool.ParseConfig(dsn)
	if e != nil {
		t.Fatal(e)
	}
	pc.ConnConfig.RuntimeParams["search_path"] = schema
	p, e := pgxpool.NewWithConfig(ctx, pc)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { p.Close(); admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE"); admin.Close() })
	c.Root = t.TempDir()
	c.Repository = "/fixture/repository"
	c.CuratorEnabled = true
	s := &store.Store{Pool: p, Q: db.New(p), Root: c.Root}
	if e = s.Migrate(ctx); e != nil {
		t.Fatal(e)
	}
	f := &fakeRunner{}
	return &Worker{Config: c, Store: s, Runner: f}, f
}
func addTurns(t *testing.T, w *Worker, conversation string, start, count int) {
	t.Helper()
	batch := evidence.Batch{Conversation: evidence.Conversation{ID: conversation, Repository: w.Config.Repository, SourcePath: "fixture.jsonl", CodexVersion: "fixture"}}
	for i := start; i < start+count; i++ {
		turn := fmt.Sprintf("t-%d", i)
		for j, kind := range []string{"turn.started", "user.message", "tool.command", "agent.message", "turn.completed"} {
			id := fmt.Sprintf("%s-%s-%d", conversation, turn, j)
			batch.Events = append(batch.Events, evidence.Event{ID: id, ConversationID: conversation, TurnID: turn, Kind: kind, OccurredAt: "2026-09-17T00:00:00Z", SourceOffset: int64(i*100 + j), Payload: json.RawMessage(`{"text":"依頼の範囲だけ修正する"}`)})
		}
	}
	if e := w.Store.Ingest(context.Background(), batch); e != nil {
		t.Fatal(e)
	}
}
func TestIntegrationThresholdIsolationReplayAndFrozenRange(t *testing.T) {
	w, f := integrationWorker(t)
	ctx := context.Background()
	addTurns(t, w, "a", 1, 9)
	addTurns(t, w, "b", 1, 9)
	if _, e := w.Store.Pool.Exec(ctx, "UPDATE conversations SET repository='/fixture/other' WHERE id='b'"); e != nil {
		t.Fatal(e)
	}
	addTurns(t, w, "a", 1, 9)
	if e := w.EnqueueReady(ctx); e != nil {
		t.Fatal(e)
	}
	jobs, _ := w.Store.Q.ListJobs(ctx)
	if len(jobs) != 0 {
		t.Fatal("cross-conversation count or duplicate count triggered")
	}
	cs, _ := w.Store.Q.ListConversations(ctx)
	for _, c := range cs {
		if c.CompletedTurns != 9 {
			t.Fatalf("duplicate turns: %+v", c)
		}
	}
	addTurns(t, w, "a", 10, 1)
	w.Config.CuratorEnabled = false
	w.EnqueueReady(ctx)
	jobs, _ = w.Store.Q.ListJobs(ctx)
	if len(jobs) != 0 {
		t.Fatal("disabled curator launched at ten")
	}
	w.Config.CuratorEnabled = true
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, e := w.Enqueue(ctx, "a", false); e != nil {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
	jobs, _ = w.Store.Q.ListJobs(ctx)
	if len(jobs) != 1 {
		t.Fatalf("duplicate job: %d", len(jobs))
	}
	through := jobs[0].ThroughSeq
	f.hook = func() { addTurns(t, w, "a", 11, 1) }
	ran, e := w.RunOnce(ctx)
	if e != nil || !ran {
		t.Fatalf("run: %v %v", ran, e)
	}
	c, _ := w.Store.Q.GetConversation(ctx, "a")
	if c.ProcessedSeq != through {
		t.Fatal("processed position crossed frozen job boundary")
	}
	cs, _ = w.Store.Q.ListConversations(ctx)
	for _, c := range cs {
		if c.ID == "a" && c.PendingTurns != 1 {
			t.Fatalf("new turn consumed: %+v", c)
		}
	}
	if e = w.EnqueueReady(ctx); e != nil {
		t.Fatal(e)
	}
	jobs, _ = w.Store.Q.ListJobs(ctx)
	if len(jobs) != 1 {
		t.Fatal("one pending turn triggered")
	}
	cases, e := w.Store.Q.ListCases(ctx)
	if e != nil || len(cases) != 1 {
		t.Fatalf("cases: %v %v", cases, e)
	}
	if f.calls != 1 {
		t.Fatal("multiple Luna calls")
	}
	if e = w.EditCase(ctx, cases[0].ID, Edit{BaseVersion: 1, Judgment: "限定した変更を優先する", Status: "held", Reason: "本人による訂正"}); e != nil {
		t.Fatal(e)
	}
	if e = w.EditCase(ctx, cases[0].ID, Edit{BaseVersion: 1, Status: "active", Reason: "stale"}); e == nil {
		t.Fatal("stale update accepted")
	}
	h, _ := w.Store.Q.CaseHistory(ctx, cases[0].ID)
	if len(h) != 2 {
		t.Fatal("lost revision history")
	}
}

func TestIntegrationThresholdAggregatesOnlyWithinRepository(t *testing.T) {
	w, f := integrationWorker(t)
	ctx := context.Background()
	w.Config.ObserveAll = true
	addTurns(t, w, "a-one", 1, 6)
	addTurns(t, w, "a-two", 1, 3)
	addTurns(t, w, "b-one", 1, 9)
	if _, e := w.Store.Pool.Exec(ctx, "UPDATE conversations SET repository='/fixture/other' WHERE id='b-one'"); e != nil {
		t.Fatal(e)
	}
	if e := w.EnqueueReady(ctx); e != nil {
		t.Fatal(e)
	}
	jobs, _ := w.Store.Q.ListJobs(ctx)
	if len(jobs) != 0 {
		t.Fatalf("turns from separate repositories were combined: %+v", jobs)
	}

	addTurns(t, w, "a-two", 4, 1)
	if e := w.EnqueueReady(ctx); e != nil {
		t.Fatal(e)
	}
	jobs, _ = w.Store.Q.ListJobs(ctx)
	if len(jobs) != 1 {
		t.Fatalf("ten turns in one repository did not create one job: %+v", jobs)
	}
	p, e := w.Packet(ctx, jobs[0])
	if e != nil {
		t.Fatal(e)
	}
	conversations := map[string]bool{}
	for _, ev := range p.Events {
		conversations[ev.ConversationID] = true
		if ev.Kind == "tool.command" {
			t.Fatal("command crossed the Luna boundary")
		}
	}
	if !conversations["a-one"] || !conversations["a-two"] || conversations["b-one"] {
		t.Fatalf("wrong repository packet membership: %+v", conversations)
	}
	if ran, e := w.RunOnce(ctx); e != nil || !ran {
		t.Fatalf("workspace run: %t %v", ran, e)
	}
	if f.calls != 1 {
		t.Fatalf("expected one Luna call, got %d", f.calls)
	}
	for _, id := range []string{"a-one", "a-two"} {
		c, _ := w.Store.Q.GetConversation(ctx, id)
		if c.ProcessedSeq != jobs[0].ThroughSeq {
			t.Fatalf("workspace cursor for %s was not advanced", id)
		}
	}
	b, _ := w.Store.Q.GetConversation(ctx, "b-one")
	if b.ProcessedSeq != 0 {
		t.Fatal("separate repository cursor was advanced")
	}
}
func TestIntegrationObserveAllAllowsSeparateRepositories(t *testing.T) {
	w, _ := integrationWorker(t)
	w.Config.ObserveAll = true
	addTurns(t, w, "other-repository", 1, 10)
	if _, e := w.Store.Pool.Exec(context.Background(), "UPDATE conversations SET repository='/another/codex/workspace' WHERE id='other-repository'"); e != nil {
		t.Fatal(e)
	}
	if e := w.EnqueueReady(context.Background()); e != nil {
		t.Fatal(e)
	}
	jobs, e := w.Store.Q.ListJobs(context.Background())
	if e != nil || len(jobs) != 1 {
		t.Fatalf("workspace was not eligible: %+v %v", jobs, e)
	}
}
func TestIntegrationFailureLeaseAndManualSmallBatch(t *testing.T) {
	w, f := integrationWorker(t)
	ctx := context.Background()
	addTurns(t, w, "a", 1, 2)
	id, e := w.Enqueue(ctx, "a", true)
	if e != nil || id == "" {
		t.Fatal(e)
	}
	f.bad = true
	if _, e = w.RunOnce(ctx); e == nil {
		t.Fatal("hallucinated evidence accepted")
	}
	c, _ := w.Store.Q.GetConversation(ctx, "a")
	if c.ProcessedSeq != 0 {
		t.Fatal("failure advanced cursor")
	}
	cases, _ := w.Store.Q.ListCases(ctx)
	if len(cases) != 0 {
		t.Fatal("partial result committed")
	}
	w.Store.Pool.Exec(ctx, "UPDATE jobs SET status='running', lease_token='old', lease_until=now()-interval '1 second' WHERE id=$1", id)
	f.bad = false
	if _, e = w.RunOnce(ctx); e != nil {
		t.Fatal(e)
	}
	jobs, _ := w.Store.Q.ListJobs(ctx)
	if jobs[0].Status != "succeeded" || jobs[0].Attempt != 2 {
		t.Fatalf("lease recovery failed: %+v", jobs[0])
	}
	// Replayed completed turns never become pending after success.
	addTurns(t, w, "a", 1, 2)
	cs, _ := w.Store.Q.ListConversations(ctx)
	if cs[0].PendingTurns != 0 {
		t.Fatal("replay moved completion position")
	}
}
func TestSplitPreservesEveryByte(t *testing.T) {
	payload, _ := json.Marshal(map[string]string{"text": strings.Repeat("日本語のコードと検証結果", 3000)})
	p := Packet{Repository: "/repo", Events: []evidence.Event{{ID: "a", TurnID: "turn", Payload: payload}}, AllowedEvidenceIDs: []string{"a"}}
	parts, e := Split(p, 4096)
	if e != nil {
		t.Fatal(e)
	}
	if len(parts) < 2 {
		t.Fatal("not split")
	}
	var combined strings.Builder
	for _, p := range parts {
		b, _ := json.Marshal(p)
		if len(b) > 4096 {
			t.Fatal("budget exceeded")
		}
		for _, ev := range p.Events {
			if ev.ID != "a" || ev.TurnID != "turn" {
				t.Fatal("lost relation")
			}
			var v struct {
				Text string `json:"original_payload_json_fragment"`
			}
			json.Unmarshal(ev.Payload, &v)
			combined.WriteString(v.Text)
		}
	}
	if combined.String() != string(payload) {
		t.Fatal("evidence truncated")
	}
}
func TestCodexArgsIsolated(t *testing.T) {
	s := strings.Join(Args(filepath.Join(t.TempDir(), "run")), " ")
	for _, required := range []string{"app-server --stdio", "cli_auth_credentials_store=\"file\"", "model_provider=\"openai\"", "--disable plugins", "--disable shell_tool", "project_doc_max_bytes=0", "forced_login_method=\"chatgpt\""} {
		if !strings.Contains(s, required) {
			t.Errorf("missing %s", required)
		}
	}
}

func TestIntegrationLiveLunaAtTenTurns(t *testing.T) {
	if os.Getenv("IMBUE_LIVE_LUNA") != "1" {
		t.Skip("explicit opt-in: consumes ChatGPT usage")
	}
	w, _ := integrationWorker(t)
	w.Runner = CodexRunner{Config: w.Config}
	os.MkdirAll(filepath.Join(w.Config.Root, "curator"), 0700)
	addTurns(t, w, "live-ten", 1, 9)
	ctx := context.Background()
	if e := w.EnqueueReady(ctx); e != nil {
		t.Fatal(e)
	}
	jobs, _ := w.Store.Q.ListJobs(ctx)
	if len(jobs) != 0 {
		t.Fatal("Luna triggered before ten turns")
	}
	addTurns(t, w, "live-ten", 10, 1)
	if e := w.EnqueueReady(ctx); e != nil {
		t.Fatal(e)
	}
	jobs, _ = w.Store.Q.ListJobs(ctx)
	if len(jobs) != 1 {
		t.Fatal("ten turns did not queue")
	}
	if ran, e := w.RunOnce(ctx); !ran || e != nil {
		t.Fatalf("live run: %t %v", ran, e)
	}
	jobs, _ = w.Store.Q.ListJobs(ctx)
	if jobs[0].Status != "succeeded" {
		t.Fatalf("job not saved: %+v", jobs[0])
	}
	cases, e := w.Store.Q.ListCases(ctx)
	if e != nil || len(cases) == 0 {
		t.Fatalf("no grounded cases: %v", e)
	}
	b, e := local.ReadBlob(w.Config.Root, jobs[0].ResultHash.String)
	if e != nil {
		t.Fatal(e)
	}
	var audit struct {
		Usage []struct {
			ThreadID string `json:"runtime_thread_id"`
			Email    string `json:"verified_account_email"`
		} `json:"usage"`
	}
	if e = json.Unmarshal(b, &audit); e != nil {
		t.Fatal(e)
	}
	for _, u := range audit.Usage {
		if u.Email != w.Config.CuratorAccountEmail {
			t.Fatal("account pin not verified")
		}
		if u.ThreadID == "" {
			t.Fatal("missing Codex run ID")
		}
		for _, dir := range []string{"sessions", "archived_sessions"} {
			filepath.WalkDir(filepath.Join(w.Config.CuratorCodexHome, dir), func(path string, d os.DirEntry, err error) error {
				if err == nil && !d.IsDir() && strings.Contains(d.Name(), u.ThreadID) {
					t.Error("ephemeral run persisted a rollout")
				}
				return nil
			})
		}
	}
	t.Logf("Luna executed at ten turns: status=%s cases=%d processed_through=%d; no runtime rollout persisted", jobs[0].Status, len(cases), jobs[0].ThroughSeq)
}
