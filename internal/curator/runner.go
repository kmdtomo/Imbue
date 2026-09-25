package curator

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"imbue/internal/collect"
	"imbue/internal/config"
	"imbue/internal/evidence"
	"imbue/internal/local"
	"imbue/internal/redact"
)

type Runner interface {
	Run(context.Context, Packet) (Result, json.RawMessage, error)
}
type CodexRunner struct{ Config config.Config }

// aliasEvidenceIDs keeps content-addressed evidence IDs out of model output.
// Short opaque references are easier to copy exactly; the trusted runtime restores
// the original IDs before returning a result to the worker and audit pipeline.
func aliasEvidenceIDs(p Packet) (Packet, map[string]string) {
	toAlias := map[string]string{}
	toOriginal := map[string]string{}
	next := 1
	alias := func(id string) string {
		if id == "" {
			return id
		}
		if v, ok := toAlias[id]; ok {
			return v
		}
		v := fmt.Sprintf("E%06d", next)
		next++
		toAlias[id] = v
		toOriginal[v] = id
		return v
	}
	p.Events = append([]evidence.Event(nil), p.Events...)
	for i := range p.Events {
		p.Events[i].ID = alias(p.Events[i].ID)
	}
	p.ContextEvents = append([]evidence.Event(nil), p.ContextEvents...)
	for i := range p.ContextEvents {
		p.ContextEvents[i].ID = alias(p.ContextEvents[i].ID)
	}
	p.AllowedEvidenceIDs = append([]string(nil), p.AllowedEvidenceIDs...)
	for i := range p.AllowedEvidenceIDs {
		p.AllowedEvidenceIDs[i] = alias(p.AllowedEvidenceIDs[i])
	}
	return p, toOriginal
}

func restoreEvidenceIDs(result *Result, aliases map[string]string) error {
	for i := range result.Patches {
		for j, id := range result.Patches[i].GroundedInEventIDs {
			original, ok := aliases[id]
			if !ok {
				return fmt.Errorf("unknown evidence alias: %s", id)
			}
			result.Patches[i].GroundedInEventIDs[j] = original
		}
		if x := result.Patches[i].Changes.Experience; x != nil {
			for k := range x.Observations {
				for j, id := range x.Observations[k].EventIDs {
					original, ok := aliases[id]
					if !ok {
						return fmt.Errorf("unknown observation evidence alias: %s", id)
					}
					x.Observations[k].EventIDs[j] = original
				}
			}
		}
	}
	return nil
}

// App Server keeps account verification and inference in one authenticated process.
func Args(dir string) []string {
	a := []string{"app-server", "--stdio"}
	for _, f := range []string{"apps", "plugins", "remote_plugin", "hooks", "memories", "multi_agent", "multi_agent_v2", "shell_tool", "unified_exec", "shell_snapshot", "browser_use", "browser_use_external", "computer_use", "in_app_browser", "image_generation", "view_image", "skill_search", "skill_mcp_dependency_install", "workspace_dependencies", "goals", "sleep_tool", "tool_suggest", "code_mode_host", "context_management"} {
		a = append(a, "--disable", f)
	}
	for _, c := range []string{`model_provider="openai"`, `cli_auth_credentials_store="file"`, `approval_policy="never"`, `forced_login_method="chatgpt"`, `web_search="disabled"`, `mcp_servers={}`, `project_doc_max_bytes=0`, `skills.bundled.enabled=false`, `skills.include_instructions=false`, `features.skip_host_skill_discovery=true`, `tools.update_plan.enabled=false`, `tools.experimental_request_user_input.enabled=false`, `history.persistence="none"`, `model_reasoning_effort="low"`, `features.rollout_budget.enabled=true`, `features.rollout_budget.limit_tokens=8192`, `features.rollout_budget.reminder_at_remaining_tokens=[2048]`, `features.rollout_budget.prefill_token_weight=0`, `features.rollout_budget.sampling_token_weight=1`, `model_instructions_file=` + fmt.Sprintf("%q", filepath.Join(dir, "instructions.md"))} {
		a = append(a, "-c", c)
	}
	return a
}
func cleanEnv(c config.Config) []string {
	out := []string{}
	for _, k := range []string{"HOME", "PATH", "TMPDIR", "LANG", "USER", "LOGNAME", "SSL_CERT_FILE", "SSL_CERT_DIR"} {
		if v, ok := os.LookupEnv(k); ok {
			out = append(out, k+"="+v)
		}
	}
	return append(out, "CODEX_HOME="+c.CuratorCodexHome)
}

func (r CodexRunner) openPeer(ctx context.Context) (*rpcPeer, string, error) {
	if e := os.MkdirAll(filepath.Join(r.Config.Root, "curator"), 0700); e != nil {
		return nil, "", e
	}
	dir, e := os.MkdirTemp(filepath.Join(r.Config.Root, "curator"), "run-")
	if e != nil {
		return nil, "", e
	}
	if e = local.AtomicWrite(filepath.Join(dir, "instructions.md"), []byte(Instructions)); e != nil {
		os.RemoveAll(dir)
		return nil, "", e
	}
	peer, e := startPeer(ctx, r.Config.CodexBinary, Args(dir), cleanEnv(r.Config), dir)
	if e != nil {
		os.RemoveAll(dir)
		return nil, "", e
	}
	if _, e = peer.Call("initialize", map[string]any{"clientInfo": map[string]any{"name": "imbue_curator", "version": "0.2.0"}}); e != nil {
		peer.Close()
		os.RemoveAll(dir)
		return nil, "", e
	}
	if e = peer.send(rpcMessage{Method: "initialized", Params: json.RawMessage(`{}`)}); e != nil {
		peer.Close()
		os.RemoveAll(dir)
		return nil, "", e
	}
	return peer, dir, nil
}

func (r CodexRunner) Run(ctx context.Context, p Packet) (Result, json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Duration(r.Config.CuratorTimeoutSeconds)*time.Second)
	defer cancel()
	if e := r.checkRuntime(ctx); e != nil {
		return Result{}, nil, e
	}
	lock, e := collect.Lock(r.Config.CuratorCodexHome, "auth")
	if e != nil {
		return Result{}, nil, e
	}
	defer collect.Unlock(lock)
	peer, dir, e := r.openPeer(ctx)
	if e != nil {
		return Result{}, nil, e
	}
	defer os.RemoveAll(dir)
	defer peer.Close()
	return r.runVerified(peer, dir, p)
}

type sessionPeer interface {
	Call(string, any) (json.RawMessage, error)
	Next() (rpcMessage, error)
	Arm()
}

func (r CodexRunner) runVerified(peer sessionPeer, dir string, p Packet) (Result, json.RawMessage, error) {
	// No thread or user evidence is sent until the execution process identifies itself.
	raw, e := peer.Call("account/read", map[string]any{"refreshToken": false})
	if e != nil {
		return Result{}, nil, e
	}
	account, e := verifyAccount(raw, r.Config.CuratorAccountEmail)
	if e != nil {
		return Result{}, nil, e
	}
	raw, e = peer.Call("thread/start", map[string]any{"model": Model, "modelProvider": "openai", "cwd": dir, "approvalPolicy": "never", "sandbox": "read-only", "ephemeral": true, "baseInstructions": Instructions, "threadSource": "imbue_curator"})
	if e != nil {
		return Result{}, nil, e
	}
	var thread struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	if e = json.Unmarshal(raw, &thread); e != nil || thread.Thread.ID == "" {
		return Result{}, nil, fmt.Errorf("invalid ephemeral thread response")
	}
	// Check again immediately before dispatch, then refuse account-change notifications.
	raw, e = peer.Call("account/read", map[string]any{"refreshToken": false})
	if e != nil {
		return Result{}, nil, e
	}
	if _, e = verifyAccount(raw, r.Config.CuratorAccountEmail); e != nil {
		return Result{}, nil, e
	}
	peer.Arm()
	cleanPacket, e := excludePacketImages(p)
	if e != nil {
		return Result{}, nil, e
	}
	modelPacket, aliases := aliasEvidenceIDs(cleanPacket)
	b, e := json.Marshal(modelInput(modelPacket))
	if e != nil {
		return Result{}, nil, e
	}
	red, e := redact.New(r.Config.RedactPatterns, r.Config.ExcludePaths)
	if e != nil {
		return Result{}, nil, e
	}
	b, e = red.JSON(b)
	if e != nil {
		return Result{}, nil, e
	}
	if r.Config.CuratorMaxPacketBytes > 0 && len(b) > r.Config.CuratorMaxPacketBytes {
		return Result{}, nil, fmt.Errorf("serialized model input exceeds packet budget")
	}
	var schema any
	if e = json.Unmarshal([]byte(Schema), &schema); e != nil {
		return Result{}, nil, e
	}
	raw, e = peer.Call("turn/start", map[string]any{"threadId": thread.Thread.ID, "input": []any{map[string]any{"type": "text", "text": string(b)}}, "model": Model, "effort": "low", "outputSchema": schema})
	if e != nil {
		return Result{}, nil, e
	}
	var started struct {
		Turn struct {
			ID string `json:"id"`
		} `json:"turn"`
	}
	if e = json.Unmarshal(raw, &started); e != nil || started.Turn.ID == "" {
		return Result{}, nil, fmt.Errorf("missing turn ID")
	}
	var output string
	var usage json.RawMessage
	events := map[string]int{}
	for {
		msg, e := peer.Next()
		if e != nil {
			return Result{}, nil, e
		}
		events[msg.Method]++
		var v struct {
			ThreadID string `json:"threadId"`
			TurnID   string `json:"turnId"`
			Item     struct {
				Type  string `json:"type"`
				Text  string `json:"text"`
				Phase string `json:"phase"`
			} `json:"item"`
			Turn struct {
				ID     string `json:"id"`
				Status string `json:"status"`
			} `json:"turn"`
		}
		if e = json.Unmarshal(msg.Params, &v); e != nil {
			return Result{}, nil, fmt.Errorf("invalid Codex notification")
		}
		if e = guardNotification(msg); e != nil {
			return Result{}, nil, e
		}
		if v.ThreadID != "" && v.ThreadID != thread.Thread.ID {
			continue
		}
		if v.TurnID != "" && v.TurnID != started.Turn.ID {
			continue
		}
		switch msg.Method {
		case "item/started", "item/completed":
			if !oneOf(v.Item.Type, "userMessage", "agentMessage", "reasoning") {
				return Result{}, nil, fmt.Errorf("curator attempted forbidden tool")
			}
			if msg.Method == "item/completed" && v.Item.Type == "agentMessage" && v.Item.Phase != "commentary" {
				output = v.Item.Text
				if len(output) > 1024*1024 {
					return Result{}, nil, fmt.Errorf("curator output exceeded limit")
				}
			}
		case "thread/tokenUsage/updated":
			usage = append(json.RawMessage(nil), msg.Params...)
		case "turn/completed":
			if v.Turn.ID != started.Turn.ID {
				continue
			}
			if v.Turn.Status != "completed" {
				return Result{}, nil, fmt.Errorf("blocked_by_codex: turn did not complete successfully")
			}
			// A final identity mismatch invalidates the result as well.
			raw, e = peer.Call("account/read", map[string]any{"refreshToken": false})
			if e != nil {
				return Result{}, nil, e
			}
			if _, e = verifyAccount(raw, r.Config.CuratorAccountEmail); e != nil {
				return Result{}, nil, e
			}
			body, e := red.JSON([]byte(output))
			if e != nil {
				return Result{}, nil, e
			}
			dec := json.NewDecoder(bytes.NewReader(body))
			dec.DisallowUnknownFields()
			var result Result
			if e = dec.Decode(&result); e != nil {
				return Result{}, nil, e
			}
			if result.Patches == nil {
				return Result{}, nil, fmt.Errorf("missing patches")
			}
			var extra any
			if dec.Decode(&extra) != io.EOF {
				return Result{}, nil, fmt.Errorf("trailing output")
			}
			audit, _ := json.Marshal(map[string]any{"tokens": usage, "input_format": "imbue-raw-timeline-2", "input_bytes": len(b), "event_types": events, "runtime_thread_id": thread.Thread.ID, "codex_version": RuntimeVersion, "verified_account_email": account.Email, "account_policy": "dedicated-home-email-pin-v1"})
			if e = Validate(result, modelPacket); e != nil {
				return Result{}, nil, e
			}
			if e = restoreEvidenceIDs(&result, aliases); e != nil {
				return Result{}, nil, e
			}
			return result, audit, Validate(result, p)
		}
	}
}
