package main

import "strings"

// The tall layout for pi's side column. It stacks the same pieces as the
// full-screen view: header, step row, the guard's panel with the keyboard,
// then the camera feed filling the rest. The log is left out because the
// conversation next to the column already shows it.

// panelFixedRows: header, step row, 5 panel rows, separator, feed banner.
const panelFixedRows = 9

func panelVizSize(width, height int) (int, int) {
	w := max(width, 24)
	h := max(height-panelFixedRows, 4)
	return w, h
}

func (m model) panelView() string {
	if m.width == 0 || len(m.viz.grid) == 0 {
		return ""
	}
	rows := []string{m.header(m.width), m.beatBar()}
	rows = append(rows, m.hud()...)
	rows = append(rows, styleSep.Render(strings.Repeat("─", m.width)))
	return strings.Join(rows, "\n") + "\n" + m.viz.render()
}
