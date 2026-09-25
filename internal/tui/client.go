package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"

	"imbue/internal/collect"
	"imbue/internal/config"
	"imbue/internal/curator"
	"imbue/internal/daemon"
	"imbue/internal/evidence"
	"imbue/internal/store/db"
)

type status struct {
	Running          bool                      `json:"running"`
	Repository       string                    `json:"repository"`
	ObserveAll       bool                      `json:"observe_all"`
	ObserveAllSince  string                    `json:"observe_all_since"`
	CuratorEnabled   bool                      `json:"curator_enabled"`
	CuratorThreshold int                       `json:"curator_threshold"`
	AccountEmail     string                    `json:"curator_account_email"`
	Conversations    []db.ListConversationsRow `json:"conversations"`
	BufferedBatches  int                       `json:"buffered_batches"`
	LastError        string                    `json:"last_error"`
	LastScan         collect.Report            `json:"last_scan"`
}
type snapshot struct {
	Status status
	Cases  []db.LearningCase
	Jobs   []db.Job
	At     time.Time
}
type client struct{ config config.Config }

func (c client) snapshot(ctx context.Context) (snapshot, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var s snapshot
	for _, request := range []struct {
		path   string
		target any
	}{
		{"/status", &s.Status}, {"/cases", &s.Cases}, {"/jobs", &s.Jobs},
	} {
		b, err := daemon.Request(ctx, c.config, "GET", request.path, nil)
		if err != nil {
			return snapshot{}, err
		}
		if err = json.Unmarshal(b, request.target); err != nil {
			return snapshot{}, err
		}
	}
	s.At = time.Now()
	return s, nil
}

func (c client) start(ctx context.Context) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "--home", c.config.Root, "start")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("起動できませんでした。Docker Desktopの起動を確認してください。\n%s", strings.TrimSpace(string(output)))
	}
	return nil
}

func (c client) verify(ctx context.Context) (string, error) {
	a, err := (curator.CodexRunner{Config: c.config}).Account(ctx)
	return a.Email, err
}

func (c client) evidence(ctx context.Context, ids []string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	var out strings.Builder
	for _, id := range ids {
		b, err := daemon.Request(ctx, c.config, "GET", "/evidence/"+url.PathEscape(id), nil)
		if err != nil {
			return "", err
		}
		var v evidence.Event
		if err = json.Unmarshal(b, &v); err != nil {
			return "", err
		}
		body, err := evidenceText(v.Payload)
		if err != nil {
			return "", err
		}
		kind := map[string]string{"user.message": "ユーザーの発言", "agent.message": "エージェントの回答", "agent.progress": "エージェントの進捗", "tool.command": "コマンドの記録", "tool.result": "ツールの結果"}[v.Kind]
		if kind == "" {
			kind = v.Kind
		}
		at := v.OccurredAt
		if t, e := time.Parse(time.RFC3339Nano, at); e == nil {
			at = t.Local().Format("2006-01-02 15:04:05")
		}
		fmt.Fprintf(&out, "%s · %s\n\n%s\n\n根拠ID: %s\n\n", kind, at, body, id)
	}
	return out.String(), nil
}

func evidenceText(raw json.RawMessage) (string, error) {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return "", err
	}
	var extract func(any) string
	extract = func(v any) string {
		switch value := v.(type) {
		case string:
			return value
		case []any:
			var parts []string
			for _, part := range value {
				if s := extract(part); s != "" {
					parts = append(parts, s)
				}
			}
			return strings.Join(parts, "\n")
		case map[string]any:
			for _, key := range []string{"text", "content", "message", "output", "aggregated_output"} {
				if child, ok := value[key]; ok {
					if s := extract(child); s != "" {
						return s
					}
				}
			}
		}
		return ""
	}
	if text := extract(v); text != "" {
		return text, nil
	}
	b, err := json.MarshalIndent(v, "", "  ")
	return string(b), err
}
