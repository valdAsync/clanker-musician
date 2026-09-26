package main

import (
	"encoding/binary"
	"github.com/charmbracelet/lipgloss"
	"math"
	"math/rand"
	"os"
	"strings"
	"testing"
)

// renderLoop plays bars of the sequencer offline, feeding lines in as it goes,
// and returns the samples in -1..1.
func renderLoop(t *testing.T, synth *Synth, bars int, lines map[int]string) []float64 {
	t.Helper()
	seq := newSequencerRand(synth, rand.New(rand.NewSource(1)))
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

func TestEveryPatchRendersCleanly(t *testing.T) {
	lines := map[int]string{
		3:  "thinking about structure",
		9:  "tool_call: edit main.go",
		20: "output: ok",
		40: "error: tests failed",
		70: "tool_call: run tests",
	}
	for _, p := range patches {
		t.Run(p.name, func(t *testing.T) {
			out := renderLoop(t, newSynthPatch(sampleRate, p), 8, lines)

			var peak, sum float64
			for i, v := range out {
				if math.IsNaN(v) || math.IsInf(v, 0) {
					t.Fatalf("sample %d is %v", i, v)
				}
				peak = math.Max(peak, math.Abs(v))
				sum += v * v
			}
			if rms := math.Sqrt(sum / float64(len(out))); rms < 0.02 {
				t.Fatalf("nearly silent: rms %.4f", rms)
			}
			if peak > 0.23 {
				t.Fatalf("peak %.3f exceeds the soft-clip ceiling", peak)
			}

			// The kick has to punch through: the 20 ms after each bar's downbeat
			// should be clearly louder than the 20 ms before it.
			perStep := int(stepLength.Seconds() * sampleRate)
			win := sampleRate / 50
			var hit, gap float64
			for s := stepCount; s < 8*stepCount; s += stepCount {
				at := s * perStep
				hit += energy(out[at : at+win])
				gap += energy(out[at-win : at])
			}
			if hit < gap*1.5 {
				t.Fatalf("kick is buried: downbeat energy %.4f vs before %.4f", hit, gap)
			}

			if path := os.Getenv("CLANKER_WAV"); path != "" {
				writeWAV(t, strings.Replace(path, ".wav", "-"+normPatchName(p.name)+".wav", 1), out)
			}
		})
	}
}

func TestPatchNamesFitTheTapeRow(t *testing.T) {
	for _, p := range patches {
		m := model{lastNote: 3, stepNote: 8, patch: p.name, beats: 10 * 3600 * 8} // a 10-hour tape
		for i, row := range m.hud() {
			if w := lipgloss.Width(row); w > hudWidth {
				t.Fatalf("%s: hud row %d is %d wide, max %d", p.name, i, w, hudWidth)
			}
		}
	}
}

func TestPickPatchHonoursTheEnvironment(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	t.Setenv("CLANKER_PATCH", "neon rain")
	if got := pickPatch(rng).name; got != "NEON RAIN" {
		t.Fatalf("CLANKER_PATCH=neon rain picked %q", got)
	}
	t.Setenv("CLANKER_PATCH", "no such patch")
	if got := pickPatch(rng).name; got == "" {
		t.Fatal("unknown CLANKER_PATCH should fall back to a random patch")
	}
}

func TestPhrasesVaryAndStayInKey(t *testing.T) {
	seq := newSequencerRand(newSynth(sampleRate), rand.New(rand.NewSource(7)))
	inKey := map[string]bool{}
	for _, prog := range progressions {
		for _, c := range prog {
			inKey[c.name] = true
		}
	}
	progs, grooveNames, basses := map[[4]chord]bool{}, map[string]bool{}, map[string]bool{}
	for bar := 0; bar < 8*phraseBars; bar++ {
		seq.energy = 1                        // everything plays, so the bass can be checked
		seq.record("tool: edit", EvtToolCall) // the agent is busy, so phrases move on
		var barChord string
		for step := 0; step < stepCount; step++ {
			seq.synth.bass.freq = 0
			name := seq.step(step).chord
			if step == 0 {
				barChord = name
			} else if name != barChord {
				t.Fatalf("bar %d: chord changed mid-bar from %s to %s", bar, barChord, name)
			}
			if !inKey[name] {
				t.Fatalf("bar %d: chord %s is not in the key", bar, name)
			}
			c := seq.chord()
			if f := seq.synth.bass.freq; f != 0 && f != c.root && f != c.root*2 && f != c.root*1.5 {
				t.Fatalf("bar %d step %d: bass %.2f is not root, octave or fifth of %s", bar, step, f, c.name)
			}
		}
		progs[seq.ph.prog] = true
		grooveNames[seq.ph.groove.name] = true
		basses[seq.ph.bass] = true
	}
	if len(progs) < 2 || len(grooveNames) < 2 || len(basses) < 2 {
		t.Fatalf("8 phrases used %d progressions, %d grooves, %d bass lines; want variety",
			len(progs), len(grooveNames), len(basses))
	}
}

// playBars steps whole bars, calling input(bar) before each.
func playBars(seq *Sequencer, bars int, input func(bar int)) {
	for bar := 0; bar < bars; bar++ {
		if input != nil {
			input(bar)
		}
		for step := 0; step < stepCount; step++ {
			seq.step(step)
		}
	}
}

func TestPhrasesHoldWhileIdleAndMoveOnInput(t *testing.T) {
	seq := newSequencerRand(newSynth(sampleRate), rand.New(rand.NewSource(9)))
	start := seq.ph.motifA

	// Three phrases' worth of silence: the band keeps looping the same phrase.
	playBars(seq, 3*phraseBars, nil)
	if seq.ph.motifA != start || seq.phraseStart != 0 {
		t.Fatal("phrase changed with no input")
	}

	// Input mid-phrase changes nothing until the next phrase boundary.
	playBars(seq, 3, nil)
	seq.record("tool: edit", EvtToolCall)
	playBars(seq, phraseBars-3, nil) // the rest of the phrase
	if seq.phraseStart != 0 {
		t.Fatal("phrase changed before the boundary")
	}
	playBars(seq, 1, nil) // the boundary bar
	if seq.phraseStart != seq.bar {
		t.Fatalf("no new phrase at the boundary after input (start %d, bar %d)", seq.phraseStart, seq.bar)
	}
}

func TestLongIdleDriftsToANewPhrase(t *testing.T) {
	seq := newSequencerRand(newSynth(sampleRate), rand.New(rand.NewSource(4)))
	playBars(seq, idleDrift/stepCount+phraseBars, nil)
	if seq.phraseStart == 0 {
		t.Fatalf("still on the first phrase after %d idle bars", seq.bar+1)
	}
}

func TestDropSlamsIntoAFreshPhrase(t *testing.T) {
	seq := newSequencerRand(newSynth(sampleRate), rand.New(rand.NewSource(2)))
	playBars(seq, 2, nil)
	seq.record("error: boom", EvtError)
	playBars(seq, 1, nil) // the drop bar
	if seq.section() != "DROP" {
		t.Fatalf("section %q, want DROP", seq.section())
	}
	seq.step(0) // the slam
	if seq.phraseStart != seq.bar {
		t.Fatalf("slam on bar %d kept the old phrase from bar %d", seq.bar, seq.phraseStart)
	}
	if got, want := seq.chord().name, seq.ph.prog[0].name; got != want {
		t.Fatalf("slam chord %s, want the new phrase's first chord %s", got, want)
	}
}

func TestEveryGrooveKicksOnTheDownbeat(t *testing.T) {
	for _, g := range grooves {
		if g.kick[0] == 0 {
			t.Fatalf("groove %s has no kick on beat one; drops couldn't slam back in", g.name)
		}
	}
	for _, i := range grooveOdds {
		if i < 0 || i >= len(grooves) {
			t.Fatalf("grooveOdds points at groove %d", i)
		}
	}
}

func TestMotifAnswersStayOnTheGrid(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	for i := 0; i < 200; i++ {
		a, b := newMotif(rng)
		hitsA, hitsB := 0, 0
		for s := 0; s < stepCount; s++ {
			for _, d := range []int{a[s], b[s]} {
				if d < -1 || d > 3 {
					t.Fatalf("motif degree %d out of range", d)
				}
			}
			if a[s] >= 0 {
				hitsA++
			}
			if b[s] >= 0 {
				hitsB++
			}
		}
		if hitsA < 4 || hitsA > 7 || hitsB != hitsA {
			t.Fatalf("motif has %d hits and answer %d; want 4–7 each, equal", hitsA, hitsB)
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
