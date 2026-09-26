package main

import (
	"bufio"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// engineHarness runs engineLoop over pipes with a hand-cranked frame clock.
type engineHarness struct {
	t     *testing.T
	in    *io.PipeWriter
	out   chan engineOut
	ticks chan time.Time
	beats chan tea.Msg
	seq   *Sequencer
	done  chan error
}

func newEngineHarness(t *testing.T) *engineHarness {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	synth := newSynth(sampleRate)
	h := &engineHarness{
		t:     t,
		in:    inW,
		out:   make(chan engineOut, 16),
		ticks: make(chan time.Time),
		beats: make(chan tea.Msg),
		seq:   newSequencer(synth),
		done:  make(chan error, 1),
	}
	go func() {
		sc := bufio.NewScanner(outR)
		sc.Buffer(make([]byte, 0, 1<<20), 1<<22)
		for sc.Scan() {
			var msg engineOut
			if err := json.Unmarshal(sc.Bytes(), &msg); err == nil {
				h.out <- msg
			}
		}
		close(h.out)
	}()
	go func() {
		w := bufio.NewWriter(outW)
		h.done <- engineLoop(inR, w, json.NewEncoder(w), synth, h.seq, h.beats, h.ticks)
		outW.Close()
	}()
	if got := h.read(); got.Type != "ready" {
		t.Fatalf("first message %q, want ready", got.Type)
	}
	return h
}

func (h *engineHarness) send(msg engineIn) {
	h.t.Helper()
	b, _ := json.Marshal(msg)
	if _, err := h.in.Write(append(b, '\n')); err != nil {
		h.t.Fatal(err)
	}
}

func (h *engineHarness) read() engineOut {
	h.t.Helper()
	select {
	case msg, ok := <-h.out:
		if !ok {
			h.t.Fatal("engine output ended")
		}
		return msg
	case <-time.After(2 * time.Second):
		h.t.Fatal("no message from the engine")
	}
	return engineOut{}
}

// frame advances the clock until the engine emits a frame. Inputs travel
// through a pipe, so a tick can overtake them; the engine skips frames it
// can't draw yet, and we just tick again.
func (h *engineHarness) frame() []string {
	h.t.Helper()
	for i := 0; i < 50; i++ {
		h.ticks <- time.Now()
		select {
		case msg, ok := <-h.out:
			if !ok {
				h.t.Fatal("engine output ended")
			}
			if msg.Type != "frame" {
				h.t.Fatalf("got %q, want frame", msg.Type)
			}
			return msg.Lines
		case <-time.After(20 * time.Millisecond):
		}
	}
	h.t.Fatal("engine never produced a frame")
	return nil
}

// frameWhere ticks until a frame satisfies want, since inputs and ticks race.
func (h *engineHarness) frameWhere(want func([]string) bool) []string {
	h.t.Helper()
	var lines []string
	for i := 0; i < 50; i++ {
		lines = h.frame()
		if want(lines) {
			return lines
		}
	}
	h.t.Fatalf("no frame matched; last header %q", stripANSI(lines[0]))
	return nil
}

func headerHas(word string) func([]string) bool {
	return func(lines []string) bool { return strings.Contains(stripANSI(lines[0]), word) }
}

func TestEngineDrawsTheColumnAtItsSize(t *testing.T) {
	h := newEngineHarness(t)
	for _, size := range [][2]int{{60, 40}, {44, 30}, {70, 24}} {
		h.send(engineIn{Type: "resize", Width: size[0], Height: size[1]})
		lines := h.frameWhere(func(l []string) bool { return len(l) == size[1] })
		if len(lines) != size[1] {
			t.Fatalf("%dx%d: %d lines, want %d", size[0], size[1], len(lines), size[1])
		}
		for i, l := range lines {
			if got := lipgloss.Width(l); got > size[0] {
				t.Fatalf("%dx%d: line %d is %d wide", size[0], size[1], i, got)
			}
		}
		if !strings.Contains(strings.Join(lines, ""), "\x1b[") {
			t.Fatal("frame has no colour; the colour profile was not forced")
		}
	}
}

func TestEngineEventsReachTheMusicAndPicture(t *testing.T) {
	h := newEngineHarness(t)
	h.send(engineIn{Type: "resize", Width: 60, Height: 40})
	h.frame()

	h.send(engineIn{Type: "event", Kind: "tool", Text: "tool: Edit main.go"})
	h.send(engineIn{Type: "event", Kind: "error", Text: "boom"})
	h.frameWhere(headerHas("ALERT"))
	h.seq.mu.Lock()
	energy, dropNext := h.seq.energy, h.seq.dropNext
	h.seq.mu.Unlock()
	if energy == 0 || !dropNext {
		t.Fatalf("events did not reach the sequencer: energy %.2f dropNext %v", energy, dropNext)
	}

	h.send(engineIn{Type: "event", Kind: "idle"})
	h.frameWhere(headerHas("SIGNAL"))
}

func TestEnginePowerShowsContextLeft(t *testing.T) {
	h := newEngineHarness(t)
	h.send(engineIn{Type: "resize", Width: 60, Height: 40})
	h.send(engineIn{Type: "power", Percent: 69.3})
	h.frameWhere(func(lines []string) bool {
		for _, l := range lines {
			if strings.Contains(stripANSI(l), "POWER") && strings.Contains(stripANSI(l), " 69%") {
				return true
			}
		}
		return false
	})
}

func TestEngineThrottlesStreamedOutput(t *testing.T) {
	h := newEngineHarness(t)
	for i := 0; i < 20; i++ {
		h.send(engineIn{Type: "event", Kind: "output", Text: "a streamed line"})
	}
	h.send(engineIn{Type: "resize", Width: 60, Height: 40}) // a sync point: everything above is handled
	h.frame()
	h.seq.mu.Lock()
	energy := h.seq.energy
	h.seq.mu.Unlock()
	if energy > energyBump[EvtOutput]+1e-9 {
		t.Fatalf("a burst of 20 lines added %.2f energy, want one line's worth", energy)
	}
}

func TestEngineStopsWhenPiClosesStdin(t *testing.T) {
	h := newEngineHarness(t)
	h.in.Close()
	select {
	case err := <-h.done:
		if err != nil {
			t.Fatalf("engine returned %v on EOF", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("engine kept running after stdin closed")
	}
}

func TestEngineLockAllowsOneAtATime(t *testing.T) {
	unlock, err := lockEngine()
	if err != nil {
		t.Skipf("lock unavailable here: %v", err)
	}
	if _, err := lockEngine(); err != errEngineBusy {
		t.Fatalf("second lock: %v, want errEngineBusy", err)
	}
	unlock()
	again, err := lockEngine()
	if err != nil {
		t.Fatalf("lock after unlock: %v", err)
	}
	again()
}

func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b {
			for i < len(s) && s[i] != 'm' {
				i++
			}
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}
