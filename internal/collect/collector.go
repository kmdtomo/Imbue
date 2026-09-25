// Package collect observes persisted Codex rollouts without modifying Codex configuration.
// This adapter is intentionally versioned: rollout JSONL is not a public stable API.
package collect

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"imbue/internal/config"
	"imbue/internal/evidence"
	"imbue/internal/local"
	"imbue/internal/redact"
)

const SupportedVersion = "0.155.0-alpha.2.6"
const LegacySupportedVersion = "0.154.0-alpha.6.2"
const maxLine = 32 * 1024 * 1024

type Cursor struct {
	Offset   int64  `json:"offset"`
	TurnID   string `json:"turn_id"`
	MetaHash string `json:"meta_hash"`
	Ignored  bool   `json:"ignored,omitempty"`
}
type Collector struct {
	Config   config.Config
	Redactor *redact.Redactor
	skipped  map[string]bool
}
type Report struct {
	Files    int      `json:"files"`
	Events   int      `json:"events"`
	Batches  int      `json:"batches"`
	Warnings []string `json:"warnings"`
}

func New(c config.Config) (*Collector, error) {
	r, e := redact.New(c.RedactPatterns, c.ExcludePaths)
	return &Collector{Config: c, Redactor: r, skipped: map[string]bool{}}, e
}
func supportedVersion(v string) bool { return v == SupportedVersion || v == LegacySupportedVersion }
func Lock(root, name string) (*os.File, error) {
	if e := os.MkdirAll(filepath.Join(root, "state"), 0700); e != nil {
		return nil, e
	}
	f, e := os.OpenFile(filepath.Join(root, "state", name+".lock"), os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return nil, e
	}
	if e = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
		f.Close()
		return nil, fmt.Errorf("%s is already running", name)
	}
	return f, nil
}
func Unlock(f *os.File) { syscall.Flock(int(f.Fd()), syscall.LOCK_UN); f.Close() }
func (c *Collector) Scan(ctx context.Context) (Report, error) {
	report := Report{Warnings: []string{}}
	lock, e := Lock(c.Config.Root, "collector")
	if e != nil {
		return report, e
	}
	defer Unlock(lock)
	for _, dir := range []string{"sessions", "archived_sessions"} {
		root := filepath.Join(c.Config.CodexHome, dir)
		e = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			if err != nil {
				return err
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if d.IsDir() || !strings.HasPrefix(d.Name(), "rollout-") || !strings.HasSuffix(d.Name(), ".jsonl") {
				return nil
			}
			if c.skipped[path] {
				return nil
			}
			n, matched, e := c.read(path)
			if !matched && e == nil {
				c.skipped[path] = true
			}
			if matched {
				report.Files++
			}
			report.Events += n
			if n > 0 {
				report.Batches++
			}
			if e != nil {
				report.Warnings = append(report.Warnings, fmt.Sprintf("%s: %s", filepath.Base(path), e))
			}
			return nil
		})
		if e != nil {
			return report, e
		}
	}
	return report, nil
}
func readLine(r *bufio.Reader) ([]byte, error) {
	var line []byte
	for {
		part, e := r.ReadSlice('\n')
		if len(line)+len(part) > maxLine {
			return nil, fmt.Errorf("rollout record exceeds %d bytes; not consumed", maxLine)
		}
		line = append(line, part...)
		if e == bufio.ErrBufferFull {
			continue
		}
		return line, e
	}
}
func (c *Collector) read(path string) (int, bool, error) {
	f, e := os.Open(path)
	if e != nil {
		return 0, false, e
	}
	defer f.Close()
	r := bufio.NewReader(f)
	first, e := readLine(r)
	if e == io.EOF {
		return 0, false, nil
	}
	if e != nil {
		return 0, false, e
	}
	var meta struct {
		Timestamp string `json:"timestamp"`
		Type      string `json:"type"`
		Payload   struct {
			ID           string          `json:"id"`
			CWD          string          `json:"cwd"`
			Version      string          `json:"cli_version"`
			Source       json.RawMessage `json:"source"`
			ThreadSource json.RawMessage `json:"thread_source"`
		} `json:"payload"`
	}
	if e = json.Unmarshal(first, &meta); e != nil {
		return 0, false, e
	}
	if meta.Type != "session_meta" {
		return 0, false, fmt.Errorf("missing session_meta")
	}
	if !filepath.IsAbs(meta.Payload.CWD) {
		return 0, false, nil
	}
	cwd, e := filepath.EvalSymlinks(meta.Payload.CWD)
	if e != nil || (!c.Config.ObserveAll && filepath.Clean(cwd) != c.Config.Repository) {
		return 0, false, nil
	}
	for _, id := range c.Config.ExcludeSessions {
		if meta.Payload.ID == id {
			return 0, false, nil
		}
	}
	// A subagent's source is an object; never turn its messages into owner turns.
	if len(meta.Payload.Source) > 0 && meta.Payload.Source[0] == '{' {
		return 0, false, nil
	}
	if strings.Contains(strings.ToLower(string(meta.Payload.ThreadSource)), "curator") || strings.Contains(strings.ToLower(string(meta.Payload.ThreadSource)), "subagent") {
		return 0, false, nil
	}
	sourceID := local.Hash(first)
	// One Codex conversation can be resumed into several rollout segments. The
	// session metadata is stable when a segment is moved to archived_sessions,
	// and distinct between resumed segments, so it is the durable source key.
	curPath := filepath.Join(c.Config.Root, "state", "cursors", sourceID+".json")
	cur := Cursor{MetaHash: local.Hash(first)}
	curExists := false
	if b, e := os.ReadFile(curPath); e == nil {
		curExists = true
		if e = json.Unmarshal(b, &cur); e != nil {
			return 0, true, e
		}
	} else if !os.IsNotExist(e) {
		return 0, true, e
	}
	if cur.Ignored {
		return 0, false, nil
	}
	if !curExists && c.Config.ObserveAll && c.Config.ObserveAllSince != "" {
		since, sinceErr := time.Parse(time.RFC3339Nano, c.Config.ObserveAllSince)
		started, startedErr := time.Parse(time.RFC3339Nano, meta.Timestamp)
		if sinceErr != nil {
			return 0, true, fmt.Errorf("invalid observe_all_since")
		}
		if startedErr == nil && started.Before(since) {
			st, statErr := f.Stat()
			if statErr != nil {
				return 0, true, statErr
			}
			cur.Offset = st.Size()
			cur.Ignored = !supportedVersion(meta.Payload.Version)
			b, marshalErr := json.Marshal(cur)
			if marshalErr != nil {
				return 0, true, marshalErr
			}
			if e := local.AtomicWrite(curPath, b); e != nil {
				return 0, true, e
			}
			return 0, !cur.Ignored, nil
		}
	}
	if !supportedVersion(meta.Payload.Version) {
		return 0, true, fmt.Errorf("unsupported Codex rollout version %s (validated: %s, %s)", meta.Payload.Version, LegacySupportedVersion, SupportedVersion)
	}
	st, e := f.Stat()
	if e != nil {
		return 0, true, e
	}
	if cur.MetaHash != local.Hash(first) || cur.Offset > st.Size() {
		return 0, true, fmt.Errorf("source rewritten/truncated; cursor preserved for inspection")
	}
	if _, e = f.Seek(cur.Offset, io.SeekStart); e != nil {
		return 0, true, e
	}
	r.Reset(f)
	batch := evidence.Batch{Conversation: evidence.Conversation{ID: meta.Payload.ID, Repository: cwd, SourcePath: path, CodexVersion: meta.Payload.Version}, Events: []evidence.Event{}}
	// Bounded batches allow DB outages without unbounded memory growth.
	bytesRead := 0
	for bytesRead < 4*1024*1024 {
		line, e := readLine(r)
		if e == io.EOF {
			break
		}
		if e != nil {
			return 0, true, e
		}
		ev, e := c.normalize(line, meta.Payload.ID, sourceID, cur.Offset, &cur)
		if e != nil {
			return 0, true, fmt.Errorf("offset %d: %w", cur.Offset, e)
		}
		if ev != nil {
			batch.Events = append(batch.Events, *ev)
		}
		cur.Offset += int64(len(line))
		bytesRead += len(line)
	}
	if len(batch.Events) > 0 {
		b, e := json.Marshal(batch)
		if e != nil {
			return 0, true, e
		}
		// Publish spool before cursor: replay after a crash is harmless (event IDs are stable).
		name := fmt.Sprintf("%s-%020d-%s.json", local.Hash([]byte(meta.Payload.ID))[:16], batch.Events[0].SourceOffset, local.Hash(b))
		if e = local.AtomicWrite(filepath.Join(c.Config.Root, "spool", "events", name), b); e != nil {
			return 0, true, e
		}
	}
	b, e := json.Marshal(cur)
	if e != nil {
		return 0, true, e
	}
	return len(batch.Events), true, local.AtomicWrite(curPath, b)
}
func (c *Collector) normalize(line []byte, conversation, sourceID string, offset int64, cur *Cursor) (*evidence.Event, error) {
	var o struct {
		Timestamp string          `json:"timestamp"`
		Type      string          `json:"type"`
		Payload   json.RawMessage `json:"payload"`
	}
	if e := json.Unmarshal(line, &o); e != nil {
		return nil, e
	}
	if o.Type != "event_msg" {
		return nil, nil
	}
	var p struct {
		Type   string          `json:"type"`
		TurnID string          `json:"turn_id"`
		Item   json.RawMessage `json:"item"`
	}
	if e := json.Unmarshal(o.Payload, &p); e != nil {
		return nil, e
	}
	kind := ""
	payload := o.Payload
	turn := p.TurnID
	switch p.Type {
	case "task_started":
		kind = "turn.started"
		cur.TurnID = turn
		payload, _ = json.Marshal(map[string]string{"turn_id": turn})
	case "task_complete":
		kind = "turn.completed"
		payload, _ = json.Marshal(map[string]string{"turn_id": turn})
		if turn == cur.TurnID {
			cur.TurnID = ""
		}
	case "turn_aborted":
		kind = "turn.aborted"
		if turn == "" {
			turn = cur.TurnID
		}
		cur.TurnID = ""
		payload, _ = json.Marshal(map[string]string{"turn_id": turn})
	case "item_completed":
		var item map[string]json.RawMessage
		if e := json.Unmarshal(p.Item, &item); e != nil {
			return nil, e
		}
		var typ string
		json.Unmarshal(item["type"], &typ)
		switch typ {
		case "UserMessage":
			kind = "user.message"
		case "AgentMessage":
			kind = "agent.message"
		case "CommandExecution":
			kind = "tool.command"
		case "FileChange":
			kind = "artifact.diff"
		case "McpToolCall", "DynamicToolCall", "WebSearch":
			kind = "tool.call"
		default:
			return nil, nil
		}
		if turn == "" {
			turn = cur.TurnID
		}
		payload = p.Item
		if c.Redactor.Excluded(string(payload)) {
			kind = "evidence.excluded"
			payload = []byte(`{"reason":"excluded path; entire item omitted"}`)
		}
	default:
		return nil, nil
	}
	if turn == "" && strings.HasPrefix(kind, "turn.") {
		kind = "boundary.missing"
	}
	b, e := c.Redactor.JSON(payload)
	if e != nil {
		return nil, e
	}
	// Source identity is independent of secret values and redaction configuration.
	id := local.Hash([]byte(fmt.Sprintf("codex-rollout-v2:%s:%s:%d", conversation, sourceID, offset)))
	return &evidence.Event{ID: id, ConversationID: conversation, TurnID: turn, Kind: kind, OccurredAt: o.Timestamp, SourceOffset: offset, Payload: b}, nil
}

type Sink interface {
	Ingest(context.Context, evidence.Batch) error
}

func Flush(ctx context.Context, root string, s Sink) (int, error) {
	lock, e := Lock(root, "flush")
	if e != nil {
		return 0, e
	}
	defer Unlock(lock)
	paths, e := filepath.Glob(filepath.Join(root, "spool", "events", "*.json"))
	if e != nil {
		return 0, e
	}
	n := 0
	for _, p := range paths {
		b, e := os.ReadFile(p)
		if e != nil {
			return n, e
		}
		if !strings.HasSuffix(filepath.Base(p), "-"+local.Hash(b)+".json") {
			return n, fmt.Errorf("spool integrity error")
		}
		var batch evidence.Batch
		if e = json.Unmarshal(b, &batch); e != nil {
			return n, e
		}
		if e = s.Ingest(ctx, batch); e != nil {
			return n, e
		}
		if e = os.Remove(p); e != nil {
			return n, e
		}
		n += len(batch.Events)
	}
	return n, nil
}
