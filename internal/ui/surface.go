package ui

import (
	"strings"

	"gamegateway/internal/ggp"
)

type surfaceCell struct {
	ch    string
	fg    string
	bg    string
	bold  bool
	dirty bool
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
	s.cols = cols
	s.rows = rows
	s.cells = make([]surfaceCell, cols*rows)
	for i := range s.cells {
		s.cells[i] = surfaceCell{ch: " ", fg: "#cbd5e1", bg: "#020617"}
	}
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
	var b strings.Builder
	for y := 0; y < s.rows; y++ {
		for x := 0; x < s.cols; x++ {
			cell := s.cells[y*s.cols+x]
			b.WriteString(cell.ch)
		}
		if y < s.rows-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
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
