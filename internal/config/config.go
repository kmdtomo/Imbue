package config

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
	"imbue/internal/local"
)

type Config struct {
	Root                  string   `toml:"-" json:"-"`
	Repository            string   `toml:"repository" json:"repository"`
	ObserveAll            bool     `toml:"observe_all" json:"observe_all"`
	ObserveAllSince       string   `toml:"observe_all_since" json:"observe_all_since"`
	CodexHome             string   `toml:"codex_home" json:"codex_home"`
	CodexBinary           string   `toml:"codex_binary" json:"codex_binary"`
	CuratorCodexHome      string   `toml:"curator_codex_home" json:"curator_codex_home"`
	CuratorAccountEmail   string   `toml:"curator_account_email" json:"curator_account_email"`
	APIAddress            string   `toml:"api_address" json:"api_address"`
	DBPort                int      `toml:"db_port" json:"db_port"`
	PollSeconds           int      `toml:"poll_seconds" json:"poll_seconds"`
	ExcludeSessions       []string `toml:"exclude_sessions" json:"exclude_sessions"`
	ExcludePaths          []string `toml:"exclude_paths" json:"exclude_paths"`
	RedactPatterns        []string `toml:"redact_patterns" json:"redact_patterns"`
	CuratorEnabled        bool     `toml:"curator_enabled" json:"curator_enabled"`
	CuratorThreshold      int      `toml:"curator_threshold" json:"curator_threshold"`
	CuratorTimeoutSeconds int      `toml:"curator_timeout_seconds" json:"curator_timeout_seconds"`
	CuratorMaxAttempts    int      `toml:"curator_max_attempts" json:"curator_max_attempts"`
	CuratorMaxPacketBytes int      `toml:"curator_max_packet_bytes" json:"curator_max_packet_bytes"`
}

func DefaultRoot() string {
	if p := os.Getenv("IMBUE_HOME"); p != "" {
		return p
	}
	h, _ := os.UserHomeDir()
	return filepath.Join(h, ".imbue")
}
func Default(root, repo string) Config {
	h, _ := os.UserHomeDir()
	ch := os.Getenv("CODEX_HOME")
	if ch == "" {
		ch = filepath.Join(h, ".codex")
	}
	cb := "/Applications/ChatGPT.app/Contents/Resources/codex"
	if _, e := os.Stat(cb); e != nil {
		cb, _ = exec.LookPath("codex")
	}
	return Config{Root: root, Repository: repo, CodexHome: ch, CodexBinary: cb, CuratorCodexHome: filepath.Join(root, "auth", "curator-codex"), APIAddress: "127.0.0.1:8788", DBPort: 55432, PollSeconds: 3, ExcludeSessions: []string{}, ExcludePaths: []string{".env", ".env.*", "*.pem", "*.key", "auth.json", "credentials.json"}, RedactPatterns: []string{}, CuratorThreshold: 10, CuratorTimeoutSeconds: 180, CuratorMaxAttempts: 3, CuratorMaxPacketBytes: 180000}
}
func (c Config) Validate() error {
	if !filepath.IsAbs(c.Repository) || !filepath.IsAbs(c.CodexHome) || !filepath.IsAbs(c.Root) {
		return fmt.Errorf("repository, codex_home and data root must be absolute")
	}
	host, _, e := net.SplitHostPort(c.APIAddress)
	if e != nil || host != "127.0.0.1" {
		return fmt.Errorf("api_address must bind to 127.0.0.1")
	}
	if c.PollSeconds < 1 || c.CuratorThreshold < 1 || c.CuratorTimeoutSeconds < 1 || c.CuratorMaxAttempts < 1 || c.CuratorMaxPacketBytes < 4096 || c.DBPort < 1024 || c.DBPort > 65535 {
		return fmt.Errorf("invalid configuration limits")
	}
	if c.ObserveAll && c.ObserveAllSince != "" {
		if _, e = time.Parse(time.RFC3339Nano, c.ObserveAllSince); e != nil {
			return fmt.Errorf("observe_all_since must be RFC3339")
		}
	}
	return nil
}
func Load(root string) (Config, error) {
	root, e := filepath.Abs(root)
	if e != nil {
		return Config{}, e
	}
	c := Default(root, "")
	b, e := os.ReadFile(filepath.Join(root, "config", "config.toml"))
	if e != nil {
		return c, e
	}
	if e = toml.Unmarshal(b, &c); e != nil {
		return c, e
	}
	return c, c.Validate()
}
func Save(c Config) error {
	if e := c.Validate(); e != nil {
		return e
	}
	b, e := toml.Marshal(c)
	if e != nil {
		return e
	}
	return local.AtomicWrite(filepath.Join(c.Root, "config", "config.toml"), b)
}
func Init(root, repo string) (Config, error) {
	root, e := filepath.Abs(root)
	if e != nil {
		return Config{}, e
	}
	repo, e = filepath.EvalSymlinks(repo)
	if e != nil {
		return Config{}, e
	}
	repo, e = filepath.Abs(repo)
	if e != nil {
		return Config{}, e
	}
	if _, e = os.Stat(filepath.Join(root, "config", "config.toml")); e == nil {
		return Config{}, fmt.Errorf("already initialized: %s", root)
	}
	c := Default(root, repo)
	c.ObserveAll = true
	c.ObserveAllSince = time.Now().UTC().Format(time.RFC3339Nano)
	for _, d := range []string{"config", "auth", "state", "spool/events", "data/blobs", "cache", "exports", "curator"} {
		if e = os.MkdirAll(filepath.Join(root, d), 0700); e != nil {
			return c, e
		}
	}
	for _, n := range []string{"api-token", "db-password"} {
		p := filepath.Join(root, "auth", n)
		if _, err := os.Stat(p); os.IsNotExist(err) {
			if e = local.AtomicWrite(p, []byte(local.ID()+local.ID())); e != nil {
				return c, e
			}
		}
	}
	if e = Save(c); e != nil {
		return c, e
	}
	return c, local.AtomicWrite(filepath.Join(root, "config", "compose.yaml"), []byte(compose))
}
func (c Config) Secret(name string) (string, error) {
	b, e := os.ReadFile(filepath.Join(c.Root, "auth", name))
	return strings.TrimSpace(string(b)), e
}
func (c Config) DSN() (string, error) {
	p, e := c.Secret("db-password")
	return "postgres://imbue:" + p + "@127.0.0.1:" + strconv.Itoa(c.DBPort) + "/imbue?sslmode=disable", e
}
func (c Config) Compose(args ...string) error {
	p, e := c.Secret("db-password")
	if e != nil {
		return e
	}
	a := []string{"compose", "--project-name", "imbue-" + local.Hash([]byte(c.Root))[:10], "-f", filepath.Join(c.Root, "config", "compose.yaml")}
	a = append(a, args...)
	cmd := exec.Command("docker", a...)
	cmd.Env = append(os.Environ(), "IMBUE_DB_PASSWORD="+p, "IMBUE_DB_PORT="+strconv.Itoa(c.DBPort))
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

const compose = `services:
  postgres:
    image: postgres:17-alpine
    restart: unless-stopped
    environment:
      POSTGRES_USER: imbue
      POSTGRES_DB: imbue
      POSTGRES_PASSWORD: ${IMBUE_DB_PASSWORD:?required}
    ports:
      - "127.0.0.1:${IMBUE_DB_PORT:-55432}:5432"
    volumes:
      - pgdata:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U imbue -d imbue"]
      interval: 2s
      timeout: 3s
      retries: 30
volumes:
  pgdata:
`
