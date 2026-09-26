package main

import (
	"encoding/binary"
	"math"
	"os"
	"testing"
)

// renderLoop plays bars of the sequencer offline, feeding lines in as it goes,
// and returns the samples in -1..1.
func renderLoop(t *testing.T, bars int, lines map[int]string) []float64 {
	t.Helper()
	synth := newSynth(sampleRate)
	seq := newSequencer(synth)
	perStep := int(stepLength.Seconds() * sampleRate)
	buf := make([]byte, perStep*2)
	var out []float64
	for i := 0; i < bars*stepCount; i++ {
		if line, ok := lines[i]; ok {
			seq.record(line, classify(line))
		}
		seq.step(i % stepCount)
		if _, err := synth.Read(buf); err != nil {
			t.Fatal(err)
		}
		for j := 0; j < perStep; j++ {
			out = append(out, float64(int16(binary.LittleEndian.Uint16(buf[2*j:])))/math.MaxInt16)
		}
	}
	return out
}

func TestSynthRendersCleanLoop(t *testing.T) {
	lines := map[int]string{
		3:  "thinking about structure",
		9:  "tool_call: edit main.go",
		20: "output: ok",
		40: "error: tests failed",
		70: "tool_call: run tests",
	}
	out := renderLoop(t, 8, lines)

	var peak, sum float64
	for i, v := range out {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			t.Fatalf("sample %d is %v", i, v)
		}
		peak = math.Max(peak, math.Abs(v))
		sum += v * v
	}
	rms := math.Sqrt(sum / float64(len(out)))
	if rms < 0.02 {
		t.Fatalf("loop is nearly silent: rms %.4f", rms)
	}
	if peak > 0.23 {
		t.Fatalf("peak %.3f exceeds the soft-clip ceiling", peak)
	}

	// The kick has to punch through: the 20 ms after each downbeat should be
	// clearly louder than the 20 ms just before it.
	perStep := int(stepLength.Seconds() * sampleRate)
	win := sampleRate / 50
	var hit, gap float64
	for s := 4; s < 8*stepCount; s += 4 {
		at := s * perStep
		hit += energy(out[at : at+win])
		gap += energy(out[at-win : at])
	}
	if hit < gap*1.5 {
		t.Fatalf("kick is buried: downbeat energy %.4f vs before %.4f", hit, gap)
	}

	if path := os.Getenv("CLANKER_WAV"); path != "" {
		writeWAV(t, path, out)
	}
}

func TestProgressionCyclesAndBassBounces(t *testing.T) {
	seq := newSequencer(newSynth(sampleRate))
	seq.energy = 1 // bass only plays from WARM up
	var names []string
	for bar := 0; bar < 5; bar++ {
		for step := 0; step < stepCount; step++ {
			name := seq.step(step).chord
			if step == 0 {
				names = append(names, name)
			}
			if step == 2 && seq.synth.bass.freq != seq.chord().root*2 {
				t.Fatalf("bar %d: off-beat bass %.2f, want octave of %.2f", bar, seq.synth.bass.freq, seq.chord().root)
			}
		}
	}
	want := []string{"Am", "F", "C", "G", "Am"}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("chords %v, want %v", names, want)
		}
	}
}

func TestArrangementFollowsEnergy(t *testing.T) {
	seq := newSequencer(newSynth(sampleRate))
	bar := func() []beatInfo {
		out := make([]beatInfo, stepCount)
		for step := range out {
			out[step] = seq.step(step)
		}
		return out
	}

	// Nothing has happened yet: a breakdown with no bass or snare.
	if b := bar(); b[0].section != "IDLE" {
		t.Fatalf("start section %q, want IDLE", b[0].section)
	}
	if seq.synth.bass.amp != 0 || seq.synth.snare.amp != 0 {
		t.Fatal("bass and snare should wait for energy")
	}

	for i := 0; i < 8; i++ {
		seq.record("tool_call: edit", EvtToolCall)
	}
	if b := bar(); b[0].section != "PEAK" {
		t.Fatalf("after a tool burst section %q, want PEAK", b[0].section)
	}

	// An error mid-bar rolls the snare into the bar line...
	for step := 0; step < 10; step++ {
		seq.step(step)
	}
	seq.record("error: boom", EvtError)
	seq.synth.snare.amp = 0
	for step := 10; step < stepCount; step++ {
		seq.step(step)
	}
	if seq.synth.snare.amp == 0 {
		t.Fatal("expected a snare roll into the drop")
	}
	// ...then a bar of pad only, then the kick slams back in.
	seq.synth.kick.amp, seq.synth.bass.amp = 0, 0
	if b := bar(); b[0].section != "DROP" {
		t.Fatalf("section %q after an error, want DROP", b[0].section)
	}
	if seq.synth.kick.amp != 0 || seq.synth.bass.amp != 0 {
		t.Fatal("drop bar should only play the pad")
	}
	seq.step(0)
	if seq.synth.kick.amp != 1 {
		t.Fatalf("kick after the drop %.2f, want a full slam", seq.synth.kick.amp)
	}

	// Silence drains it back to IDLE, and old melody notes leave the loop.
	var last beatInfo
	for i := 0; i < noteLife/stepCount+2; i++ {
		b := bar()
		last = b[0]
	}
	if last.section != "IDLE" {
		t.Fatalf("after silence section %q, want IDLE", last.section)
	}
	for step, n := range seq.notes {
		if n != 0 {
			t.Fatalf("step %d still holds a note after %d steps", step, noteLife)
		}
	}
}

func TestFilterStaysStableAtExtremes(t *testing.T) {
	var f svf
	for i := 0; i < sampleRate; i++ {
		in := 1.0
		if i%2 == 0 {
			in = -1
		}
		low, high := f.run(in, 20000, 0.1)
		if math.IsNaN(low) || math.Abs(low) > 50 || math.Abs(high) > 50 {
			t.Fatalf("filter blew up at %d: %v %v", i, low, high)
		}
	}
}

func energy(s []float64) float64 {
	var e float64
	for _, v := range s {
		e += v * v
	}
	return e
}

func writeWAV(t *testing.T, path string, samples []float64) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	n := uint32(len(samples) * 2)
	hdr := []any{
		[]byte("RIFF"), 36 + n, []byte("WAVEfmt "), uint32(16), uint16(1), uint16(1),
		uint32(sampleRate), uint32(sampleRate * 2), uint16(2), uint16(16), []byte("data"), n,
	}
	for _, v := range hdr {
		if err := binary.Write(f, binary.LittleEndian, v); err != nil {
			t.Fatal(err)
		}
	}
	for _, v := range samples {
		if err := binary.Write(f, binary.LittleEndian, int16(v*math.MaxInt16)); err != nil {
			t.Fatal(err)
		}
	}
}
