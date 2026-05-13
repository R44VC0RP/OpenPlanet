package ui

import (
	"fmt"
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
)

var (
	panelBorder = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("#334155"))
	titleStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#7dd3fc")).Bold(true)
	mutedStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#64748b"))
	goodStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#22c55e"))
	focusStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#facc15")).Bold(true)
	errorStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#f87171")).Bold(true)
)

func (m Model) renderMenu() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("Game Gateway"))
	b.WriteString("\n")
	b.WriteString(mutedStyle.Render("SSH-key identity: "))
	b.WriteString(goodStyle.Render(m.player.DisplayName))
	b.WriteString(" ")
	b.WriteString(mutedStyle.Render(shortFingerprint(m.player.Fingerprint)))
	b.WriteString("\n\n")
	b.WriteString("Choose an endpoint. Games can be written in any language if they speak ggp.cell.v1.\n\n")

	if len(m.games) == 0 {
		b.WriteString(errorStyle.Render("No enabled games found in Postgres."))
	} else {
		for i, game := range m.games {
			cursor := "  "
			style := lipgloss.NewStyle().Foreground(lipgloss.Color("#cbd5e1"))
			if i == m.selected {
				cursor = "> "
				style = focusStyle
			}
			b.WriteString(style.Render(cursor + game.Name))
			b.WriteString("\n")
			b.WriteString(mutedStyle.Render("    " + game.Description))
			b.WriteString("\n")
			b.WriteString(mutedStyle.Render("    " + game.EndpointURL))
			b.WriteString("\n\n")
		}
	}

	b.WriteString(mutedStyle.Render("up/down: move  enter: join  q: quit"))
	return panelBorder.Width(max(m.width-4, 40)).Height(max(m.height-2, 12)).Padding(1, 2).Render(b.String())
}

func (m Model) renderGame() string {
	if m.width < 100 {
		return m.renderCompactGame()
	}

	chatWidth := 32
	gameWidth := max(m.width-chatWidth-5, 20)
	height := max(m.height-2, 12)

	chat := panelBorder.BorderForeground(m.borderColor(focusChat)).Width(chatWidth).Height(height).Render(m.renderChat(chatWidth-4, height-2))
	game := panelBorder.BorderForeground(m.borderColor(focusGame)).Width(gameWidth).Height(height).Render(m.renderGamePanel(gameWidth-4, height-2))
	return joinColumns(chat, game, " ")
}

func (m Model) renderCompactGame() string {
	content := m.renderGamePanel(max(m.width-4, 20), max(m.height-8, 8))
	footer := m.renderChat(max(m.width-4, 20), 5)
	return panelBorder.BorderForeground(m.borderColor(focusGame)).Width(max(m.width-2, 20)).Render(content) + "\n" +
		panelBorder.BorderForeground(m.borderColor(focusChat)).Width(max(m.width-2, 20)).Height(7).Render(footer)
}

func (m Model) renderGamePanel(width, height int) string {
	title := m.gameTitle
	if title == "" && m.activeGame != nil {
		title = m.activeGame.Name
	}
	if title == "" {
		title = "Connecting"
	}

	var b strings.Builder
	b.WriteString(titleStyle.Render(title))
	b.WriteString(" ")
	b.WriteString(mutedStyle.Render(m.gameStatus))
	b.WriteString("\n")
	b.WriteString(mutedStyle.Render("tab/ctrl+g: focus chat/game  esc: gateway"))
	b.WriteString("\n")
	surfaceHeight := max(height-3, 1)
	b.WriteString(cropLines(m.surface.Render(), width, surfaceHeight))
	b.WriteString("\n")
	b.WriteString(fitLine(focusStyle.Render(m.gameMessage), width))

	return cropLines(b.String(), width, height)
}

func (m Model) renderChat(width, height int) string {
	var b strings.Builder
	focusLabel := ""
	if m.focus == focusChat {
		focusLabel = " " + focusStyle.Render("focused")
	}
	b.WriteString(titleStyle.Render("Room Chat"))
	b.WriteString(focusLabel)
	b.WriteString("\n")

	available := max(height-4, 1)
	start := max(len(m.messages)-available, 0)
	for _, msg := range m.messages[start:] {
		line := fmt.Sprintf("%s: %s", msg.DisplayName, msg.Body)
		b.WriteString(fitLine(line, width))
		b.WriteString("\n")
	}

	for lines := countLines(b.String()); lines < height-2; lines++ {
		b.WriteString("\n")
	}

	prompt := "> " + m.chatInput
	if m.focus != focusChat {
		prompt = mutedStyle.Render("tab to chat")
	}
	b.WriteString(fitLine(prompt, width))
	return cropLines(b.String(), width, height)
}

func (m Model) renderError() string {
	return panelBorder.Width(max(m.width-4, 40)).Padding(1, 2).Render(
		errorStyle.Render(m.errorTitle) + "\n\n" + m.errorText + "\n\nPress esc to return to the gateway.",
	)
}

func (m Model) borderColor(area focusArea) color.Color {
	if m.focus == area {
		return lipgloss.Color("#facc15")
	}
	return lipgloss.Color("#334155")
}

func joinColumns(left, right, gap string) string {
	leftLines := strings.Split(left, "\n")
	rightLines := strings.Split(right, "\n")
	width := 0
	for _, line := range leftLines {
		if len([]rune(stripANSI(line))) > width {
			width = len([]rune(stripANSI(line)))
		}
	}
	rows := max(len(leftLines), len(rightLines))
	var b strings.Builder
	for i := 0; i < rows; i++ {
		leftLine := ""
		if i < len(leftLines) {
			leftLine = leftLines[i]
		}
		rightLine := ""
		if i < len(rightLines) {
			rightLine = rightLines[i]
		}
		b.WriteString(leftLine)
		b.WriteString(strings.Repeat(" ", max(width-len([]rune(stripANSI(leftLine))), 0)))
		b.WriteString(gap)
		b.WriteString(rightLine)
		if i < rows-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

func cropLines(value string, width, height int) string {
	lines := strings.Split(value, "\n")
	if len(lines) > height {
		lines = lines[:height]
	}
	for i := range lines {
		lines[i] = fitLine(lines[i], width)
	}
	return strings.Join(lines, "\n")
}

func fitLine(value string, width int) string {
	if width < 1 {
		return ""
	}
	visibleWidth := lipgloss.Width(value)
	if visibleWidth > width {
		return truncatePlain(stripANSI(value), width)
	}
	return value + strings.Repeat(" ", width-visibleWidth)
}

func truncatePlain(value string, width int) string {
	runes := []rune(value)
	if len(runes) <= width {
		return value
	}
	return string(runes[:width])
}

func countLines(value string) int {
	if value == "" {
		return 0
	}
	return strings.Count(value, "\n") + 1
}

func shortFingerprint(value string) string {
	if len(value) <= 18 {
		return value
	}
	return value[:18] + "..."
}

func stripANSI(value string) string {
	var b strings.Builder
	inEscape := false
	for _, r := range value {
		if r == '\x1b' {
			inEscape = true
			continue
		}
		if inEscape {
			if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') {
				inEscape = false
			}
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
