package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
	"imbue/internal/config"
	"imbue/internal/curator"
	"imbue/internal/store/db"
)

func fixture() model {
	c := config.Config{Repository: "/Users/demo/imbue", CuratorAccountEmail: "owner@example.com", CuratorThreshold: 10}
	m := newModel(context.Background(), c)
	b, _ := json.Marshal(curator.Patch{Changes: curator.Changes{Title: "変更する範囲を見極める", Judgment: strings.Repeat("依頼された範囲を守り、目的に必要な変更を選ぶ。", 10), Conditions: []string{"既存の設計を尊重する"}, Exceptions: []string{}, EligibilityCandidates: []string{"Eval"}}, GroundedInEventIDs: []string{"ground-a"}})
	m.data = snapshot{Status: status{Running: true, Repository: c.Repository, AccountEmail: c.CuratorAccountEmail, CuratorThreshold: 10, CuratorEnabled: true, Conversations: []db.ListConversationsRow{{ID: "01a0acd4-4fb2-7d03-974e-c0bf9898d8ad", CompletedTurns: 8, PendingTurns: 5}}}, Cases: []db.LearningCase{{ID: "case-a", Version: 1, Status: "active", Body: b}}, At: time.Date(2026, 9, 18, 10, 32, 0, 0, time.Local)}
	m.loading = false
	m.connected = true
	m.rebuild()
	return m
}
func key(m model, k string) model {
	var msg tea.KeyMsg
	switch k {
	case "enter":
		msg.Type = tea.KeyEnter
	case "esc":
		msg.Type = tea.KeyEsc
	default:
		msg.Type = tea.KeyRunes
		msg.Runes = []rune(k)
	}
	v, _ := m.Update(msg)
	return v.(model)
}
func TestNavigationPreservesDetailsAndSelection(t *testing.T) {
	m := key(fixture(), "3")
	m = key(m, "enter")
	if m.detail == nil || !strings.Contains(m.detail.body, "依頼された範囲") {
		t.Fatal("case did not open")
	}
	view, _ := m.Update(snapshotMsg{s: m.data})
	m = view.(model)
	if m.detail == nil {
		t.Fatal("refresh dismissed detail")
	}
	m = key(m, "esc")
	s := m.data
	s.Cases = append([]db.LearningCase{{ID: "newest"}}, s.Cases...)
	view, _ = m.Update(snapshotMsg{s: s})
	m = view.(model)
	if m.selectedID() != "case-a" {
		t.Fatal("refresh changed selection")
	}
	m = key(m, "1")
	view, _ = m.Update(actionMsg{kind: "evidence", text: "late content", generation: m.generation - 1})
	m = view.(model)
	if m.detail != nil || strings.Contains(m.notice, "late content") {
		t.Fatal("late evidence changed screen")
	}
}
func TestOfflineAndFailedVerificationAreHonest(t *testing.T) {
	m := fixture()
	m.verifiedEmail = m.data.Status.AccountEmail
	view, _ := m.Update(actionMsg{kind: "verify", err: fmt.Errorf("blocked_by_account")})
	m = view.(model)
	if m.verifiedEmail != "" {
		t.Fatal("failed verification still shown as verified")
	}
	view, _ = m.Update(snapshotMsg{err: fmt.Errorf("connection refused")})
	m = view.(model)
	if m.connected || !strings.Contains(ansi.Strip(m.View()), "OFFLINE") || len(m.data.Cases) != 1 {
		t.Fatal("offline state inaccurate")
	}
	if !strings.Contains(ansi.Strip(m.overview()), "最後に取得") {
		t.Fatal("cached data is not identified")
	}
}
func TestTerminalControlSequencesAreRemoved(t *testing.T) {
	input := "判断\x1b[2J\x1b]52;c;secret\a\x1b[31m基準\x1b[0m\r\b\u202e\u2066"
	if got := safe(input); got != "判断基準" {
		t.Fatalf("unsafe terminal text: %q", got)
	}
	m := fixture()
	m.detail = &detail{title: input, body: input}
	m.rebuild()
	view := m.View()
	if strings.Contains(view, "secret") || strings.Contains(view, "\x1b[2J") {
		t.Fatal("escape injection reached view")
	}
}

func TestEvidenceShowsMessageWithoutTransportMetadata(t *testing.T) {
	got, err := evidenceText(json.RawMessage(`{"client_id":"internal-only","type":"UserMessage","content":[{"type":"text","text":"依頼した範囲を守る"},{"type":"text","text":"必要な変更だけを行う"}]}`))
	if err != nil || got != "依頼した範囲を守る\n必要な変更だけを行う" {
		t.Fatalf("unexpected evidence: %q %v", got, err)
	}
}
func TestViewsFitTerminalWithJapaneseText(t *testing.T) {
	for _, size := range [][2]int{{40, 12}, {60, 20}, {80, 24}, {100, 32}, {140, 45}} {
		for tab := 0; tab < 4; tab++ {
			m := fixture()
			m.tab = tab
			for _, details := range []bool{false, true} {
				if details {
					m.openSelected()
				}
				v, _ := m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
				m = v.(model)
				lines := strings.Split(m.View(), "\n")
				if len(lines) > size[1] {
					t.Fatalf("%v tab %d: %d lines", size, tab, len(lines))
				}
				for _, line := range lines {
					if ansi.StringWidth(line) > size[0] {
						t.Fatalf("%v tab %d: width %d", size, tab, ansi.StringWidth(line))
					}
				}
			}
		}
	}
}

// Optional visual artifact, using the same renderer and fixture as the UI tests.
func TestRenderPreview(t *testing.T) {
	path := os.Getenv("IMBUE_TUI_PREVIEW")
	if path == "" {
		t.Skip("optional local preview")
	}
	previous := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(previous)
	m := fixture()
	v, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 32})
	m = v.(model)
	if err := os.WriteFile(path, []byte(m.View()), 0600); err != nil {
		t.Fatal(err)
	}
}
