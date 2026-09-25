// Package tui is a local terminal client of the existing authenticated daemon API.
package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/term"
	"imbue/internal/config"
	"imbue/internal/curator"
)

type snapshotMsg struct {
	s   snapshot
	err error
}
type actionMsg struct {
	kind, text string
	err        error
	generation int
}
type pollMsg time.Time
type frameMsg time.Time
type detail struct {
	title, body string
	grounds     []string
}

type model struct {
	ctx                            context.Context
	client                         client
	width, height                  int
	view                           viewport.Model
	tab, cursor, frame, generation int
	data                           snapshot
	loading, connected, animated   bool
	busy, notice, lastError        string
	verifiedEmail                  string
	verifiedAt                     time.Time
	detail                         *detail
}

func Run(ctx context.Context, c config.Config) error {
	if !term.IsTerminal(os.Stdin.Fd()) || !term.IsTerminal(os.Stdout.Fd()) {
		return fmt.Errorf("UIは対話型ターミナルで ./bin/imbue ui を実行してください。状態の取得には ./bin/imbue status を使えます")
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	_, err := tea.NewProgram(newModel(ctx, c), tea.WithAltScreen(), tea.WithContext(ctx)).Run()
	return err
}
func newModel(ctx context.Context, c config.Config) model {
	return model{ctx: ctx, client: client{c}, width: 80, height: 24, view: viewport.New(76, 14), loading: true, animated: true, notice: "あなたの判断と流儀を、エージェントに吹き込む。"}
}
func poll() tea.Cmd { return tea.Tick(3*time.Second, func(t time.Time) tea.Msg { return pollMsg(t) }) }
func pulse() tea.Cmd {
	return tea.Tick(200*time.Millisecond, func(t time.Time) tea.Msg { return frameMsg(t) })
}
func (m model) fetch() tea.Cmd {
	return func() tea.Msg { s, e := m.client.snapshot(m.ctx); return snapshotMsg{s, e} }
}
func (m model) Init() tea.Cmd { return tea.Batch(m.fetch(), poll(), pulse()) }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch v := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = v.Width, v.Height
		m.view.Width = max(1, m.width-4)
		m.view.Height = max(1, m.height-10)
		m.rebuild()
	case frameMsg:
		m.frame++
		if m.animated && m.tab == 0 && m.detail == nil {
			m.rebuild()
		}
		return m, pulse()
	case pollMsg:
		if !m.loading {
			m.loading = true
			return m, tea.Batch(m.fetch(), poll())
		}
		return m, poll()
	case snapshotMsg:
		m.loading = false
		if v.err != nil {
			m.connected = false
			m.lastError = oneLine(v.err.Error())
		} else {
			id := m.selectedID()
			m.data = v.s
			m.connected = v.s.Status.Running
			m.lastError = ""
			if m.verifiedEmail != m.data.Status.AccountEmail {
				m.verifiedEmail = ""
			}
			m.restoreSelection(id)
		}
		m.rebuild()
	case actionMsg:
		m.busy = ""
		if v.kind == "evidence" && (v.generation != m.generation || m.detail == nil) {
			return m, nil
		}
		if v.kind == "verify" {
			m.verifiedEmail = ""
		}
		if v.err != nil {
			m.notice = "操作できませんでした: " + oneLine(v.err.Error())
		} else {
			m.notice = v.text
			if v.kind == "verify" {
				m.verifiedEmail = v.text
				m.verifiedAt = time.Now()
				m.notice = "Lunaの専用認証を確認しました: " + v.text
			}
			if v.kind == "evidence" && v.generation == m.generation && m.detail != nil {
				m.detail = &detail{title: "判断の根拠", body: v.text}
				m.view.GotoTop()
				m.notice = "保存された根拠を表示しています"
			}
		}
		m.rebuild()
		if v.kind == "start" && !m.loading {
			m.loading = true
			return m, m.fetch()
		}
	case tea.KeyMsg:
		key := v.String()
		switch key {
		case "ctrl+c", "q":
			return m, tea.Quit
		case "esc", "backspace":
			if m.detail != nil {
				m.detail = nil
				m.generation++
				m.view.GotoTop()
				m.rebuild()
			}
			return m, nil
		case "tab", "right", "shift+tab", "left", "1", "2", "3", "4":
			n := m.tab
			if key == "tab" || key == "right" {
				n = (n + 1) % 4
			} else if key == "shift+tab" || key == "left" {
				n = (n + 3) % 4
			} else {
				n = int(key[0] - '1')
			}
			m.tab = n
			m.cursor = 0
			m.detail = nil
			m.generation++
			m.view.GotoTop()
			m.rebuild()
			return m, nil
		case " ":
			m.animated = !m.animated
			m.rebuild()
			return m, nil
		case "r":
			if !m.loading {
				m.loading = true
				return m, m.fetch()
			}
			return m, nil
		case "s":
			if !m.connected && m.busy == "" {
				m.busy = "起動中 · PostgreSQLとImbueを準備しています"
				return m, func() tea.Msg {
					return actionMsg{kind: "start", text: "Imbueを起動しました", err: m.client.start(m.ctx)}
				}
			}
			return m, nil
		case "v":
			if m.busy == "" {
				m.busy = "Lunaの専用認証を確認中"
				return m, func() tea.Msg {
					email, e := m.client.verify(m.ctx)
					return actionMsg{kind: "verify", text: email, err: e}
				}
			}
			return m, nil
		case "g":
			if m.busy == "" && m.detail != nil && len(m.detail.grounds) > 0 {
				ids := append([]string(nil), m.detail.grounds...)
				generation := m.generation
				m.busy = "判断の根拠を読み込み中"
				return m, func() tea.Msg {
					text, e := m.client.evidence(m.ctx, ids)
					return actionMsg{kind: "evidence", text: text, err: e, generation: generation}
				}
			}
			return m, nil
		case "enter":
			if m.detail == nil {
				m.openSelected()
				m.view.GotoTop()
				m.rebuild()
			}
			return m, nil
		case "up", "k", "down", "j", "home", "end", "pgup", "pgdown":
			if m.tab > 0 && m.detail == nil {
				switch key {
				case "up", "k":
					m.cursor--
				case "down", "j":
					m.cursor++
				case "home":
					m.cursor = 0
				case "end":
					m.cursor = m.rowCount() - 1
				case "pgup":
					m.cursor -= max(1, m.view.Height-3)
				case "pgdown":
					m.cursor += max(1, m.view.Height-3)
				}
				m.cursor = max(0, min(m.cursor, m.rowCount()-1))
				m.rebuild()
				return m, nil
			}
		}
		var cmd tea.Cmd
		m.view, cmd = m.view.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m model) rowCount() int {
	switch m.tab {
	case 1:
		return len(m.data.Status.Conversations)
	case 2:
		return len(m.data.Cases)
	case 3:
		return len(m.data.Jobs)
	}
	return 0
}
func (m model) rowID(i int) string {
	if i < 0 || i >= m.rowCount() {
		return ""
	}
	switch m.tab {
	case 1:
		return m.data.Status.Conversations[i].ID
	case 2:
		return m.data.Cases[i].ID
	case 3:
		return m.data.Jobs[i].ID
	}
	return ""
}
func (m model) selectedID() string { return m.rowID(m.cursor) }
func (m *model) restoreSelection(id string) {
	for i := 0; i < m.rowCount(); i++ {
		if m.rowID(i) == id && id != "" {
			m.cursor = i
			return
		}
	}
	m.cursor = max(0, min(m.cursor, m.rowCount()-1))
}
func (m *model) openSelected() {
	if m.rowCount() == 0 {
		return
	}
	switch m.tab {
	case 1:
		c := m.data.Status.Conversations[m.cursor]
		m.detail = &detail{title: "会話の収集状況", body: fmt.Sprintf("会話\n%s\n\n完了したTurn   %d\n未整理のTurn   %d\n自動整形の閾値 %d / 会話\n\nリポジトリ\n%s\n\n作業ログ\n%s\n\nCodexのログ形式\n%s", c.ID, c.CompletedTurns, c.PendingTurns, m.data.Status.CuratorThreshold, c.Repository, c.SourcePath, c.CodexVersion)}
	case 2:
		c := m.data.Cases[m.cursor]
		var p curator.Patch
		if err := json.Unmarshal(c.Body, &p); err != nil {
			m.notice = "判断事例を読み取れませんでした"
			return
		}
		body := fmt.Sprintf("%s  ·  v%d\n\n%s\n\n適用する条件\n%s\n\n例外\n%s\n\n用途候補\n%s\n\n会話\n%s\n\n事例ID\n%s\n\n根拠 %d 件 · g で表示", c.Status, c.Version, p.Changes.Judgment, bullets(p.Changes.Conditions), bullets(p.Changes.Exceptions), strings.Join(p.Changes.EligibilityCandidates, " / "), c.ConversationID, c.ID, len(p.GroundedInEventIDs))
		if x := p.Changes.Experience; x != nil {
			observations := make([]string, 0, len(x.Observations))
			for _, o := range x.Observations {
				observations = append(observations, o.Fact)
			}
			body += fmt.Sprintf("\n\n判断時の状況\n%s\n\n観測された経緯\n%s\n\n判断の仮説\n%s\n\n別の解釈・反証\n%s\n\n不明点・適用限界\n%s", x.Situation, bullets(observations), x.Hypothesis, bullets(x.AlternativeInterpretations), bullets(x.Unknowns))
		}
		m.detail = &detail{title: p.Changes.Title, body: body, grounds: p.GroundedInEventIDs}
	case 3:
		j := m.data.Jobs[m.cursor]
		body := fmt.Sprintf("状態  %s\n試行  %d\n\n会話\n%s\n\nJob ID\n%s\n\n作成日時\n%s\n\nエラー\n%s", j.Status, j.Attempt, j.ConversationID, j.ID, j.CreatedAt.Time.Local().Format("2006-01-02 15:04:05"), j.Error)
		m.detail = &detail{title: "Lunaの処理履歴", body: body}
	}
	m.generation++
}
func bullets(items []string) string {
	if len(items) == 0 {
		return "指定なし"
	}
	return "• " + strings.Join(items, "\n• ")
}
