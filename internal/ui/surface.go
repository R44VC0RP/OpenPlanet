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

type RenderMode struct {
	Mode       string
	CellAspect string
}

type Surface struct {
	terminalCols int
	terminalRows int
	cols         int
	rows         int
	render       RenderMode
	cells        []surfaceCell
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
	s.terminalCols = cols
	s.terminalRows = rows
	s.cols, s.rows = s.logicalSize(cols, rows)
	s.cells = make([]surfaceCell, s.cols*s.rows)
	for i := range s.cells {
		s.cells[i] = surfaceCell{ch: " ", fg: "#cbd5e1", bg: "#020617"}
	}
}

func (s *Surface) SetRender(render RenderMode) {
	if render.Mode == "" {
		render.Mode = ggp.RenderModeCells
	}
	if render.CellAspect == "" {
		render.CellAspect = ggp.CellAspectTerminal
	}
	s.render = render
	s.Resize(s.terminalCols, s.terminalRows)
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
	switch s.render.CellAspect {
	case ggp.CellAspectSquareWide:
		return s.renderSquareWide()
	case ggp.CellAspectSquareHalf:
		return s.renderSquareHalf()
	default:
		return s.renderTerminalCells()
	}
}

func (s Surface) renderTerminalCells() string {
	var b strings.Builder
	for y := 0; y < s.rows; y++ {
		b.WriteString(s.renderStyledRow(y, false))
		if y < s.rows-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

func (s Surface) renderSquareWide() string {
	var b strings.Builder
	for y := 0; y < s.rows; y++ {
		b.WriteString(s.renderStyledRow(y, true))
		if y < s.rows-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

func (s Surface) renderStyledRow(y int, squareWide bool) string {
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
		if squareWide {
			left, right := squareWideGlyph(cell.ch)
			run.WriteString(left)
			run.WriteString(right)
		} else {
			run.WriteString(cell.ch)
		}
	}
	b.WriteString(renderCellRun(run.String(), current))
	return b.String()
}

func (s Surface) renderSquareHalf() string {
	var b strings.Builder
	for y := 0; y < s.terminalRows; y++ {
		var run strings.Builder
		current := surfaceStyle{}
		for x := 0; x < s.cols; x++ {
			top := s.cellAt(x, y*2)
			bottom := s.cellAt(x, y*2+1)
			ch := "▀"
			next := surfaceStyle{fg: top.bg, bg: bottom.bg, bold: false}
			if top.ch != " " {
				ch = top.ch
				next = surfaceStyle{fg: top.fg, bg: top.bg, bold: top.bold}
			} else if bottom.ch != " " {
				ch = bottom.ch
				next = surfaceStyle{fg: bottom.fg, bg: bottom.bg, bold: bottom.bold}
			}
			if x == 0 {
				current = next
			}
			if next != current {
				b.WriteString(renderCellRun(run.String(), current))
				run.Reset()
				current = next
			}
			run.WriteString(ch)
		}
		b.WriteString(renderCellRun(run.String(), current))
		if y < s.terminalRows-1 {
			b.WriteByte('\n')
		}
	}
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

func (s Surface) logicalSize(cols, rows int) (int, int) {
	switch s.render.CellAspect {
	case ggp.CellAspectSquareWide:
		return max(cols/2, 1), rows
	case ggp.CellAspectSquareHalf:
		return cols, rows * 2
	default:
		return cols, rows
	}
}

func (s Surface) cellAt(x, y int) surfaceCell {
	if x < 0 || y < 0 || x >= s.cols || y >= s.rows {
		return surfaceCell{ch: " ", fg: "#cbd5e1", bg: "#020617"}
	}
	return s.cells[y*s.cols+x]
}

func squareWideGlyph(ch string) (string, string) {
	if ch == "" || ch == " " {
		return " ", " "
	}
	return ch, " "
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
