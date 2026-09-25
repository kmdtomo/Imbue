package curator

import (
	"context"
	"encoding/json"
	"fmt"
	"net/mail"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"imbue/internal/collect"
	"imbue/internal/config"
)

type Account struct {
	Type  string `json:"type"`
	Email string `json:"email"`
}

const RuntimeVersion = "0.155.0-alpha.2.6"

type accountResponse struct {
	Account *Account `json:"account"`
}

func verifyAccount(raw json.RawMessage, expected string) (Account, error) {
	var r accountResponse
	if json.Unmarshal(raw, &r) != nil || r.Account == nil || r.Account.Type != "chatgpt" || r.Account.Email == "" {
		return Account{}, fmt.Errorf("blocked_by_account: verified ChatGPT account required")
	}
	if expected == "" || !strings.EqualFold(strings.TrimSpace(r.Account.Email), expected) {
		return Account{}, fmt.Errorf("blocked_by_account: account does not match the pinned email")
	}
	return *r.Account, nil
}

// ValidateAccountConfig fails closed for old configurations and shared/symlinked homes.
// Credentials stay under Codex's control. No auth.json content is read by Imbue.
func ValidateAccountConfig(c config.Config) error {
	a, e := mail.ParseAddress(c.CuratorAccountEmail)
	if e != nil || a.Address != c.CuratorAccountEmail || a.Name != "" {
		return fmt.Errorf("blocked_by_account: curator_account_email must be an explicit email address")
	}
	if !filepath.IsAbs(c.CuratorCodexHome) {
		return fmt.Errorf("blocked_by_account: curator_codex_home must be absolute")
	}
	if e = os.MkdirAll(c.CuratorCodexHome, 0700); e != nil {
		return e
	}
	source, e := filepath.EvalSymlinks(c.CodexHome)
	if e != nil {
		return fmt.Errorf("blocked_by_account: cannot resolve collection home")
	}
	target, e := filepath.EvalSymlinks(c.CuratorCodexHome)
	if e != nil {
		return fmt.Errorf("blocked_by_account: cannot resolve curator home")
	}
	rel, e := filepath.Rel(source, target)
	if e != nil || rel == "." || (!strings.HasPrefix(rel, ".."+string(os.PathSeparator)) && rel != "..") {
		return fmt.Errorf("blocked_by_account: curator home must be separate from the collection home")
	}
	// Avoid a credentials symlink silently reintroducing the shared login.
	for _, name := range []string{"auth.json", "config.toml"} {
		st, e := os.Lstat(filepath.Join(target, name))
		if e == nil && (st.Mode()&os.ModeSymlink != 0 || !st.Mode().IsRegular()) {
			return fmt.Errorf("blocked_by_account: curator credential/config symlinks are forbidden")
		}
		if e != nil && !os.IsNotExist(e) {
			return e
		}
	}
	return os.Chmod(target, 0700)
}
func (r CodexRunner) checkRuntime(ctx context.Context) error {
	if e := ValidateAccountConfig(r.Config); e != nil {
		return e
	}
	cmd := exec.CommandContext(ctx, r.Config.CodexBinary, "--version")
	cmd.Env = cleanEnv(r.Config)
	v, e := cmd.Output()
	if e != nil || strings.TrimSpace(string(v)) != "codex-cli "+RuntimeVersion {
		return fmt.Errorf("blocked_by_codex: runtime version is not validated")
	}
	return nil
}
func (r CodexRunner) Account(ctx context.Context) (Account, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if e := r.checkRuntime(ctx); e != nil {
		return Account{}, e
	}
	lock, e := collect.Lock(r.Config.CuratorCodexHome, "auth")
	if e != nil {
		return Account{}, e
	}
	defer collect.Unlock(lock)
	peer, dir, e := r.openPeer(ctx)
	if e != nil {
		return Account{}, e
	}
	defer os.RemoveAll(dir)
	defer peer.Close()
	raw, e := peer.Call("account/read", map[string]any{"refreshToken": false})
	if e != nil {
		return Account{}, e
	}
	return verifyAccount(raw, r.Config.CuratorAccountEmail)
}
func (r CodexRunner) Check(ctx context.Context) error { _, e := r.Account(ctx); return e }

// Login requires the user to choose the pinned account in the official browser flow.
// This does not read, copy or modify the working Codex's credential store.
func (r CodexRunner) Login(ctx context.Context) error {
	if e := r.checkRuntime(ctx); e != nil {
		return e
	}
	lock, e := collect.Lock(r.Config.CuratorCodexHome, "auth")
	if e != nil {
		return e
	}
	cmd := exec.CommandContext(ctx, r.Config.CodexBinary, "-c", `cli_auth_credentials_store="file"`, "-c", `forced_login_method="chatgpt"`, "login")
	cmd.Env = cleanEnv(r.Config)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	e = cmd.Run()
	collect.Unlock(lock)
	if e != nil {
		return e
	}
	return r.Check(ctx)
}
