package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"imbue/internal/collect"
	"imbue/internal/config"
	"imbue/internal/curator"
	"imbue/internal/daemon"
	"imbue/internal/evidence"
	"imbue/internal/store"
	"imbue/internal/tui"
)

func pretty(b []byte) {
	var v any
	if json.Unmarshal(b, &v) == nil {
		b, _ = json.MarshalIndent(v, "", "  ")
	}
	fmt.Println(string(b))
}
func main() {
	if e := command().Execute(); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
func command() *cobra.Command {
	var home string
	root := &cobra.Command{Use: "imbue", Short: "日々の仕事から判断の学習事例を育てる", SilenceUsage: true, SilenceErrors: true}
	root.PersistentFlags().StringVar(&home, "home", config.DefaultRoot(), "Imbue data directory")
	load := func() (config.Config, error) { return config.Load(home) }
	runUI := func(cmd *cobra.Command, args []string) error {
		c, err := load()
		if err != nil {
			return fmt.Errorf("Imbueの設定を読み込めません。先に imbue init を実行してください: %w", err)
		}
		return tui.Run(cmd.Context(), c)
	}
	root.RunE = runUI
	root.Args = cobra.NoArgs
	root.AddCommand(&cobra.Command{Use: "ui", Args: cobra.NoArgs, Short: "Open the interactive terminal dashboard", RunE: runUI})
	call := func(cmd *cobra.Command, method, path string) error {
		c, e := load()
		if e != nil {
			return e
		}
		b, e := daemon.Request(cmd.Context(), c, method, path, nil)
		if e == nil {
			pretty(b)
		}
		return e
	}
	var repo string
	initCmd := &cobra.Command{Use: "init", Short: "Initialize local storage and Docker PostgreSQL", RunE: func(cmd *cobra.Command, args []string) error {
		if repo == "" {
			repo, _ = os.Getwd()
		}
		c, e := config.Init(home, repo)
		if e != nil {
			return e
		}
		fmt.Println("Configuration created:", filepath.Join(c.Root, "config", "config.toml"))
		if e = c.Compose("up", "-d", "--wait"); e != nil {
			return e
		}
		st, e := store.Open(cmd.Context(), c)
		if e != nil {
			return e
		}
		defer st.Close()
		if e = st.Migrate(cmd.Context()); e != nil {
			return e
		}
		fmt.Println("Initialized. Curator is disabled. Run: imbue start")
		return nil
	}}
	initCmd.Flags().StringVar(&repo, "repo", "", "Initial repository label (collection defaults to all future Codex workspaces)")
	root.AddCommand(initCmd)
	scope := &cobra.Command{Use: "scope", Short: "Configure which Codex workspaces Imbue observes"}
	scope.AddCommand(&cobra.Command{Use: "all", Args: cobra.NoArgs, Short: "Observe every directory used by Codex from now on", RunE: func(cmd *cobra.Command, args []string) error {
		c, e := load()
		if e != nil {
			return e
		}
		c.ObserveAll = true
		c.ObserveAllSince = time.Now().UTC().Format(time.RFC3339Nano)
		if e = config.Save(c); e != nil {
			return e
		}
		fmt.Println("Scope set to all Codex workspaces from", c.ObserveAllSince)
		fmt.Println("Restart Imbue to apply: imbue stop && imbue start")
		return nil
	}})
	scope.AddCommand(&cobra.Command{Use: "repository PATH", Args: cobra.ExactArgs(1), Short: "Observe one exact repository", RunE: func(cmd *cobra.Command, args []string) error {
		path, e := filepath.EvalSymlinks(args[0])
		if e != nil {
			return e
		}
		path, e = filepath.Abs(path)
		if e != nil {
			return e
		}
		st, e := os.Stat(path)
		if e != nil {
			return e
		}
		if !st.IsDir() {
			return fmt.Errorf("scope path is not a directory")
		}
		c, e := load()
		if e != nil {
			return e
		}
		c.Repository, c.ObserveAll, c.ObserveAllSince = path, false, ""
		if e = config.Save(c); e != nil {
			return e
		}
		fmt.Println("Scope set to", path)
		fmt.Println("Restart Imbue to apply: imbue stop && imbue start")
		return nil
	}})
	root.AddCommand(scope)
	root.AddCommand(&cobra.Command{Use: "start", Short: "Start PostgreSQL and the local daemon", RunE: func(cmd *cobra.Command, args []string) error {
		c, e := load()
		if e != nil {
			return e
		}
		if _, e = daemon.Request(cmd.Context(), c, "GET", "/status", nil); e == nil {
			fmt.Println("Already running")
			return nil
		}
		if e = c.Compose("up", "-d", "--wait"); e != nil {
			return e
		}
		exe, e := os.Executable()
		if e != nil {
			return e
		}
		logFile, e := os.OpenFile(filepath.Join(c.Root, "state", "daemon.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
		if e != nil {
			return e
		}
		defer logFile.Close()
		child := exec.Command(exe, "--home", c.Root, "serve")
		child.Stdout = logFile
		child.Stderr = logFile
		child.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		if e = child.Start(); e != nil {
			return e
		}
		child.Process.Release()
		for i := 0; i < 50; i++ {
			time.Sleep(200 * time.Millisecond)
			if b, e := daemon.Request(cmd.Context(), c, "GET", "/status", nil); e == nil {
				pretty(b)
				return nil
			}
		}
		return fmt.Errorf("daemon did not become ready; inspect %s", filepath.Join(c.Root, "state", "daemon.log"))
	}})
	root.AddCommand(&cobra.Command{Use: "serve", Hidden: true, RunE: func(cmd *cobra.Command, args []string) error {
		c, e := load()
		if e != nil {
			return e
		}
		ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		return daemon.Run(ctx, c)
	}})
	root.AddCommand(&cobra.Command{Use: "stop", Short: "Stop daemon and PostgreSQL; preserve all data", RunE: func(cmd *cobra.Command, args []string) error {
		c, e := load()
		if e != nil {
			return e
		}
		_, reqErr := daemon.Request(cmd.Context(), c, "POST", "/shutdown", nil)
		if reqErr == nil {
			for i := 0; i < 100; i++ {
				if _, e := os.Stat(filepath.Join(c.Root, "state", "daemon.json")); os.IsNotExist(e) {
					break
				}
				time.Sleep(100 * time.Millisecond)
			}
		}
		if e = c.Compose("stop"); e != nil {
			return e
		}
		fmt.Println("Stopped. Database volume and evidence preserved.")
		return nil
	}})
	root.AddCommand(&cobra.Command{Use: "status", Short: "Collection and per-conversation turn counts", RunE: func(cmd *cobra.Command, args []string) error { return call(cmd, "GET", "/status") }})
	root.AddCommand(&cobra.Command{Use: "collect", Short: "Collect once, including while the daemon is stopped", RunE: func(cmd *cobra.Command, args []string) error {
		c, e := load()
		if e != nil {
			return e
		}
		if _, e = daemon.Request(cmd.Context(), c, "GET", "/status", nil); e == nil {
			return call(cmd, "POST", "/collect")
		}
		cl, e := collect.New(c)
		if e != nil {
			return e
		}
		r, e := cl.Scan(cmd.Context())
		b, _ := json.Marshal(r)
		pretty(b)
		if e != nil {
			return e
		}
		st, e := store.Open(cmd.Context(), c)
		if e != nil {
			fmt.Fprintln(os.Stderr, "Evidence buffered; database unavailable. It will be replayed on restart.")
			return nil
		}
		defer st.Close()
		_, e = collect.Flush(cmd.Context(), c.Root, st)
		return e
	}})
	root.AddCommand(&cobra.Command{Use: "conversations", RunE: func(cmd *cobra.Command, args []string) error { return call(cmd, "GET", "/conversations") }})
	root.AddCommand(&cobra.Command{Use: "turns CONVERSATION", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		return call(cmd, "GET", "/turns/"+url.PathEscape(args[0]))
	}})
	ev := &cobra.Command{Use: "evidence", Short: "Inspect captured evidence"}
	ev.AddCommand(&cobra.Command{Use: "list CONVERSATION", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		return call(cmd, "GET", "/evidence?conversation="+url.QueryEscape(args[0]))
	}}, &cobra.Command{Use: "show ID", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		return call(cmd, "GET", "/evidence/"+url.PathEscape(args[0]))
	}})
	root.AddCommand(ev)
	root.AddCommand(&cobra.Command{Use: "jobs", RunE: func(cmd *cobra.Command, args []string) error { return call(cmd, "GET", "/jobs") }})
	cases := &cobra.Command{Use: "case", Short: "Inspect and correct judgment cases"}
	cases.AddCommand(&cobra.Command{Use: "list", RunE: func(cmd *cobra.Command, args []string) error { return call(cmd, "GET", "/cases") }}, &cobra.Command{Use: "show ID", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		return call(cmd, "GET", "/cases/"+url.PathEscape(args[0]))
	}})
	root.AddCommand(cases)
	var judgment, status, reason string
	edit := &cobra.Command{Use: "edit ID", Args: cobra.ExactArgs(1), Short: "Correct or hold a case while retaining its revision history", RunE: func(cmd *cobra.Command, args []string) error {
		c, e := load()
		if e != nil {
			return e
		}
		path := "/cases/" + url.PathEscape(args[0])
		b, e := daemon.Request(cmd.Context(), c, "GET", path, nil)
		if e != nil {
			return e
		}
		var current struct {
			Case struct {
				Version int    `json:"version"`
				Status  string `json:"status"`
			} `json:"case"`
		}
		if e = json.Unmarshal(b, &current); e != nil {
			return e
		}
		s := status
		if s == "" {
			s = current.Case.Status
		}
		b, _ = json.Marshal(curator.Edit{BaseVersion: current.Case.Version, Judgment: judgment, Status: s, Reason: reason})
		out, e := daemon.Request(cmd.Context(), c, "POST", path, bytes.NewReader(b))
		if e == nil {
			pretty(out)
		}
		return e
	}}
	edit.Flags().StringVar(&judgment, "judgment", "", "Corrected natural-language judgment")
	edit.Flags().StringVar(&status, "status", "", "active or held")
	edit.Flags().StringVar(&reason, "reason", "", "Reason for the correction (required)")
	edit.MarkFlagRequired("reason")
	cases.AddCommand(edit)
	root.AddCommand(&cobra.Command{Use: "curate CONVERSATION", Args: cobra.ExactArgs(1), Short: "Manually queue completed turns, including fewer than ten", RunE: func(cmd *cobra.Command, args []string) error {
		return call(cmd, "POST", "/curate/"+url.PathEscape(args[0]))
	}})
	root.AddCommand(&cobra.Command{Use: "retry JOB", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		return call(cmd, "POST", "/jobs/"+url.PathEscape(args[0])+"/retry")
	}})
	cur := &cobra.Command{Use: "curator", Short: "Configure automatic curation or check Luna"}
	var loginEmail string
	login := &cobra.Command{Use: "login", Short: "Sign into the isolated curator account and verify its email", RunE: func(cmd *cobra.Command, args []string) error {
		c, e := load()
		if e != nil {
			return e
		}
		if loginEmail != "" {
			c.CuratorAccountEmail = loginEmail
			c.CuratorEnabled = false
			if e = curator.ValidateAccountConfig(c); e != nil {
				return e
			}
			if e = config.Save(c); e != nil {
				return e
			}
		}
		fmt.Println("Luna-only sign-in. Choose:", c.CuratorAccountEmail)
		return (curator.CodexRunner{Config: c}).Login(cmd.Context())
	}}
	login.Flags().StringVar(&loginEmail, "email", "", "Pin an explicit ChatGPT email (changing it disables automatic curation until restart)")
	cur.AddCommand(login, &cobra.Command{Use: "account", Short: "Verify the pinned Luna account without running a model", RunE: func(cmd *cobra.Command, args []string) error {
		c, e := load()
		if e != nil {
			return e
		}
		a, e := (curator.CodexRunner{Config: c}).Account(cmd.Context())
		if e != nil {
			return e
		}
		b, _ := json.Marshal(map[string]any{"verified": true, "account": a, "codex_home": c.CuratorCodexHome})
		pretty(b)
		return nil
	}})
	for _, enabled := range []bool{true, false} {
		name := "disable"
		if enabled {
			name = "enable"
		}
		cur.AddCommand(&cobra.Command{Use: name, RunE: func(cmd *cobra.Command, args []string) error {
			c, e := load()
			if e != nil {
				return e
			}
			c.CuratorEnabled = enabled
			if enabled {
				if e = (curator.CodexRunner{Config: c}).Check(cmd.Context()); e != nil {
					return e
				}
			}
			if e = config.Save(c); e != nil {
				return e
			}
			fmt.Printf("Automatic curator enabled=%t. Restart the daemon to apply.\n", enabled)
			return nil
		}})
	}
	cur.AddCommand(&cobra.Command{Use: "check", Short: "Verify isolated Luna using synthetic evidence", RunE: func(cmd *cobra.Command, args []string) error {
		c, e := load()
		if e != nil {
			return e
		}
		p := curator.Packet{JobID: "doctor", ConversationID: "synthetic", Repository: c.Repository, PolicyVersion: curator.PolicyVersion, SchemaVersion: curator.SchemaVersion, Events: []evidence.Event{{ID: "synthetic-evidence", ConversationID: "synthetic", TurnID: "one", Kind: "user.message", Payload: json.RawMessage(`{"text":"修正対象以外のリファクタリングは不要です。今回依頼した不具合の修正に限定してください。"}`)}}, ExistingCases: []curator.ExistingCase{}, AllowedEvidenceIDs: []string{"synthetic-evidence"}, Part: 1, Parts: 1}
		r, u, e := (curator.CodexRunner{Config: c}).Run(cmd.Context(), p)
		if e != nil {
			return e
		}
		b, _ := json.Marshal(map[string]any{"model": curator.Model, "result": r, "usage": u})
		pretty(b)
		return nil
	}})
	root.AddCommand(cur)
	root.AddCommand(&cobra.Command{Use: "doctor", Short: "Inspect local runtime availability", RunE: func(cmd *cobra.Command, args []string) error {
		c, e := load()
		if e != nil {
			cwd, _ := os.Getwd()
			c = config.Default(home, cwd)
		}
		for _, v := range [][]string{{"go", "version"}, {"docker", "version", "--format", "{{.Server.Version}}"}, {"docker", "compose", "version"}, {c.CodexBinary, "--version"}, {c.CodexBinary, "login", "status"}} {
			ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Second)
			out, err := exec.CommandContext(ctx, v[0], v[1:]...).CombinedOutput()
			cancel()
			fmt.Printf("%s: %s", v[0], out)
			if err != nil {
				fmt.Printf(" (%v)\n", err)
			}
		}
		return nil
	}})
	return root
}
