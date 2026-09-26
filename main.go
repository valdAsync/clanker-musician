// Command clanker-musician is a night-shift monitor and synthwave groovebox. A
// stream of agent output (one event per line on stdin) becomes an endless loop.
//
//	agent | clanker-musician
package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/ebitengine/oto/v3"
)

// --- EVENT TYPES ---

type EventType int

const (
	EvtOutput EventType = iota
	EvtThinking
	EvtToolCall
	EvtError
)

func classify(line string) EventType {
	switch {
	case strings.Contains(line, "tool"), strings.Contains(line, "edit"):
		return EvtToolCall
	case strings.Contains(line, "think"):
		return EvtThinking
	case strings.Contains(line, "error"), strings.Contains(line, "fail"):
		return EvtError
	default:
		return EvtOutput
	}
}

// --- BUBBLE TEA UI ---

type textMsg struct {
	text  string
	event EventType
	note  int
}

type beatMsg struct {
	step    int
	level   float64
	note    int
	chord   string
	section string
	energy  float64
}

type model struct {
	msgs <-chan tea.Msg

	logs     []textMsg
	beatStep int
	beats    int
	night    int     // completed nights
	drain    float64 // power used tonight, in percent
	lastNote int
	stepNote int
	chord    string
	section  string
	energy   float64
	panel    bool // drawing the tall side-column layout for pi
	// contextPower, when set, replaces the night's drain: pi reports how much
	// of the context window is left.
	contextPower    float64
	hasContextPower bool
	audioLevel      float64
	width           int
	height          int
	agentState      EventType
	viz             vizEngine
}

var (
	styleGreen   = lipgloss.NewStyle().Foreground(lipgloss.Color("46"))
	styleAmber   = lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Bold(true)
	styleMagenta = lipgloss.NewStyle().Foreground(lipgloss.Color("201"))
	styleDim     = lipgloss.NewStyle().Foreground(lipgloss.Color("65"))
	styleFaint   = lipgloss.NewStyle().Foreground(lipgloss.Color("238"))
	styleBox     = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("22")).Padding(1, 2)
	styleSep     = lipgloss.NewStyle().Foreground(lipgloss.Color("22"))

	// A beige 80s home keyboard.
	keyWhite = lipgloss.NewStyle().Background(lipgloss.Color("187")).Foreground(lipgloss.Color("101"))
	keyBlack = lipgloss.NewStyle().Background(lipgloss.Color("187")).Foreground(lipgloss.Color("234"))
	keyLast  = lipgloss.NewStyle().Background(lipgloss.Color("214"))
	keyStep  = lipgloss.NewStyle().Background(lipgloss.Color("46"))
)

const (
	nightBeats = stepCount * 360 // one clock minute per loop, 12 AM to 6 AM
	hudWidth   = pianoWhites*2 + 1
	hudMinView = hudWidth + 3 + 36 // below this the log gets the whole row
)

func waitForMsg(ch <-chan tea.Msg) tea.Cmd {
	return func() tea.Msg { return <-ch }
}

func (m model) Init() tea.Cmd {
	return tea.Batch(waitForMsg(m.msgs), scheduleViz())
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		if m.width > 0 {
			w, h := m.vizDims()
			m.viz.relayout(w, h)
		}

	case vizTickMsg:
		m.advanceViz()
		return m, scheduleViz()

	case textMsg:
		m.agentState = msg.event
		m.lastNote = msg.note
		m.viz.cue() // a new lead note moves to the next camera
		if msg.event == EvtError {
			m.drain += 1.5
		}
		m.logs = append(m.logs, msg)
		if len(m.logs) > 200 {
			m.logs = m.logs[len(m.logs)-200:]
		}
		return m, waitForMsg(m.msgs)

	case beatMsg:
		m.beatStep = msg.step
		m.beats++
		m.drain += 0.008
		if m.agentState == EvtThinking || m.agentState == EvtToolCall {
			m.drain += 0.006
		}
		if m.beats%nightBeats == 0 {
			m.night++
			m.drain = 0
			m.viz.announce("6:00 AM", fmt.Sprintf("NIGHT %d COMPLETE", m.night))
		}
		m.stepNote = msg.note
		m.chord = msg.chord
		m.section = msg.section
		m.energy = msg.energy
		m.audioLevel = msg.level
		return m, waitForMsg(m.msgs)
	}

	return m, nil
}

func renderLog(msg textMsg, width int) string {
	tag := "SIGNAL"
	style := styleGreen
	switch msg.event {
	case EvtThinking, EvtToolCall:
		tag = "MOTION"
		style = styleAmber
	case EvtError:
		tag = "ALERT "
		style = styleMagenta
	}
	prefix := tag + "  "
	return style.Render(prefix + truncateRunes(msg.text, width-len([]rune(prefix))))
}

func truncateRunes(s string, n int) string {
	if n <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n == 1 {
		return "…"
	}
	return string(r[:n-1]) + "…"
}

func (m model) View() string {
	if m.width == 0 {
		return "Initializing..."
	}

	vizW, _ := vizSize(m.width, m.height)

	header := m.header(vizW)
	sep := styleSep.Render(strings.Repeat("─", vizW))
	viz := m.viz.render()

	boxW := m.width - styleBox.GetHorizontalBorderSize()
	boxH := m.height - styleBox.GetVerticalBorderSize()
	if boxW < 1 {
		boxW = 1
	}
	if boxH < 1 {
		boxH = 1
	}
	box := styleBox.Width(boxW).Height(boxH)
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Top,
		box.Render(header+"\n"+m.deck(vizW)+"\n"+sep+"\n"+viz))
}

// deck is the five rows between the header and the feed: sequencer and log on
// the left, the guard's panel with the keyboard on the right.
func (m model) deck(w int) string {
	if w < hudMinView {
		return m.beatBar() + "\n" + strings.Join(m.visibleLogs(w), "\n")
	}
	leftW := w - hudWidth - 3
	left := append([]string{m.beatBar()}, m.visibleLogs(leftW)...)
	right := m.hud()
	rows := make([]string, len(left))
	for i := range left {
		pad := leftW - lipgloss.Width(left[i])
		if pad < 0 {
			pad = 0
		}
		rows[i] = left[i] + strings.Repeat(" ", pad) + styleSep.Render(" │ ") + right[i]
	}
	return strings.Join(rows, "\n")
}

func (m model) clock() string {
	mins := (m.beats / stepCount) % 360
	hour := mins / 60
	if hour == 0 {
		hour = 12
	}
	return fmt.Sprintf("%2d:%02d AM", hour, mins%60)
}

func (m model) chordName() string {
	if m.chord == "" {
		return progression[0].name
	}
	return m.chord
}

// sectionView names the part of the arrangement playing now.
func (m model) sectionView() string {
	switch m.section {
	case "WARM":
		return styleGreen.Render(m.section)
	case "WORK":
		return styleAmber.Render(m.section)
	case "PEAK", "DROP":
		return styleMagenta.Render(m.section)
	case "":
		return styleDim.Render("IDLE")
	default:
		return styleDim.Render(m.section)
	}
}

func (m model) power() float64 {
	if m.hasContextPower {
		return clamp(m.contextPower, 0, 100)
	}
	return clamp(100-m.drain, 1, 100)
}

func (m model) stateLabel() string {
	switch m.agentState {
	case EvtThinking, EvtToolCall:
		return "MOTION"
	case EvtError:
		return "ALERT"
	default:
		return "SIGNAL"
	}
}

func (m model) header(w int) string {
	cam := "CAM " + m.viz.feed().cam
	clock := m.clock()
	night := fmt.Sprintf("NIGHT %d", m.night+1)
	state := m.stateLabel()
	if w < 44 {
		return styleGreen.Render(truncateRunes(
			fmt.Sprintf("%s %s %s", cam, strings.TrimSpace(clock), state), w))
	}
	rec := " REC"
	recStyle := styleFaint
	if m.beatStep%2 == 0 {
		rec = "●REC"
		recStyle = styleMagenta
	}
	left := styleGreen.Render(cam) + "  " + recStyle.Render(rec) + "  " + styleAmber.Render(clock)
	right := styleDim.Render(night) + "   " + m.stateView()
	gap := w - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		gap = 1
	}
	return left + strings.Repeat(" ", gap) + right
}

// beatBar is the step row of a drum machine: one LED per 16th, in bars of four.
func (m model) beatBar() string {
	var b strings.Builder
	for i := 0; i < stepCount; i++ {
		if i > 0 && i%4 == 0 {
			b.WriteString("  ")
		} else if i > 0 {
			b.WriteByte(' ')
		}
		switch {
		case i == m.beatStep:
			b.WriteString(styleMagenta.Render("■"))
		case i%4 == 0:
			b.WriteString(styleDim.Render("■"))
		default:
			b.WriteString(styleFaint.Render("□"))
		}
	}
	return b.String()
}

func (m model) stateView() string {
	switch m.agentState {
	case EvtThinking, EvtToolCall:
		return styleAmber.Render("MOTION")
	case EvtError:
		return styleMagenta.Render("ALERT")
	default:
		return styleGreen.Render("SIGNAL")
	}
}

// hud is the guard's panel: power, the keyboard, what it is playing, the tape.
func (m model) hud() []string {
	pwr := m.power()
	const barW = 16
	fill := int(pwr/100*barW + 0.5)
	pStyle := styleGreen
	switch {
	case pwr <= 20:
		pStyle = styleMagenta
	case pwr <= 50:
		pStyle = styleAmber
	}
	power := styleDim.Render("POWER ") +
		pStyle.Render(strings.Repeat("█", fill)) +
		styleFaint.Render(strings.Repeat("░", barW-fill)) +
		pStyle.Render(fmt.Sprintf(" %3.0f%%", pwr))

	lead := "--"
	if m.lastNote >= 0 && m.lastNote < len(noteNames) {
		lead = noteNames[m.lastNote]
	}
	notes := styleDim.Render("LEAD ") + styleAmber.Render(fmt.Sprintf("%-3s", lead)) +
		styleDim.Render("  CHORD ") + styleGreen.Render(fmt.Sprintf("%-2s", m.chordName())) +
		"  " + m.sectionView()

	// The usage meter is the arrangement's energy.
	usage := 1 + int(clamp(m.energy, 0, 0.999)*4)
	if usage > 4 {
		usage = 4
	}
	secs := m.beats * int(stepLength/time.Millisecond) / 1000
	tape := styleDim.Render("TAPE ") + styleGreen.Render("▶ SP ") +
		styleAmber.Render(fmt.Sprintf("%d:%02d:%02d", secs/3600, secs/60%60, secs%60)) +
		styleDim.Render("  USE ") + pStyle.Render(strings.Repeat("▮", usage)) +
		styleFaint.Render(strings.Repeat("▯", 4-usage))

	top, bottom := m.piano()
	rows := []string{power, top, bottom, notes, tape}
	for i, r := range rows {
		if pad := hudWidth - lipgloss.Width(r); pad > 0 {
			rows[i] = r + strings.Repeat(" ", pad)
		}
	}
	return rows
}

// pianoWhites are the white keys from A2 to E5, which covers the whole scale.
const pianoWhites = 19

var (
	pianoWhiteOf  = make([]int, len(scaleSemis)) // scale index -> white key
	pianoHasBlack [pianoWhites]bool              // a black key sits left of this white key
)

func init() {
	// White keys starting at A: A B C D E F G, as semitone offsets.
	whiteSemi := []int{0, 2, 3, 5, 7, 8, 10}
	sharpAfter := []bool{true, false, true, true, false, true, true}
	for k := 0; k < pianoWhites; k++ {
		semi := whiteSemi[k%7] + 12*(k/7)
		for i, s := range scaleSemis {
			if s == semi {
				pianoWhiteOf[i] = k
			}
		}
		if k > 0 {
			pianoHasBlack[k] = sharpAfter[(k-1)%7]
		}
	}
}

// piano draws a two-row keyboard. Amber is the key the last line landed on,
// green is the note the loop is playing now.
func (m model) piano() (string, string) {
	lit := func(k int) (lipgloss.Style, bool) {
		switch {
		case m.lastNote >= 0 && pianoWhiteOf[m.lastNote] == k:
			return keyLast, true
		case m.stepNote >= 0 && pianoWhiteOf[m.stepNote] == k:
			return keyStep, true
		}
		return keyWhite, false
	}
	var top, bottom strings.Builder
	for k := 0; k < pianoWhites; k++ {
		body, _ := lit(k)
		if pianoHasBlack[k] {
			top.WriteString(keyBlack.Render("█"))
		} else {
			top.WriteString(keyWhite.Render("▏"))
		}
		bottom.WriteString(keyWhite.Render("▏"))
		top.WriteString(body.Render(" "))
		bottom.WriteString(body.Render(" "))
	}
	top.WriteString(keyWhite.Render("▕"))
	bottom.WriteString(keyWhite.Render("▕"))
	return top.String(), bottom.String()
}

func (m *model) advanceViz() {
	if m.width == 0 {
		return
	}
	w, h := m.vizDims()
	chaos := 0
	switch m.agentState {
	case EvtToolCall:
		chaos = 1
	case EvtError:
		chaos = 2
	}
	m.viz.lowPower = m.power() <= 20
	m.viz.tick(w, h, m.beatStep, m.audioLevel, chaos)
}

func (m model) visibleLogs(width int) []string {
	const n = 4

	logs := m.logs
	if len(logs) > n {
		logs = logs[len(logs)-n:]
	}

	out := make([]string, 0, n)
	for _, entry := range logs {
		out = append(out, renderLog(entry, width))
	}
	for len(out) < n {
		out = append(out, "")
	}
	return out
}

// vizDims is the size of the camera feed for the current layout.
func (m model) vizDims() (int, int) {
	if m.panel {
		return panelVizSize(m.width, m.height)
	}
	return vizSize(m.width, m.height)
}

// startAudio opens the sound device and starts the synth playing. Keep the
// returned player referenced for as long as audio should play.
func startAudio() (*Synth, *Sequencer, *oto.Player, error) {
	ctx, ready, err := oto.NewContext(&oto.NewContextOptions{
		SampleRate:   sampleRate,
		ChannelCount: 1,
		Format:       oto.FormatSignedInt16LE,
	})
	if err != nil {
		return nil, nil, nil, fmt.Errorf("audio: %w", err)
	}
	<-ready

	synth := newSynth(sampleRate)
	seq := newSequencer(synth)
	player := ctx.NewPlayer(synth)
	player.Play()
	return synth, seq, player, nil
}

// runBeats steps the sequencer forever and reports each step. A slow reader
// misses beats rather than stalling the music.
func runBeats(seq *Sequencer, synth *Synth, out chan<- tea.Msg) {
	ticker := time.NewTicker(stepLength)
	defer ticker.Stop()

	step := 0
	for range ticker.C {
		b := seq.step(step)

		select {
		case out <- beatMsg{step: step, level: synth.Level(), note: b.note, chord: b.chord, section: b.section, energy: b.energy}:
		default:
		}

		step = (step + 1) % stepCount
	}
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "engine" {
		if err := runEngine(os.Stdin, os.Stdout); err != nil {
			fmt.Fprintf(os.Stderr, "engine: %v\n", err)
			os.Exit(1)
		}
		return
	}

	synth, seq, player, err := startAudio()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer player.Close()

	msgs := make(chan tea.Msg, 16)

	p := tea.NewProgram(
		model{msgs: msgs, logs: make([]textMsg, 0, 64), lastNote: -1, stepNote: -1},
		tea.WithAltScreen(),
		tea.WithInput(nil),
	)

	restoreEcho := silenceEcho()
	defer restoreEcho()

	go runBeats(seq, synth, msgs)

	go func() {
		scanner := bufio.NewScanner(os.Stdin)
		scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
		for scanner.Scan() {
			text := strings.TrimSpace(scanner.Text())
			if text == "" {
				continue
			}
			line := strings.ToLower(text)
			evt := classify(line)

			idx := seq.record(line, evt)
			msgs <- textMsg{text: text, event: evt, note: idx}
		}
	}()

	if _, err := p.Run(); err != nil && !errors.Is(err, tea.ErrInterrupted) {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	runtime.KeepAlive(player)
}
