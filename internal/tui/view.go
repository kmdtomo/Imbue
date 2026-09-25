package tui

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"imbue/internal/curator"
)

func (m *model) rebuild() {
	var content string
	if m.detail != nil {
		content = label(safe(m.detail.title)) + "\n" + dim.Render("Esc 一覧に戻る") + "\n\n" + base.Render(safe(m.detail.body))
		content = ansi.Wrap(content, m.view.Width, "")
	} else if m.tab == 0 {
		content = m.overview()
	} else {
		content = m.list()
	}
	m.view.SetContent(content)
	if m.detail == nil && m.tab > 0 {
		m.view.GotoTop()
	}
}

func (m model) overview() string {
	s := m.data.Status
	var pending int64
	for _, c := range s.Conversations {
		pending += c.PendingTurns
	}
	state := badge("稼働中") + dim.Render("  ·  作業ログを収集中")
	if !m.connected {
		state = lipgloss.NewStyle().Foreground(amber).Render("接続待ち") + dim.Render("  ·  s で起動")
	}
	auto := "OFF"
	if s.CuratorEnabled {
		auto = "ON"
	}
	email := s.AccountEmail
	if email == "" {
		email = m.client.config.CuratorAccountEmail
	}
	if email == "" {
		email = "専用アカウント未設定"
	}
	verified := dim.Render("PINNED  ·  v で認証確認")
	if m.verifiedEmail != "" && m.verifiedEmail == email {
		verified = lipgloss.NewStyle().Foreground(green).Render("VERIFIED  " + m.verifiedAt.Format("15:04:05"))
	}
	count := fmt.Sprintf("%d", pending)
	if !m.connected && m.data.At.IsZero() {
		count = "—"
	}
	stats := accent.Bold(true).Render(count) + dim.Render(" 未整理  /  ") + lipgloss.NewStyle().Foreground(pink).Bold(true).Render(fmt.Sprintf("%d", len(m.data.Cases))) + dim.Render(" 判断事例* ")
	info := []string{
		lipgloss.NewStyle().Foreground(violet).Bold(true).Render("YOUR MIND, IN MOTION."),
		dim.Render("あなたの判断が、次の仕事に染み込む。"),
		"",
		state,
		stats,
		"",
		label("LUNA") + dim.Render("  /  "+curator.Model),
		base.Render(oneLine(email)),
		verified,
		"",
		dim.Render(fmt.Sprintf("自動整形 %s  ·  %d Turn / 会話", auto, max(1, m.threshold()))),
		dim.Render("* 判断事例は直近100件まで"),
		dim.Render("追加学習はまだ実装されていません"),
	}
	var hero string
	if m.view.Width >= 70 {
		rightWidth := m.view.Width - 29
		for i, line := range info {
			info[i] = cut(line, rightWidth)
		}
		hero = lipgloss.JoinHorizontal(lipgloss.Top, lipgloss.NewStyle().Width(29).Render(brain(m.frame, m.animated)), strings.Join(info, "\n"))
	} else {
		hero = brain(m.frame, m.animated) + "\n\n" + strings.Join(info, "\n")
	}
	var extra []string
	if !m.connected {
		extra = append(extra, label("接続する"), base.Render("s でPostgreSQLとImbueを起動します。"))
		if m.lastError != "" {
			extra = append(extra, dim.Render(safe(m.lastError)))
		}
		if !m.data.At.IsZero() {
			extra = append(extra, dim.Render("接続が戻るまでは、最後に取得した内容を表示しています。"))
		}
	} else {
		extra = append(extra, label("NEXT SIGNAL"))
		if len(s.Conversations) == 0 {
			extra = append(extra, base.Render("このリポジトリでCodexと作業すると、ここに会話が現れます。"))
		} else {
			for i, c := range s.Conversations {
				if i == 3 {
					extra = append(extra, dim.Render("すべての会話は 2 キーで確認"))
					break
				}
				extra = append(extra, dim.Render(shortID(c.ID))+"  "+meter(c.PendingTurns, m.threshold(), min(18, max(5, m.view.Width-40))))
			}
			extra = append(extra, dim.Render("同じ会話の未整理Turnが閾値に達すると、Lunaが整形します。"))
		}
		if len(m.data.Cases) > 0 {
			var p curator.Patch
			if json.Unmarshal(m.data.Cases[0].Body, &p) == nil {
				extra = append(extra, "", label("LATEST JUDGMENT"), bold.Render(safe(p.Changes.Title)), base.Render(safe(p.Changes.Judgment)), dim.Render("3 キーで判断事例を開く"))
			}
		}
	}
	if s.BufferedBatches > 0 {
		extra = append(extra, lipgloss.NewStyle().Foreground(amber).Render(fmt.Sprintf("保存待ちのバッチ: %d", s.BufferedBatches)))
	}
	if s.LastError != "" {
		extra = append(extra, lipgloss.NewStyle().Foreground(amber).Render("収集エラー: "+safe(s.LastError)))
	}
	for _, warning := range s.LastScan.Warnings {
		extra = append(extra, lipgloss.NewStyle().Foreground(amber).Render("収集の確認事項: "+safe(warning)))
	}
	return hero + "\n\n" + rule(m.view.Width) + "\n\n" + ansi.Wrap(strings.Join(extra, "\n"), m.view.Width, "")
}
func (m model) threshold() int {
	if m.data.Status.CuratorThreshold > 0 {
		return m.data.Status.CuratorThreshold
	}
	return m.client.config.CuratorThreshold
}
func shortID(s string) string {
	if len(s) > 18 {
		return s[:8] + "…" + s[len(s)-6:]
	}
	return s
}

func (m model) list() string {
	titles := []string{"", "会話の収集状況", "あなたの判断事例", "Lunaの処理履歴"}
	var lines []string
	suffix := ""
	if m.tab >= 2 {
		suffix = " · 直近100件まで"
	}
	lines = append(lines, label(titles[m.tab])+dim.Render(fmt.Sprintf("  %d件%s", m.rowCount(), suffix)), dim.Render("↑↓ 選択  /  Enter 詳細"), "")
	if m.rowCount() == 0 {
		if !m.connected {
			return strings.Join(lines, "\n") + "\n" + dim.Render("接続できていません。1 キーでホームへ戻り、s で起動してください。")
		}
		messages := []string{"", "まだ会話がありません。このリポジトリでCodexとの作業を始めてください。", "まだ判断事例がありません。会話が蓄積され、Lunaが整形すると表示されます。", "まだ処理履歴がありません。会話ごとの閾値に達すると処理が始まります。"}
		return strings.Join(lines, "\n") + "\n" + ansi.Wrap(dim.Render(messages[m.tab]), m.view.Width, "")
	}
	pageSize := max(1, m.view.Height-4)
	start := m.cursor / pageSize * pageSize
	for i := start; i < min(m.rowCount(), start+pageSize); i++ {
		var text string
		switch m.tab {
		case 1:
			c := m.data.Status.Conversations[i]
			text = fmt.Sprintf("%s  %d 完了 · %d 未整理", shortID(c.ID), c.CompletedTurns, c.PendingTurns)
		case 2:
			c := m.data.Cases[i]
			var p curator.Patch
			json.Unmarshal(c.Body, &p)
			text = fmt.Sprintf("%s  v%d  %s", c.Status, c.Version, oneLine(p.Changes.Title))
		case 3:
			j := m.data.Jobs[i]
			text = fmt.Sprintf("%s  %-18s %s", j.CreatedAt.Time.Local().Format("01/02 15:04"), j.Status, shortID(j.ConversationID))
		}
		prefix := "  "
		style := base
		if i == m.cursor {
			prefix = "▸ "
			style = lipgloss.NewStyle().Foreground(cyan).Background(lipgloss.Color("#18233C")).Bold(true)
		}
		lines = append(lines, style.Width(m.view.Width).Render(cut(prefix+oneLine(text), m.view.Width)))
	}
	lines = append(lines, dim.Render(fmt.Sprintf("%d–%d / %d", start+1, min(m.rowCount(), start+pageSize), m.rowCount())))
	return strings.Join(lines, "\n")
}

func (m model) View() string {
	if m.width < 40 || m.height < 12 {
		return "imbue\n画面を40列 × 12行以上に広げてください。\nq で終了"
	}
	w := m.width - 4
	state := lipgloss.NewStyle().Foreground(amber).Render("● OFFLINE")
	if m.connected {
		state = lipgloss.NewStyle().Foreground(green).Render("● ONLINE")
	}
	if m.loading && m.data.At.IsZero() {
		state = accent.Render("◌ CONNECTING")
	}
	logo := lipgloss.NewStyle().Foreground(cyan).Bold(true).Render("▗▖ imbue") + dim.Render("  /  PERSONAL INTELLIGENCE")
	gap := max(1, w-ansi.StringWidth(logo)-ansi.StringWidth(state))
	repo := m.data.Status.Repository
	if repo == "" {
		repo = m.client.config.Repository
	}
	if m.data.Status.ObserveAll || (m.data.At.IsZero() && m.client.config.ObserveAll) {
		repo = "ALL CODEX WORKSPACES  /  各会話の作業ディレクトリを自動検出"
	}
	tabs := []string{"1 脳 / ホーム", "2 会話", "3 判断事例", "4 処理履歴"}
	for i, t := range tabs {
		style := dim
		if i == m.tab {
			style = lipgloss.NewStyle().Foreground(pink).Bold(true).Underline(true)
		}
		tabs[i] = style.Render(t)
	}
	crumb := dim.Render(oneLine(filepath.Clean(repo)))
	lines := []string{logo + strings.Repeat(" ", gap) + state, crumb, rule(w), strings.Join(tabs, "   "), ""}
	lines = append(lines, strings.Split(m.view.View(), "\n")...)
	notice := oneLine(m.notice)
	if m.busy != "" {
		notice = "◌ " + oneLine(m.busy)
	} else if m.lastError != "" {
		notice = "未接続 · s 起動 / r 再接続 · " + oneLine(m.lastError)
	}
	help := "Tab 切替  ↑↓ 移動  Enter 詳細  r 更新  v 認証  q 閉じる"
	if m.detail != nil {
		help = "↑↓ スクロール  Esc 戻る  g 根拠  q 閉じる"
	} else if m.tab == 0 {
		help = "1–4 切替  ↑↓ スクロール  s 起動  v 認証  q 閉じる"
	}
	timeLabel := "接続待ち"
	if !m.data.At.IsZero() {
		timeLabel = "取得 " + m.data.At.Format("15:04:05") + " · 3秒ごとに更新"
	}
	lines = append(lines, "", dim.Render(notice), dim.Render(help), dim.Render(timeLabel+"  /  Space 発光ON・OFF · UIを閉じても収集は継続"))
	for len(lines) < m.height {
		lines = append(lines, "")
	}
	canvas := lipgloss.NewStyle().Background(background).Foreground(ink).Width(m.width)
	for i, line := range lines {
		lines[i] = paintCanvas(canvas.Render("  " + cut(line, w) + "  "))
	}
	return strings.Join(lines, "\n")
}
