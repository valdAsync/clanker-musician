package main

import (
	"bufio"
	"encoding/json"
	"io"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// Engine mode is how the pi extension runs clanker-musician: the same synth,
// sequencer and visualizer, but instead of taking over the terminal it trades
// JSON lines over stdin/stdout and pi draws the frames in its side column.
//
//	in:  {"type":"event","kind":"output|thinking|tool|error|idle","text":"..."}
//	     {"type":"resize","width":60,"height":40}
//	     {"type":"mute","on":true}
//	     {"type":"power","percent":69.3}       context window left, shown as POWER
//	out: {"type":"ready"}
//	     {"type":"frame","lines":["...", ...]}   about 20 per second, on change
//	     {"type":"error","message":"..."}
//
// Closing stdin stops the engine, so it never outlives pi.

type engineIn struct {
	Type    string  `json:"type"`
	Kind    string  `json:"kind"`
	Text    string  `json:"text"`
	Width   int     `json:"width"`
	Height  int     `json:"height"`
	On      bool    `json:"on"`
	Percent float64 `json:"percent"`
}

type engineOut struct {
	Type    string   `json:"type"`
	Lines   []string `json:"lines,omitempty"`
	Message string   `json:"message,omitempty"`
}

var engineKinds = map[string]EventType{
	"output":   EvtOutput,
	"thinking": EvtThinking,
	"tool":     EvtToolCall,
	"error":    EvtError,
}

// streamGap throttles streamed reply and thinking lines. A long answer arrives
// as a burst of lines, and turning every one into a note would pin the music
// at PEAK. Tool calls and errors are never dropped.
const streamGap = 250 * time.Millisecond

func runEngine(in io.Reader, out io.Writer) error {
	w := bufio.NewWriter(out)
	enc := json.NewEncoder(w)
	fail := func(err error) error {
		enc.Encode(engineOut{Type: "error", Message: err.Error()})
		w.Flush()
		return err
	}

	unlock, err := lockEngine()
	if err != nil {
		return fail(err)
	}
	defer unlock()

	synth, seq, player, err := startAudio()
	if err != nil {
		return fail(err)
	}
	defer player.Close()

	beats := make(chan tea.Msg, 16)
	go runBeats(seq, synth, beats)
	return engineLoop(in, w, enc, synth, seq, beats, time.NewTicker(vizTickEvery).C)
}

func engineLoop(in io.Reader, w *bufio.Writer, enc *json.Encoder, synth *Synth, seq *Sequencer, beats <-chan tea.Msg, ticks <-chan time.Time) error {
	// stdout is a pipe, so lipgloss would otherwise decide there is no colour.
	lipgloss.SetColorProfile(termenv.ANSI256)

	inputs := make(chan engineIn, 64)
	done := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(in)
		scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
		for scanner.Scan() {
			var msg engineIn
			if json.Unmarshal(scanner.Bytes(), &msg) == nil {
				inputs <- msg
			}
		}
		done <- scanner.Err() // nil on EOF: pi closed the pipe
	}()

	if err := enc.Encode(engineOut{Type: "ready"}); err != nil {
		return nil
	}
	if err := w.Flush(); err != nil {
		return nil
	}

	m := model{panel: true, lastNote: -1, stepNote: -1}
	lastStream := map[EventType]time.Time{}
	var lastFrame string
	for {
		select {
		case err := <-done:
			return err

		case msg := <-inputs:
			switch msg.Type {
			case "resize":
				m = step(m, tea.WindowSizeMsg{Width: msg.Width, Height: msg.Height})
			case "mute":
				synth.setMuted(msg.On)
			case "power":
				m.contextPower, m.hasContextPower = msg.Percent, true
			case "event":
				if msg.Kind == "idle" {
					seq.settle()
					m.agentState = EvtOutput
					continue
				}
				evt, ok := engineKinds[msg.Kind]
				if !ok {
					continue
				}
				if evt == EvtOutput || evt == EvtThinking {
					if time.Since(lastStream[evt]) < streamGap {
						continue
					}
					lastStream[evt] = time.Now()
				}
				text := strings.TrimSpace(msg.Text)
				if text == "" {
					text = msg.Kind
				}
				idx := seq.record(strings.ToLower(text), evt)
				m = step(m, textMsg{text: text, event: evt, note: idx})
			}

		case b := <-beats:
			m = step(m, b)

		case <-ticks:
			m = step(m, vizTickMsg{})
			frame := m.panelView()
			if frame == "" || frame == lastFrame {
				continue
			}
			lastFrame = frame
			if err := enc.Encode(engineOut{Type: "frame", Lines: strings.Split(frame, "\n")}); err != nil {
				return nil // pi is gone
			}
			if err := w.Flush(); err != nil {
				return nil
			}
		}
	}
}

// step runs one message through the same Update the full-screen app uses.
func step(m model, msg tea.Msg) model {
	next, _ := m.Update(msg)
	return next.(model)
}
