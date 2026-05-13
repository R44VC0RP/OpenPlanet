package ui

import (
	"hash/fnv"
	"strconv"
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
		b.WriteString("\n\n")
	} else {
		for i, game := range m.games {
			cursor, style := menuCursor(m.selected == i)
			b.WriteString(style.Render(cursor + game.Name))
			b.WriteString("\n")
			b.WriteString(mutedStyle.Render("    " + game.Description))
			b.WriteString("\n")
			b.WriteString(mutedStyle.Render("    players: " + activePlayerCount(m.activity.Count(game.ID)) + " playing now | capacity: " + playerCapacity(game.MaxPlayers)))
			b.WriteString("\n\n")
		}
	}
	submitIndex := len(m.games)
	cursor, style := menuCursor(m.selected == submitIndex)
	b.WriteString(style.Render(cursor + "Submit a Game"))
	b.WriteString("\n")
	b.WriteString(mutedStyle.Render("    Send a ggp.cell.v1 endpoint for admin review."))
	b.WriteString("\n\n")
	if m.player.Role == "admin" {
		cursor, style = menuCursor(m.selected == submitIndex+1)
		b.WriteString(style.Render(cursor + "Submitted Games"))
		b.WriteString("\n")
		b.WriteString(mutedStyle.Render("    Admin review queue for pending games."))
		b.WriteString("\n\n")
	}

	b.WriteString(mutedStyle.Render("up/down: move  enter: join  q: quit"))
	return panelBorder.Width(max(m.width-4, 40)).Height(max(m.height-2, 12)).Padding(1, 2).Render(b.String())
}

func menuCursor(selected bool) (string, lipgloss.Style) {
	if selected {
		return "> ", focusStyle
	}
	return "  ", lipgloss.NewStyle().Foreground(lipgloss.Color("#cbd5e1"))
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

func (m Model) renderSubmit() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("Submit a Game"))
	b.WriteString("\n")
	b.WriteString(mutedStyle.Render("Submit an OCI/Docker image. It must listen on $PORT and speak ggp.cell.v1 at /ggp."))
	b.WriteString("\n\n")

	fields := []struct {
		label string
		value string
	}{
		{"Game ID", m.submitID},
		{"Name", m.submitName},
		{"Description", m.submitDescription},
		{"Docker image", m.submitImageRef},
		{"Container port", m.submitContainerPort},
		{"Min cols", m.submitMinCols},
		{"Min rows", m.submitMinRows},
		{"Max players", m.submitMaxPlayers},
		{"Game secret", maskSecret(m.submitSessionSecret)},
	}
	for i, field := range fields {
		cursor, style := menuCursor(m.submitIndex == i)
		value := field.value
		if value == "" && i == 0 {
			value = mutedStyle.Render("auto from name")
		}
		b.WriteString(style.Render(cursor + field.label + ": "))
		b.WriteString(value)
		if m.submitIndex == i {
			b.WriteString(focusStyle.Render("_"))
		}
		b.WriteString("\n")
	}
	cursor, style := menuCursor(m.submitIndex == 9)
	mouse := "no"
	if m.submitSupportsMouse {
		mouse = "yes"
	}
	b.WriteString(style.Render(cursor + "Supports mouse: "))
	b.WriteString(mouse)
	b.WriteString("\n")
	cursor, style = menuCursor(m.submitIndex == submitFieldCount)
	b.WriteString(style.Render(cursor + "Submit for Review"))
	b.WriteString("\n\n")
	if m.submitMessage != "" {
		b.WriteString(mutedStyle.Render(m.submitMessage))
		b.WriteString("\n\n")
	}
	b.WriteString(mutedStyle.Render("enter/tab: next  space: toggle  esc: lobby"))
	b.WriteString("\n")
	b.WriteString(mutedStyle.Render("Use a pinned image tag or digest, not latest. For multiplayer, provide the same 32+ byte secret in your game server."))
	return panelBorder.Width(max(m.width-4, 70)).Height(max(m.height-2, 18)).Padding(1, 2).Render(b.String())
}

func (m Model) renderAdmin() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("Submitted Games"))
	b.WriteString("\n")
	b.WriteString(mutedStyle.Render("enter: test deployed container  a: approve  x: reject  c: re-check  r: reload  esc: lobby"))
	b.WriteString("\n\n")
	if m.adminLoading {
		b.WriteString(mutedStyle.Render("Loading submissions..."))
		b.WriteString("\n")
	} else if len(m.adminGames) == 0 {
		b.WriteString(goodStyle.Render("No pending games."))
		b.WriteString("\n")
	} else {
		for i, game := range m.adminGames {
			cursor, style := menuCursor(m.adminSelected == i)
			b.WriteString(style.Render(cursor + game.Name + " [" + game.ID + "]"))
			b.WriteString("\n")
			b.WriteString(mutedStyle.Render("    by " + defaultText(game.SubmittedByName, "unknown") + " | players: " + playerCapacity(game.MaxPlayers)))
			b.WriteString("\n")
			image := game.ImageRef
			if image == "" {
				image = game.EndpointURL
			}
			b.WriteString(mutedStyle.Render("    " + image))
			b.WriteString("\n")
			check := game.LastCheckStatus
			if check == "" {
				check = "not checked"
			}
			b.WriteString(mutedStyle.Render("    check: " + check))
			if game.LastCheckError != "" {
				b.WriteString(errorStyle.Render(" - " + game.LastCheckError))
			}
			b.WriteString("\n\n")
		}
	}
	if m.adminMessage != "" {
		b.WriteString("\n")
		b.WriteString(mutedStyle.Render(m.adminMessage))
	}
	return panelBorder.Width(max(m.width-4, 70)).Height(max(m.height-2, 18)).Padding(1, 2).Render(b.String())
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

	overlayHeight := max(panelHeight-4, 8)
	maxOverlayWidth := max((panelWidth-6)/2, 18)
	leaderboardWidth := min(max(30, m.width/4), maxOverlayWidth)
	chatWidth := min(max(36, m.width/3), maxOverlayWidth)

	leaderboard := panelBorder.BorderForeground(lipgloss.Color("#38bdf8")).Width(leaderboardWidth).Height(overlayHeight).Render(m.renderLeaderboard(max(leaderboardWidth-4, 1), max(overlayHeight-2, 1)))
	game = overlayAt(game, leaderboard, 2, 2)

	chatLeft := max(maxLineWidth(strings.Split(game, "\n"))-chatWidth-2, 0)
	chat := panelBorder.BorderForeground(lipgloss.Color("#facc15")).Width(chatWidth).Height(overlayHeight).Render(m.renderChat(max(chatWidth-4, 1), max(overlayHeight-2, 1)))
	return overlayAt(game, chat, 2, chatLeft)
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

func (m Model) renderLeaderboard(width, height int) string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("Leaderboard"))
	b.WriteString("\n")

	available := max(height-2, 1)
	if len(m.leaderboard) == 0 {
		b.WriteString(mutedStyle.Render("No scores yet."))
		b.WriteString("\n")
	} else {
		for i, entry := range m.leaderboard[:min(len(m.leaderboard), available)] {
			rank := mutedStyle.Render(strconv.Itoa(i+1) + ". ")
			name := chatNameStyle(entry.DisplayName).Render(entry.DisplayName)
			score := focusStyle.Render(strconv.FormatInt(entry.Score, 10))
			b.WriteString(fitLine(rank+name+" "+score, width))
			b.WriteString("\n")
		}
	}

	for lines := countLines(b.String()); lines < height; lines++ {
		b.WriteString("\n")
	}
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

func overlayAt(base, overlay string, topPad, leftPad int) string {
	baseLines := strings.Split(base, "\n")
	overlayLines := strings.Split(overlay, "\n")
	totalWidth := maxLineWidth(baseLines)
	overlayWidth := maxLineWidth(overlayLines)

	for i, line := range overlayLines {
		idx := topPad + i
		if idx >= len(baseLines) {
			break
		}
		prefix := visiblePrefix(baseLines[idx], leftPad)
		suffix := visibleSuffix(baseLines[idx], leftPad+overlayWidth)
		baseLines[idx] = prefix + strings.Repeat(" ", max(leftPad-lipgloss.Width(prefix), 0)) + line + suffix + strings.Repeat(" ", max(totalWidth-leftPad-overlayWidth-lipgloss.Width(suffix), 0))
	}
	return strings.Join(baseLines, "\n")
}

func maxLineWidth(lines []string) int {
	width := 0
	for _, line := range lines {
		width = max(width, lipgloss.Width(line))
	}
	return width
}

func visiblePrefix(value string, width int) string {
	if width <= 0 {
		return ""
	}
	var b strings.Builder
	visible := 0
	inEscape := false
	for _, r := range value {
		if r == '\x1b' {
			inEscape = true
			b.WriteRune(r)
			continue
		}
		if inEscape {
			b.WriteRune(r)
			if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') {
				inEscape = false
			}
			continue
		}
		if visible >= width {
			break
		}
		b.WriteRune(r)
		visible++
	}
	if visible > 0 {
		b.WriteString("\x1b[0m")
	}
	return b.String()
}

func visibleSuffix(value string, start int) string {
	if start <= 0 {
		return value
	}
	var b strings.Builder
	visible := 0
	inEscape := false
	keep := false
	for _, r := range value {
		if r == '\x1b' {
			inEscape = true
			if keep {
				b.WriteRune(r)
			}
			continue
		}
		if inEscape {
			if keep {
				b.WriteRune(r)
			}
			if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') {
				inEscape = false
			}
			continue
		}
		if visible >= start {
			keep = true
			b.WriteRune(r)
		}
		visible++
	}
	if b.Len() == 0 {
		return ""
	}
	return "\x1b[0m" + b.String()
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

func playerCapacity(maxPlayers int) string {
	if maxPlayers <= 1 {
		return "solo"
	}
	return "up to " + strconv.Itoa(maxPlayers)
}

func activePlayerCount(count int) string {
	if count == 1 {
		return "1 player"
	}
	return strconv.Itoa(count) + " players"
}

func maskSecret(value string) string {
	if value == "" {
		return ""
	}
	return strings.Repeat("*", min(len([]rune(value)), 12))
}

func defaultText(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
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
