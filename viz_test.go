package main

import (
	"math"
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
	// 400 random picks: missing any of the 16 feeds by chance is ~1e-10.
	seen := map[int]bool{}
	for i := 0; i < 400; i++ {
		e.switchMode()
		seen[e.mode] = true
		if e.mode == e.prevMode {
			t.Fatal("preset did not change")
		}
	}
	if len(seen) < vizModeCount {
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
	for i := 0; i < 200; i++ {
		m, _ = m.Update(vizTickMsg{})
	}
	if got := m.(model).viz.frame; got != 200 {
		t.Fatalf("viz frame %d, want 200: tick state is being dropped", got)
	}
}

// camerasSeen ticks n frames, calling during(i) first, and counts cuts.
func camerasSeen(m tea.Model, n int, during func(i int) tea.Msg) (tea.Model, int) {
	cuts := 0
	last := m.(model).viz.mode
	for i := 0; i < n; i++ {
		if msg := during(i); msg != nil {
			m, _ = m.Update(msg)
		}
		m, _ = m.Update(vizTickMsg{})
		if mode := m.(model).viz.mode; mode != last {
			cuts++
			last = mode
		}
	}
	return m, cuts
}

func TestCamerasDriftSlowlyWhenIdle(t *testing.T) {
	var m tea.Model = model{lastNote: -1, stepNote: -1}
	m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	none := func(int) tea.Msg { return nil }
	m, cuts := camerasSeen(m, vizDriftMin-1, none)
	if cuts != 0 {
		t.Fatalf("%d cuts in the first %d idle frames, want none", cuts, vizDriftMin-1)
	}
	_, cuts = camerasSeen(m, vizDriftMax, none)
	if cuts == 0 {
		t.Fatal("an idle camera should drift to the next one eventually")
	}
}

func TestLeadNotesCueCamerasWithoutStrobing(t *testing.T) {
	var m tea.Model = model{lastNote: -1, stepNote: -1}
	m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	m, _ = camerasSeen(m, vizMinHold, func(int) tea.Msg { return nil })

	// A lead note once the camera has had its time cuts on the next frame.
	m, cuts := camerasSeen(m, 1, func(int) tea.Msg { return textMsg{text: "tool: edit", event: EvtToolCall, note: 5} })
	if cuts != 1 {
		t.Fatalf("lead note after the minimum hold: %d cuts, want 1", cuts)
	}

	// A note on every frame for a stretch still cuts only once per minimum hold.
	frames := vizMinHold * 5
	_, cuts = camerasSeen(m, frames, func(int) tea.Msg { return textMsg{text: "burst", event: EvtOutput, note: 6} })
	if cuts > frames/vizMinHold {
		t.Fatalf("burst of notes gave %d cuts in %d frames, want at most %d", cuts, frames, frames/vizMinHold)
	}
	if cuts < frames/vizMinHold-1 {
		t.Fatalf("burst of notes gave only %d cuts; notes should keep cueing", cuts)
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

func TestShapesStayRoundInAnyFeedShape(t *testing.T) {
	for _, size := range [][2]int{{52, 30}, {114, 27}, {70, 60}} {
		var e vizEngine
		e.relayout(size[0], size[1])

		// The same physical distance right and down lands on the same radius.
		x0, y0 := e.norm(3, 3)
		x1, _ := e.norm(13, 3)
		_, y1 := e.norm(3, 8) // 5 rows are as tall as 10 columns are wide
		if math.Abs((x1-x0)-(y1-y0)) > 1e-9 {
			t.Fatalf("%v: 10 columns -> %.3f but 5 rows -> %.3f", size, x1-x0, y1-y0)
		}

		// A drawn ring is twice as many columns wide as it is rows tall.
		e.rings = []vring{{r: 0.5}}
		g := make([]vcell, e.w*e.h)
		e.drawRings(g)
		minX, maxX, minY, maxY := e.w, -1, e.h, -1
		for y := 0; y < e.h; y++ {
			for x := 0; x < e.w; x++ {
				if c := g[y*e.w+x].ch; c == '*' || c == 'o' {
					minX, maxX = min(minX, x), max(maxX, x)
					minY, maxY = min(minY, y), max(maxY, y)
				}
			}
		}
		wide := float64(maxX - minX + 1)
		tall := float64(maxY-minY+1) * cellAspect
		if ratio := wide / tall; ratio < 0.85 || ratio > 1.15 {
			t.Fatalf("%v: ring is %.0f wide by %.0f tall (in columns), ratio %.2f", size, wide, tall, ratio)
		}
	}
}
