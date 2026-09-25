package tui

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// Emulate a terminal with a light default background. A custom preview that
// treats SGR 0 as dark would conceal this regression.
func terminalBackgrounds(t *testing.T, output string) map[string]int {
	t.Helper()
	sgr := regexp.MustCompile(`\x1b\[([0-9;]*)m`)
	bg := "terminal-default"
	counts := map[string]int{}
	pos := 0
	text := func(s string) {
		for _, r := range s {
			if !unicode.IsControl(r) {
				counts[bg]++
			}
		}
	}
	for _, match := range sgr.FindAllStringSubmatchIndex(output, -1) {
		text(output[pos:match[0]])
		params := strings.Split(output[match[2]:match[3]], ";")
		for i := 0; i < len(params); i++ {
			code, _ := strconv.Atoi(params[i])
			switch code {
			case 0, 49:
				bg = "terminal-default"
			case 38, 48:
				if i+2 >= len(params) {
					t.Fatal("incomplete color sequence")
				}
				n := 2
				if params[i+1] == "2" {
					n = 4
				}
				if i+n >= len(params) {
					t.Fatal("incomplete color components")
				}
				if code == 48 {
					bg = strings.Join(params[i+1:i+n+1], ";")
				}
				i += n
			default:
				if code >= 40 && code <= 47 {
					bg = params[i]
				}
			}
		}
		pos = match[1]
	}
	text(output[pos:])
	if bg != "terminal-default" {
		t.Fatal("canvas colors would leak into the shell")
	}
	return counts
}

func TestCanvasBackgroundSurvivesNestedStyles(t *testing.T) {
	previous := lipgloss.ColorProfile()
	defer lipgloss.SetColorProfile(previous)
	for _, profile := range []termenv.Profile{termenv.TrueColor, termenv.ANSI256} {
		lipgloss.SetColorProfile(profile)
		for _, size := range [][2]int{{40, 12}, {80, 24}, {100, 32}} {
			for tab := 0; tab < 4; tab++ {
				m := fixture()
				m.tab = tab
				for _, details := range []bool{false, true} {
					if details {
						m.openSelected()
					}
					v, _ := m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
					m = v.(model)
					counts := terminalBackgrounds(t, m.View())
					if n := counts["terminal-default"]; n != 0 {
						t.Fatalf("profile %v size %v tab %d detail %t: %d cells inherit the terminal background", profile, size, tab, details, n)
					}
					if len(strings.Split(m.View(), "\n")) != size[1] {
						t.Fatal("canvas does not fill terminal height")
					}
					if tab == 2 && !details && m.view.Height > 4 && len(counts) < 2 {
						t.Fatal("selected row lost its distinct background")
					}
				}
			}
		}
	}
}

func TestCanvasStillHonorsNoColor(t *testing.T) {
	previous := lipgloss.ColorProfile()
	defer lipgloss.SetColorProfile(previous)
	lipgloss.SetColorProfile(termenv.Ascii)
	if strings.Contains(fixture().View(), "\x1b[") {
		t.Fatal("canvas forces color in monochrome mode")
	}
}
