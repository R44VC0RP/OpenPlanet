package ui

import (
	"strings"

	"charm.land/lipgloss/v2"

	"gamegateway/internal/ggp"
)

type surfaceCell struct {
	ch   string
	fg   string
	bg   string
	bold bool
}

type surfaceStyle struct {
	fg   string
	bg   string
	bold bool
}

type Surface struct {
	cols  int
	rows  int
	cells []surfaceCell
}

func NewSurface(cols, rows int) Surface {
	s := Surface{}
	s.Resize(cols, rows)
	return s
}

func (s *Surface) Resize(cols, rows int) {
	if cols < 1 {
		cols = 1
	}
	if rows < 1 {
		rows = 1
	}
	s.cols, s.rows = cols, rows
	s.cells = make([]surfaceCell, s.cols*s.rows)
	for i := range s.cells {
		s.cells[i] = surfaceCell{ch: " ", fg: "#cbd5e1", bg: "#020617"}
	}
}

func (s Surface) Viewport() (int, int) {
	return s.cols, s.rows
}

func (s *Surface) Apply(frame ggp.Frame) {
	if frame.Mode == ggp.FrameFull {
		s.clear()
	}
	for _, cell := range frame.Cells {
		if cell.X < 0 || cell.Y < 0 || cell.X >= s.cols || cell.Y >= s.rows {
			continue
		}
		idx := cell.Y*s.cols + cell.X
		ch := cell.Ch
		if ch == "" {
			ch = " "
		}
		s.cells[idx] = surfaceCell{
			ch:   firstRune(ch),
			fg:   fallback(cell.Fg, "#cbd5e1"),
			bg:   fallback(cell.Bg, "#020617"),
			bold: hasAttr(cell.Attrs, "bold"),
		}
	}
}

func (s Surface) Render() string {
	return s.renderTerminalCells()
}

func (s Surface) renderTerminalCells() string {
	var b strings.Builder
	for y := 0; y < s.rows; y++ {
		b.WriteString(s.renderStyledRow(y))
		if y < s.rows-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

func (s Surface) renderStyledRow(y int) string {
	var b strings.Builder
	var run strings.Builder
	current := surfaceStyle{}

	for x := 0; x < s.cols; x++ {
		cell := s.cells[y*s.cols+x]
		next := surfaceStyle{fg: cell.fg, bg: cell.bg, bold: cell.bold}
		if x == 0 {
			current = next
		}
		if next != current {
			b.WriteString(renderCellRun(run.String(), current))
			run.Reset()
			current = next
		}
		run.WriteString(cell.ch)
	}
	b.WriteString(renderCellRun(run.String(), current))
	return b.String()
}

func renderCellRun(value string, style surfaceStyle) string {
	if value == "" {
		return ""
	}
	renderer := lipgloss.NewStyle().Foreground(lipgloss.Color(style.fg)).Background(lipgloss.Color(style.bg))
	if style.bold {
		renderer = renderer.Bold(true)
	}
	return renderer.Render(value)
}

func (s *Surface) clear() {
	for i := range s.cells {
		s.cells[i] = surfaceCell{ch: " ", fg: "#cbd5e1", bg: "#020617"}
	}
}

func firstRune(value string) string {
	for _, r := range value {
		return string(r)
	}
	return " "
}

func fallback(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func hasAttr(attrs []string, attr string) bool {
	for _, candidate := range attrs {
		if candidate == attr {
			return true
		}
	}
	return false
}
