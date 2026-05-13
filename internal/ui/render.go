package ui

import (
	"hash/fnv"
	"strings"

	"charm.land/lipgloss/v2"
)

var (
	panelBorder    = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("#334155"))
	titleStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("#7dd3fc")).Bold(true)
	mutedStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("#64748b"))
	goodStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("#22c55e"))
	focusStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("#facc15")).Bold(true)
	errorStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("#f87171")).Bold(true)
	chatNameColors = []string{
		"#f87171",
		"#fb923c",
		"#facc15",
		"#4ade80",
		"#2dd4bf",
		"#38bdf8",
		"#818cf8",
		"#c084fc",
		"#f472b6",
	}
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

func (m Model) renderName() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("Choose Your Name"))
	b.WriteString("\n\n")
	b.WriteString("This name appears in chat and games. It must be unique.\n")
	b.WriteString("Only letters and numbers are allowed, up to 12 characters.\n\n")
	b.WriteString(mutedStyle.Render("Generated name:"))
	b.WriteString(" ")
	b.WriteString(goodStyle.Render(m.player.DisplayName))
	b.WriteString("\n\n")
	b.WriteString("Name: ")
	b.WriteString(focusStyle.Render(m.nameInput + "_"))
	b.WriteString("\n")
	if m.nameError != "" {
		b.WriteString(errorStyle.Render(m.nameError))
		b.WriteString("\n")
	} else if m.nameSaving {
		b.WriteString(mutedStyle.Render("Saving name..."))
		b.WriteString("\n")
	} else {
		b.WriteString(mutedStyle.Render("Press Enter to accept or save. Backspace edits."))
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString(mutedStyle.Render("If your chosen name is taken, a number will be appended automatically."))

	return panelBorder.Width(max(m.width-4, 50)).Height(max(m.height-2, 14)).Padding(1, 2).Render(b.String())
}

func (m Model) renderGame() string {
	panelWidth := max(m.width-2, 20)
	panelHeight := max(m.height-2, 12)
	contentWidth := max(m.width-4, 20)
	contentHeight := max(m.height-4, 8)

	game := panelBorder.Width(panelWidth).Height(panelHeight).Render(m.renderGamePanel(contentWidth, contentHeight))
	if !m.chatOpen {
		return game
	}

	overlayHeight := min(max(10, m.height/3), max(panelHeight-2, 6))
	overlay := panelBorder.BorderForeground(lipgloss.Color("#facc15")).Width(panelWidth).Height(overlayHeight).Render(m.renderChat(contentWidth, max(overlayHeight-2, 1)))
	return overlayBottom(game, overlay)
}

func (m Model) renderGamePanel(width, height int) string {
	view := cropLines(m.surface.Render(), width, height)
	if m.exitArmed {
		return overlayBottom(view, fitLine(errorStyle.Render("Press escape again to return to the gateway."), width))
	}
	return view
}

func (m Model) renderChat(width, height int) string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("Room Chat"))
	b.WriteString(" ")
	b.WriteString(mutedStyle.Render("tab/esc: close  enter: send"))
	b.WriteString("\n")

	available := max(height-4, 1)
	start := max(len(m.messages)-available, 0)
	for _, msg := range m.messages[start:] {
		b.WriteString(fitLine(renderChatMessage(msg.DisplayName, msg.Body), width))
		b.WriteString("\n")
	}

	for lines := countLines(b.String()); lines < height-2; lines++ {
		b.WriteString("\n")
	}

	b.WriteString(fitLine(focusStyle.Render("> "+m.chatInput+"_"), width))
	return cropLines(b.String(), width, height)
}

func (m Model) renderError() string {
	return panelBorder.Width(max(m.width-4, 40)).Padding(1, 2).Render(
		errorStyle.Render(m.errorTitle) + "\n\n" + m.errorText + "\n\nPress esc to return to the gateway.",
	)
}

func overlayBottom(base, overlay string) string {
	baseLines := strings.Split(base, "\n")
	overlayLines := strings.Split(overlay, "\n")
	start := max(len(baseLines)-len(overlayLines), 0)
	for i, line := range overlayLines {
		idx := start + i
		if idx >= len(baseLines) {
			baseLines = append(baseLines, line)
			continue
		}
		baseLines[idx] = line
	}
	return strings.Join(baseLines, "\n")
}

func renderChatMessage(name, body string) string {
	return chatNameStyle(name).Render(name) + mutedStyle.Render(": ") + body
}

func chatNameStyle(name string) lipgloss.Style {
	if len(chatNameColors) == 0 {
		return goodStyle
	}
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(strings.ToLower(name)))
	color := chatNameColors[int(hash.Sum32())%len(chatNameColors)]
	return lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Bold(true)
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

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
