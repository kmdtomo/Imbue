package curator

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"syscall"
	"time"
)

type rpcMessage struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  json.RawMessage `json:"error,omitempty"`
}
type rpcRead struct {
	message rpcMessage
	err     error
}
type rpcPeer struct {
	ctx      context.Context
	cancel   context.CancelFunc
	cmd      *exec.Cmd
	stdin    io.WriteCloser
	incoming chan rpcRead
	pending  []rpcMessage
	seq      int
	guarded  bool
}

func startPeer(ctx context.Context, bin string, args, env []string, dir string) (*rpcPeer, error) {
	ctx, cancel := context.WithCancel(ctx)
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = env
	cmd.Dir = dir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = 2 * time.Second
	cmd.Cancel = func() error {
		if cmd.Process != nil {
			return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		return nil
	}
	stdout, e := cmd.StdoutPipe()
	if e != nil {
		cancel()
		return nil, e
	}
	stdin, e := cmd.StdinPipe()
	if e != nil {
		cancel()
		return nil, e
	}
	cmd.Stderr = io.Discard
	if e = cmd.Start(); e != nil {
		cancel()
		return nil, fmt.Errorf("blocked_by_codex: unable to start App Server")
	}
	p := &rpcPeer{ctx: ctx, cancel: cancel, cmd: cmd, stdin: stdin, incoming: make(chan rpcRead, 32)}
	go func() {
		defer close(p.incoming)
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 8192), 8*1024*1024)
		total := 0
		for scanner.Scan() {
			total += len(scanner.Bytes())
			var msg rpcMessage
			e := json.Unmarshal(scanner.Bytes(), &msg)
			if total > 16*1024*1024 {
				e = fmt.Errorf("Codex event budget exceeded")
			}
			select {
			case p.incoming <- rpcRead{msg, e}:
			case <-ctx.Done():
				return
			}
			if e != nil {
				return
			}
		}
		e := scanner.Err()
		if e == nil {
			e = io.EOF
		}
		select {
		case p.incoming <- rpcRead{err: e}:
		case <-ctx.Done():
		}
	}()
	return p, nil
}
func (p *rpcPeer) Close() { p.cancel(); p.stdin.Close(); p.cmd.Wait() }
func (p *rpcPeer) send(m rpcMessage) error {
	b, e := json.Marshal(m)
	if e != nil {
		return e
	}
	_, e = p.stdin.Write(append(b, '\n'))
	return e
}
func (p *rpcPeer) read() (rpcMessage, error) {
	select {
	case <-p.ctx.Done():
		return rpcMessage{}, p.ctx.Err()
	case v, ok := <-p.incoming:
		if !ok {
			return rpcMessage{}, io.EOF
		}
		if v.err != nil {
			return rpcMessage{}, v.err
		}
		if len(v.message.ID) > 0 && v.message.Method != "" {
			return rpcMessage{}, fmt.Errorf("blocked_by_codex: unexpected server request; no tool or credential fallback allowed")
		}
		if p.guarded {
			if e := guardNotification(v.message); e != nil {
				return rpcMessage{}, e
			}
		}
		return v.message, nil
	}
}
func (p *rpcPeer) Call(method string, params any) (json.RawMessage, error) {
	p.seq++
	id := strconv.Itoa(p.seq)
	b, e := json.Marshal(params)
	if e != nil {
		return nil, e
	}
	if e = p.send(rpcMessage{ID: json.RawMessage(id), Method: method, Params: b}); e != nil {
		return nil, e
	}
	for {
		m, e := p.read()
		if e != nil {
			return nil, e
		}
		if string(m.ID) == id {
			if len(m.Error) > 0 && string(m.Error) != "null" {
				return nil, fmt.Errorf("blocked_by_codex: %s failed", method)
			}
			return m.Result, nil
		}
		if m.Method != "" {
			p.pending = append(p.pending, m)
			if len(p.pending) > 10000 {
				return nil, fmt.Errorf("Codex notification budget exceeded")
			}
		}
	}
}
func (p *rpcPeer) Next() (rpcMessage, error) {
	if len(p.pending) > 0 {
		m := p.pending[0]
		p.pending = p.pending[1:]
		return m, nil
	}
	return p.read()
}
func (p *rpcPeer) Arm() {
	// Notifications preceding the final account check cannot describe the inference turn.
	p.pending = nil
	p.guarded = true
}
func guardNotification(m rpcMessage) error {
	switch m.Method {
	case "account/updated", "account/login/completed", "account/logout":
		return fmt.Errorf("blocked_by_account: authentication changed during curator execution")
	case "error", "model/rerouted":
		return fmt.Errorf("blocked_by_codex: runtime error or model reroute")
	}
	return nil
}
