package main

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func TestVizCyclesPluginsAndStaysAligned(t *testing.T) {
	var e vizEngine
	var last string
	for i := 0; i < 200; i++ {
		level := 0.2
		if i%8 == 0 {
			level = 1.2
		}
		e.tick(64, 8, i%16, level, i%17/8)
		out := e.render()
		lines := strings.Split(out, "\n")
		if len(lines) != 9 {
			t.Fatalf("frame %d: got %d lines, want 9", i, len(lines))
		}
		if out != e.render() {
			t.Fatal("render must be stable between ticks")
		}
		last = out
	}
	if strings.TrimSpace(last) == "" {
		t.Fatal("expected a visible frame")
	}
	seen := map[int]bool{}
	for i := 0; i < 40; i++ {
		e.switchMode()
		seen[e.mode] = true
		if e.mode == e.prevMode {
			t.Fatal("preset did not change")
		}
	}
	if len(seen) < vizModeCount-2 {
		t.Fatalf("only saw %d/%d plugins", len(seen), vizModeCount)
	}

	for mode := 0; mode < vizModeCount; mode++ {
		e.mode = mode
		e.blend = 0
		e.compose()
		if got := len(strings.Split(e.render(), "\n")); got != e.h+1 {
			t.Fatalf("mode %d: lines %d", mode, got)
		}
	}
}

func TestClockCrawlsTowardMorning(t *testing.T) {
	var m model
	if got := m.clock(); got != "12:00 AM" {
		t.Fatalf("clock %q", got)
	}
	m.beats = stepCount * 60
	if got := m.clock(); got != " 1:00 AM" {
		t.Fatalf("clock %q", got)
	}
	m.beats = stepCount * 359
	if got := m.clock(); got != " 5:59 AM" {
		t.Fatalf("clock %q", got)
	}
	m.beats = stepCount * 360
	if got := m.clock(); got != "12:00 AM" {
		t.Fatalf("clock %q", got)
	}
}

func TestNoteForStaysOnScale(t *testing.T) {
	for _, line := range []string{"", "tool edit", "think", "error fail", "hello"} {
		freq, idx := noteFor(line)
		if idx < 4 || idx >= len(scale) || freq != scale[idx] {
			t.Fatalf("%q -> %d %f", line, idx, freq)
		}
	}
}

func TestVizFeedsAreComplete(t *testing.T) {
	for mode, f := range vizFeeds {
		if f.cam == "" || f.room == "" || f.plugin == "" {
			t.Fatalf("mode %d has no feed", mode)
		}
		if f.palette < 0 || f.palette >= len(vizPalettes) {
			t.Fatalf("mode %d palette %d", mode, f.palette)
		}
	}
}

func TestVizStateAdvancesThroughUpdate(t *testing.T) {
	var m tea.Model = model{lastNote: -1, stepNote: -1}
	m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	seen := map[int]bool{}
	for i := 0; i < 2000; i++ {
		m, _ = m.Update(vizTickMsg{})
		seen[m.(model).viz.mode] = true
	}
	if got := m.(model).viz.frame; got != 2000 {
		t.Fatalf("viz frame %d, want 2000: tick state is being dropped", got)
	}
	if len(seen) < 4 {
		t.Fatalf("only %d presets in 100s of ticks", len(seen))
	}
}

func TestPianoCoversScaleOnWhiteKeys(t *testing.T) {
	for i, k := range pianoWhiteOf {
		if k < 0 || k >= pianoWhites {
			t.Fatalf("note %d -> key %d", i, k)
		}
		if i > 0 && k <= pianoWhiteOf[i-1] {
			t.Fatalf("keys not ascending at %d", i)
		}
	}
	m := model{lastNote: 3, stepNote: 8}
	top, bottom := m.piano()
	if lipgloss.Width(top) != hudWidth || lipgloss.Width(bottom) != hudWidth {
		t.Fatalf("piano width %d/%d, want %d", lipgloss.Width(top), lipgloss.Width(bottom), hudWidth)
	}
	for i, row := range m.hud() {
		if lipgloss.Width(row) != hudWidth {
			t.Fatalf("hud row %d width %d, want %d", i, lipgloss.Width(row), hudWidth)
		}
	}
}

func TestNightEndsAtSixAndResetsPower(t *testing.T) {
	var m tea.Model = model{lastNote: -1, stepNote: -1}
	m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	for i := 0; i < nightBeats; i++ {
		m, _ = m.Update(beatMsg{step: i % stepCount})
		if i == nightBeats-2 && m.(model).power() >= 100 {
			t.Fatal("power should drain during the night")
		}
	}
	mm := m.(model)
	if mm.night != 1 || mm.power() != 100 || mm.viz.msgLeft == 0 {
		t.Fatalf("night %d power %.1f msg %d", mm.night, mm.power(), mm.viz.msgLeft)
	}
}

func TestLayoutGivesLeftoverRowsToViz(t *testing.T) {
	m := model{
		width:  100,
		height: 40,
		logs: []textMsg{
			{text: "one", event: EvtOutput},
			{text: "two", event: EvtThinking},
			{text: "three", event: EvtToolCall},
			{text: strings.Repeat("wide ", 40), event: EvtError},
			{text: "five", event: EvtOutput},
		},
	}
	w, h := vizSize(m.width, m.height)
	m.viz.relayout(w, h)

	logs := m.visibleLogs(w)
	if len(logs) != 4 {
		t.Fatalf("logs: got %d, want 4", len(logs))
	}
	if strings.Contains(logs[0], "one") {
		t.Fatal("oldest line should have scrolled off")
	}
	if strings.Contains(logs[3], "wide wide") {
		t.Fatal("log line was not truncated")
	}

	view := m.View()
	lines := strings.Split(view, "\n")
	if len(lines) > m.height {
		t.Fatalf("view is %d rows, terminal is %d", len(lines), m.height)
	}

	// Field plus banner should be most of the screen, not a 12-row strip.
	if h < 20 {
		t.Fatalf("viz height %d, want the leftover rows", h)
	}
	next, cmd := m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	if cmd != nil {
		t.Fatal("resize should not replace in-flight commands")
	}
	m = next.(model)
	wantH := 30 - styleBox.GetVerticalFrameSize() - 8
	if m.viz.h != wantH {
		t.Fatalf("resize viz height %d, want %d", m.viz.h, wantH)
	}
	view = m.View()
	lines = strings.Split(view, "\n")
	if len(lines) != m.height {
		t.Fatalf("view is %d rows, terminal is %d", len(lines), m.height)
	}
}
