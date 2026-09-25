package tui

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

var (
	background = lipgloss.Color("#0B1020")
	ink        = lipgloss.Color("#E6E9FF")
	muted      = lipgloss.Color("#8590B3")
	cyan       = lipgloss.Color("#49F5EF")
	pink       = lipgloss.Color("#FF6CCD")
	violet     = lipgloss.Color("#AE87FF")
	green      = lipgloss.Color("#9BFFBB")
	amber      = lipgloss.Color("#FFD787")
	base       = lipgloss.NewStyle().Foreground(ink)
	dim        = lipgloss.NewStyle().Foreground(muted)
	accent     = lipgloss.NewStyle().Foreground(cyan)
	bold       = lipgloss.NewStyle().Foreground(ink).Bold(true)
)

// Nested Lip Gloss spans end with SGR 0, which also clears their parent's
// background. Restore the canvas colors after those resets, but leave an
// explicit terminal reset at the end of the line so no styling leaks on exit.
// Deriving the sequences from Lip Gloss preserves NO_COLOR and 256-color support.
func paintCanvas(s string) string {
	defaults, _, _ := strings.Cut(lipgloss.NewStyle().Foreground(ink).Background(background).Render(" "), " ")
	if defaults == "" {
		return s
	}
	bg, _, _ := strings.Cut(lipgloss.NewStyle().Background(background).Render(" "), " ")
	s = strings.NewReplacer(
		"\x1b[0m", "\x1b[0m"+defaults,
		"\x1b[m", "\x1b[m"+defaults,
		"\x1b[49m", "\x1b[49m"+bg,
	).Replace(s)
	return s + "\x1b[0m"
}

// All external strings cross this boundary before reaching the terminal.
// Strip OSC/CSI (including clipboard commands), remaining controls and bidi controls.
func safe(s string) string {
	s = ansi.Strip(s)
	return strings.Map(func(r rune) rune {
		if r == '\n' {
			return r
		}
		if r == '\t' {
			return ' '
		}
		if unicode.IsControl(r) || (r >= 0x202A && r <= 0x202E) || (r >= 0x2066 && r <= 0x2069) {
			return -1
		}
		return r
	}, s)
}
func oneLine(s string) string    { return strings.Join(strings.Fields(safe(s)), " ") }
func cut(s string, w int) string { return ansi.Truncate(s, max(1, w), "…") }
func rule(w int) string          { return dim.Render(strings.Repeat("─", max(0, w))) }
func label(s string) string      { return accent.Bold(true).Render(s) }

// Two hemispheres, separated fissure and brainstem. Each cell is a terminal pixel.
var brainPixels = []string{
	"       aaa      bbb       ",
	"    aaaaaaa  bbbbbbb     ",
	"  aaAAaaAAaa bbBBbbBBbb  ",
	" aaAA  aAAaa bbBBb  BBbb ",
	"aaAAaaAAAaaa bbbBBBbbBBbb",
	"aaA  AAAaaAa bBbBB  bBBbb",
	"aaAAAaaA  aa bb  BBBbbBbb",
	" aaAAaAAAAaa bbBBbbBBBbb ",
	"  aaAAAaaaaa bbbbbBBBbb  ",
	"    aaaaaaaa bbbbbbbb    ",
	"      aaaaaa bbbbbb      ",
	"          cc cc         ",
	"           ccc          ",
}

func brain(frame int, animated bool) string {
	palette := []lipgloss.Color{"#43F2EC", "#54CFFF", "#7C9BFF", "#B780FF", "#F072E1", "#FF78BB"}
	var lines []string
	for y, row := range brainPixels {
		var line strings.Builder
		for x, r := range row {
			if r == ' ' {
				line.WriteByte(' ')
				continue
			}
			i := min(len(palette)-1, x/6+y/4)
			pixel := "⠿"
			if unicode.IsUpper(r) {
				pixel = "⣿"
			}
			if r == 'c' {
				pixel = "⣦"
				i = 2
			}
			color := palette[i]
			if animated && (x+y*2+frame/2)%23 == 0 {
				color = "#F5F4FF"
				pixel = "⣿"
			}
			line.WriteString(lipgloss.NewStyle().Foreground(color).Render(pixel))
		}
		lines = append(lines, line.String())
	}
	return strings.Join(lines, "\n")
}

func badge(s string) string {
	color := muted
	switch s {
	case "active", "succeeded", "稼働中":
		color = green
	case "running", "queued":
		color = cyan
	case "held", "retry", "blocked_by_account", "blocked_by_codex", "quarantined":
		color = amber
	}
	return lipgloss.NewStyle().Foreground(color).Render(s)
}
func meter(n int64, threshold, w int) string {
	threshold = max(1, threshold)
	filled := min(w, int(n)*w/threshold)
	return accent.Render(strings.Repeat("━", max(0, filled))) + dim.Render(strings.Repeat("─", max(0, w-filled))) + dim.Render(fmt.Sprintf(" %d / %d", n, threshold))
}
