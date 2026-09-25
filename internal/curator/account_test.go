package curator

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"imbue/internal/config"
)

type accountBlockedRunner struct{}

func (accountBlockedRunner) Run(context.Context, Packet) (Result, json.RawMessage, error) {
	return Result{}, nil, fmt.Errorf("blocked_by_account: account mismatch")
}

func TestIntegrationAccountMismatchHoldsJob(t *testing.T) {
	w, _ := integrationWorker(t)
	w.Runner = accountBlockedRunner{}
	w.Config.CuratorMaxAttempts = 1
	ctx := context.Background()
	addTurns(t, w, "account-held", 1, 10)
	if err := w.EnqueueReady(ctx); err != nil {
		t.Fatal(err)
	}
	if ran, err := w.RunOnce(ctx); !ran || err == nil {
		t.Fatalf("expected blocked run: %v %v", ran, err)
	}
	jobs, err := w.Store.Q.ListJobs(ctx)
	if err != nil || len(jobs) != 1 || jobs[0].Status != "blocked_by_account" {
		t.Fatalf("job not held: %v %v", jobs, err)
	}
	if _, err := w.Store.Pool.Exec(ctx, "UPDATE jobs SET next_run_at=now()-interval '1 hour'"); err != nil {
		t.Fatal(err)
	}
	w.Config.CuratorMaxAttempts = 3
	if ran, err := w.RunOnce(ctx); ran || err != nil {
		t.Fatalf("held job auto retried: %v %v", ran, err)
	}
	cs, err := w.Store.Q.ListConversations(ctx)
	if err != nil || len(cs) != 1 || cs[0].ProcessedSeq != 0 {
		t.Fatalf("advanced unprocessed evidence: %v %v", cs, err)
	}
	cases, err := w.Store.Q.ListCases(ctx)
	if err != nil || len(cases) != 0 {
		t.Fatalf("saved unverified result: %v %v", cases, err)
	}
}

type accountPeer struct {
	accounts      []string
	calls         []string
	notifications []rpcMessage
	armed         bool
}

func (p *accountPeer) Call(method string, params any) (json.RawMessage, error) {
	p.calls = append(p.calls, method)
	switch method {
	case "account/read":
		if len(p.accounts) == 0 {
			return nil, fmt.Errorf("unexpected account read")
		}
		r := p.accounts[0]
		p.accounts = p.accounts[1:]
		return json.RawMessage(r), nil
	case "thread/start":
		v := params.(map[string]any)
		if v["ephemeral"] != true || v["model"] != Model || v["modelProvider"] != "openai" {
			return nil, fmt.Errorf("unsafe thread")
		}
		return json.RawMessage(`{"thread":{"id":"thread"}}`), nil
	case "turn/start":
		if !p.armed {
			return nil, fmt.Errorf("unguarded dispatch")
		}
		return json.RawMessage(`{"turn":{"id":"turn"}}`), nil
	}
	return nil, fmt.Errorf("unexpected method")
}
func (p *accountPeer) Arm() { p.armed = true }
func (p *accountPeer) Next() (rpcMessage, error) {
	if len(p.notifications) == 0 {
		return rpcMessage{}, io.EOF
	}
	m := p.notifications[0]
	p.notifications = p.notifications[1:]
	return m, nil
}
func TestAccountPinGuardsDispatchAndResult(t *testing.T) {
	good := `{"account":{"type":"chatgpt","email":"owner@example.com"}}`
	bad := `{"account":{"type":"chatgpt","email":"other@example.com"}}`
	for _, tc := range []struct {
		name      string
		accounts  []string
		change    bool
		wantCalls string
		ok        bool
	}{
		{"wrong", []string{bad}, false, "account/read", false},
		{"logged-out", []string{`{"account":null}`}, false, "account/read", false},
		{"api-key", []string{`{"account":{"type":"apiKey"}}`}, false, "account/read", false},
		{"changes-before-dispatch", []string{good, bad}, false, "account/read,thread/start,account/read", false},
		{"changes-during-turn", []string{good, good}, true, "account/read,thread/start,account/read,turn/start", false},
		{"changes-after-turn", []string{good, good, bad}, false, "account/read,thread/start,account/read,turn/start,account/read", false},
		{"pinned", []string{good, good, good}, false, "account/read,thread/start,account/read,turn/start,account/read", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &accountPeer{accounts: tc.accounts, notifications: []rpcMessage{
				{Method: "item/completed", Params: json.RawMessage(`{"threadId":"thread","turnId":"turn","item":{"type":"agentMessage","phase":"final_answer","text":"{\"patches\":[]}"}}`)},
				{Method: "turn/completed", Params: json.RawMessage(`{"threadId":"thread","turn":{"id":"turn","status":"completed"}}`)},
			}}
			if tc.change {
				p.notifications = append([]rpcMessage{{Method: "account/updated", Params: json.RawMessage(`{}`)}}, p.notifications...)
			}
			r := CodexRunner{Config: config.Config{CuratorAccountEmail: "owner@example.com"}}
			_, audit, err := r.runVerified(p, t.TempDir(), Packet{})
			if tc.ok {
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(string(audit), `"verified_account_email":"owner@example.com"`) {
					t.Fatal("missing identity audit")
				}
			} else if err == nil || !strings.Contains(err.Error(), "blocked_by_account") || audit != nil {
				t.Fatalf("not held: %v %s", err, audit)
			}
			if strings.Join(p.calls, ",") != tc.wantCalls {
				t.Fatalf("unexpected dispatch: %v", p.calls)
			}
		})
	}
}
func TestCuratorEnvironmentDoesNotInheritWorkingCredentials(t *testing.T) {
	t.Setenv("CODEX_HOME", "/working")
	t.Setenv("OPENAI_API_KEY", "secret")
	t.Setenv("CODEX_API_KEY", "secret")
	t.Setenv("OPENAI_BASE_URL", "https://wrong.invalid")
	env := strings.Join(cleanEnv(config.Config{CodexHome: "/working", CuratorCodexHome: "/dedicated"}), "\n")
	if !strings.Contains(env, "CODEX_HOME=/dedicated") || strings.Contains(env, "/working") || strings.Contains(env, "secret") || strings.Contains(env, "wrong.invalid") {
		t.Fatal("inherited working credentials")
	}
}
func TestAccountConfigRejectsSharedCredentials(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "working")
	target := filepath.Join(root, "dedicated")
	os.MkdirAll(source, 0700)
	c := config.Config{CodexHome: source, CuratorCodexHome: target, CuratorAccountEmail: "owner@example.com"}
	if err := ValidateAccountConfig(c); err != nil {
		t.Fatal(err)
	}
	c.CuratorCodexHome = source
	if ValidateAccountConfig(c) == nil {
		t.Fatal("shared home accepted")
	}
	c.CuratorCodexHome = target
	if err := os.Symlink(filepath.Join(source, "auth.json"), filepath.Join(target, "auth.json")); err != nil {
		t.Fatal(err)
	}
	if ValidateAccountConfig(c) == nil {
		t.Fatal("credential symlink accepted")
	}
	c.CuratorAccountEmail = ""
	if ValidateAccountConfig(c) == nil {
		t.Fatal("missing pin accepted")
	}
}
