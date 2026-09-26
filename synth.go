package main

import (
	"math"
	"math/rand"
	"sync"
	"time"
)

// --- MUSICAL MATERIAL ---

// scale is A minor pentatonic over three octaves. Arbitrary text is folded onto
// it so every event produces something that sounds intentional, and it sits on
// every chord of the progression.
var scale = []float64{110.00, 146.83, 164.81, 220.00, 261.63, 293.66, 329.63, 392.00, 440.00, 523.25, 659.25}

var noteNames = []string{"A2", "D3", "E3", "A3", "C4", "D4", "E4", "G4", "A4", "C5", "E5"}

// scaleSemis is each scale note in semitones above A2, for the piano strip.
var scaleSemis = []int{0, 5, 7, 12, 15, 17, 19, 22, 24, 27, 31}

const (
	sampleRate = 44100
	stepCount  = 16
	stepLength = 125 * time.Millisecond

	// sweepBars is how long the filter takes to open and close again.
	sweepBars = 8
)

// chord is one bar of the progression: a bass root and a three-note pad.
type chord struct {
	name string
	root float64
	pad  [3]float64
}

// progression is the synthwave staple i–VI–III–VII in A minor, one bar each.
var progression = []chord{
	{"Am", 55.00, [3]float64{220.00, 261.63, 329.63}},
	{"F", 43.65, [3]float64{220.00, 261.63, 349.23}},
	{"C", 65.41, [3]float64{196.00, 261.63, 329.63}},
	{"G", 49.00, [3]float64{196.00, 246.94, 293.66}},
}

func noteFor(line string) (float64, int) {
	hash := 0
	for _, b := range []byte(line) {
		hash += int(b)
	}
	idx := 4 + hash%(len(scale)-4)
	return scale[idx], idx
}

// --- DSP BUILDING BLOCKS ---

// decayFor is the per-sample multiplier that fades to -60 dB in secs.
func decayFor(secs float64) float64 {
	return math.Exp(math.Log(0.001) / (secs * sampleRate))
}

// polyBLEP rounds off a waveform's jump so high notes don't alias into fizz.
func polyBLEP(t, dt float64) float64 {
	switch {
	case t < dt:
		t /= dt
		return t + t - t*t - 1
	case t > 1-dt:
		t = (t - 1) / dt
		return t*t + t + t + 1
	}
	return 0
}

type osc struct{ phase float64 }

func (o *osc) advance(dt float64) {
	o.phase += dt
	o.phase -= math.Floor(o.phase)
}

func (o *osc) saw(freq float64) float64 {
	dt := freq / sampleRate
	s := 2*o.phase - 1 - polyBLEP(o.phase, dt)
	o.advance(dt)
	return s
}

func (o *osc) pulse(freq, width float64) float64 {
	dt := freq / sampleRate
	s := -1.0
	if o.phase < width {
		s = 1
	}
	s += polyBLEP(o.phase, dt)
	s -= polyBLEP(math.Mod(o.phase+1-width, 1), dt)
	o.advance(dt)
	return s
}

func (o *osc) sine(freq float64) float64 {
	s := math.Sin(2 * math.Pi * o.phase)
	o.advance(freq / sampleRate)
	return s
}

// svf is a two-pole state-variable filter. damp is 1/Q: lower rings more.
type svf struct{ low, band float64 }

func (f *svf) run(in, cutoff, damp float64) (low, high float64) {
	cutoff = clamp(cutoff, 20, sampleRate/7) // the Chamberlin form goes unstable above this
	g := 2 * math.Sin(math.Pi*cutoff/sampleRate)
	f.low += g * f.band
	high = in - f.low - damp*f.band
	f.band += g * high
	return f.low, high
}

// --- VOICES ---

// lead is two detuned saws through a plucked low-pass: the synthwave lead.
// Errors swap it for a hollow square.
type lead struct {
	a, b     osc
	filt     svf
	freq     float64
	amp, env float64
	square   bool
}

func (v *lead) next(sweep float64) float64 {
	if v.amp < 1e-4 {
		return 0
	}
	var s float64
	if v.square {
		s = v.a.pulse(v.freq, 0.5) * 0.7
	} else {
		s = (v.a.saw(v.freq*1.006) + v.b.saw(v.freq*0.994)) * 0.5
	}
	out, _ := v.filt.run(s, 500+sweep*1800+v.env*2600, 0.8)
	out *= v.amp
	v.amp *= leadDecay
	v.env *= leadEnvDecay
	return out
}

// arp is the chiptune bit: a 25% pulse flicking through chord tones every
// 50 ms, like a console faking a chord on one channel.
type arp struct {
	o     osc
	notes [4]float64
	idx   int
	left  int
	amp   float64
}

const arpTick = sampleRate / 20

func (v *arp) next() float64 {
	if v.amp < 1e-4 {
		return 0
	}
	if v.left <= 0 {
		v.left = arpTick
		v.idx = (v.idx + 1) % len(v.notes)
	}
	v.left--
	s := v.o.pulse(v.notes[v.idx], 0.25) * v.amp
	v.amp *= arpDecay
	return s
}

// bass is a plucked saw with a sine under it, the filter opening with the sweep.
type bass struct {
	o, sub   osc
	filt     svf
	freq     float64
	amp, env float64
}

func (v *bass) next(sweep float64) float64 {
	if v.amp < 1e-4 {
		return 0
	}
	s := v.o.saw(v.freq)*0.7 + v.sub.sine(v.freq)*0.6
	out, _ := v.filt.run(s, 220+sweep*500+v.env*900, 0.7)
	out *= v.amp
	v.amp *= bassDecay
	v.env *= bassEnvDecay
	return out
}

// pad holds the chord: six saws, dark and slow, swelling in on each change.
type pad struct {
	osc   [6]osc
	filt  svf
	notes [3]float64
	amp   float64
}

func (v *pad) next(sweep float64) float64 {
	if v.notes[0] == 0 {
		return 0
	}
	var s float64
	for i, n := range v.notes {
		s += v.osc[2*i].saw(n*1.004) + v.osc[2*i+1].saw(n*0.996)
	}
	v.amp += (1 - v.amp) * padAttack
	out, _ := v.filt.run(s/6, 450+sweep*1500, 1.1)
	return out * v.amp
}

// bell is a soft sine pluck with a glassy overtone, for the thinking arpeggio.
type bell struct {
	a, b      osc
	freq, amp float64
}

func (v *bell) next() float64 {
	if v.amp < 1e-4 {
		return 0
	}
	s := (v.a.sine(v.freq) + 0.3*v.b.sine(v.freq*3)) * v.amp
	v.amp *= bellDecay
	return s
}

// kick is a sine with a fast pitch drop.
type kick struct {
	o         osc
	amp, bend float64
}

func (v *kick) next() float64 {
	if v.amp < 1e-4 {
		return 0
	}
	s := v.o.sine(45+120*v.bend) * v.amp
	v.amp *= kickDecay
	v.bend *= kickBendDecay
	return s
}

// noise covers the snare and both hats: filtered noise plus an optional tone.
type noise struct {
	filt       svf
	cutoff     float64
	decay      float64
	amp        float64
	body       osc
	bodyFreq   float64
	bodyAmp    float64
	bodyDecay  float64
	passBright bool // true: high-pass (hats, snare sizzle)
}

func (v *noise) next() float64 {
	if v.amp < 1e-4 && v.bodyAmp < 1e-4 {
		return 0
	}
	low, high := v.filt.run(rand.Float64()*2-1, v.cutoff, 1)
	s := low
	if v.passBright {
		s = high
	}
	s *= v.amp
	v.amp *= v.decay
	if v.bodyAmp >= 1e-4 {
		s += v.body.sine(v.bodyFreq) * v.bodyAmp
		v.bodyAmp *= v.bodyDecay
	}
	return s
}

var (
	leadDecay     = decayFor(0.4)
	leadEnvDecay  = decayFor(0.12)
	arpDecay      = decayFor(0.7)
	bellDecay     = decayFor(0.6)
	bassDecay     = decayFor(0.28)
	bassEnvDecay  = decayFor(0.08)
	padAttack     = 1 / (0.6 * sampleRate)
	kickDecay     = decayFor(0.32)
	kickBendDecay = decayFor(0.05)
	duckRelease   = decayFor(0.4)
)

// --- SYNTH ---

type Synth struct {
	lead  lead
	arp   arp
	bell  bell
	bass  bass
	pad   pad
	kick  kick
	snare noise
	hat   noise
	ohat  noise

	duck  float64 // sidechain: 1 right after a kick, pumps the bass and pad
	sweep float64 // 0..1 filter opening, set by the sequencer

	delay      []float64
	delayIndex int
	level      float64
	muted      bool // keeps playing silently, so the visuals still follow the music

	mu sync.Mutex
}

func newSynth(rate float64) *Synth {
	s := &Synth{delay: make([]float64, int(rate*0.375))} // dotted eighth at 120 BPM
	s.snare = noise{cutoff: 1500, decay: decayFor(0.25), passBright: true, bodyFreq: 185, bodyDecay: decayFor(0.07)}
	s.hat = noise{cutoff: 6000, decay: decayFor(0.05), passBright: true}
	s.ohat = noise{cutoff: 5000, decay: decayFor(0.3), passBright: true}
	return s
}

func (s *Synth) Read(buf []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var peak float64
	frames := len(buf) / 2
	for i := 0; i < frames; i++ {
		pump := 1 - 0.65*s.duck
		s.duck *= duckRelease

		lead := s.lead.next(s.sweep) * 0.36
		arp := s.arp.next() * 0.20
		bell := s.bell.next() * 0.18
		snare := s.snare.next() * 0.50

		dry := s.kick.next()*0.95 + snare +
			s.hat.next()*0.16 + s.ohat.next()*0.13 +
			(s.bass.next(s.sweep)*0.65+s.pad.next(s.sweep)*0.18)*pump +
			lead + arp + bell

		// Only the melodic parts and the snare go to the echo; kick and bass
		// stay dry so the low end doesn't turn to mud.
		delayed := s.delay[s.delayIndex]
		s.delay[s.delayIndex] = lead + arp + bell + snare*0.35 + delayed*0.4
		s.delayIndex++
		if s.delayIndex == len(s.delay) {
			s.delayIndex = 0
		}

		mix := dry + delayed*0.5
		if abs := math.Abs(mix); abs > peak {
			peak = abs
		}

		var sample int16
		if !s.muted {
			sample = int16(math.Tanh(mix*0.9) * 0.22 * math.MaxInt16)
		}
		buf[2*i] = byte(sample)
		buf[2*i+1] = byte(sample >> 8)
	}

	s.level = s.level*0.7 + peak*0.3
	return frames * 2, nil
}

func (s *Synth) Level() float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.level
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func (s *Synth) triggerKick(amp float64) {
	s.mu.Lock()
	s.kick.o.phase = 0
	s.kick.amp, s.kick.bend = amp, 1
	s.duck = amp
	s.mu.Unlock()
}

func (s *Synth) triggerSnare(amp float64) {
	s.mu.Lock()
	s.snare.amp, s.snare.bodyAmp = amp, amp*0.6
	s.mu.Unlock()
}

func (s *Synth) triggerHat(amp float64, open bool) {
	s.mu.Lock()
	if open {
		s.ohat.amp = amp
	} else {
		s.hat.amp = amp
		s.ohat.amp *= 0.2 // a closed hat chokes the open one, like on a real kit
	}
	s.mu.Unlock()
}

func (s *Synth) triggerLead(freq float64, square bool, amp float64) {
	s.mu.Lock()
	s.lead.freq, s.lead.square = freq, square
	s.lead.amp, s.lead.env = amp, 1
	s.mu.Unlock()
}

func (s *Synth) triggerBell(freq float64) {
	s.mu.Lock()
	s.bell.freq, s.bell.amp = freq, 1
	s.mu.Unlock()
}

func (s *Synth) triggerArp(c chord) {
	s.mu.Lock()
	s.arp.notes = [4]float64{c.pad[0] * 2, c.pad[1] * 2, c.pad[2] * 2, c.pad[0] * 4}
	s.arp.idx, s.arp.left, s.arp.amp = 0, arpTick, 1
	s.mu.Unlock()
}

func (s *Synth) triggerBass(freq float64) {
	s.mu.Lock()
	s.bass.freq = freq
	s.bass.amp, s.bass.env = 1, 1
	s.mu.Unlock()
}

func (s *Synth) setChord(c chord) {
	s.mu.Lock()
	s.pad.notes = c.pad
	s.pad.amp *= 0.5 // dip and swell back in, so each change breathes
	s.mu.Unlock()
}

func (s *Synth) setMuted(on bool) {
	s.mu.Lock()
	s.muted = on
	s.mu.Unlock()
}

func (s *Synth) setSweep(v float64) {
	s.mu.Lock()
	s.sweep = v
	s.mu.Unlock()
}

// --- SEQUENCER ---

// The arrangement follows the agent. Every line adds energy, silence lets it
// drain, and the energy decides which layers play:
//
//	IDLE  pad, soft kick, hats: a breakdown while nothing happens
//	WARM  + bass and the off-beat open hat
//	WORK  + snare backbeat, sixteenth hats, the filter opens up
//	PEAK  + an arpeggio layer and extra open hats
//	DROP  one bar of pad only after an error, then the beat slams back in
const (
	energyDecay = 0.992 // per step: halves in ~11 s of silence
	noteLife    = 256   // steps a melody note survives in the loop (~32 s)

	levelWarm = 0.15
	levelWork = 0.4
	levelPeak = 0.7
)

// energyBump is how much each kind of line pushes the arrangement.
var energyBump = map[EventType]float64{
	EvtOutput:   0.08,
	EvtThinking: 0.08,
	EvtToolCall: 0.12,
	EvtError:    0.05,
}

// beatInfo is what one step reports back to the UI.
type beatInfo struct {
	note    int // lead note played, -1 for none
	chord   string
	section string
	energy  float64
}

type Sequencer struct {
	synth *Synth

	mu        sync.Mutex
	notes     [stepCount]float64
	noteIdx   [stepCount]int
	square    [stepCount]bool
	writtenAt [stepCount]int
	head      int
	bar       int
	ticks     int
	energy    float64
	dropNext  bool // an error arrived; the next bar is a drop
	dropping  bool // this bar is the drop
	current   EventType
}

func newSequencer(synth *Synth) *Sequencer {
	q := &Sequencer{synth: synth, bar: -1}
	for i := range q.noteIdx {
		q.noteIdx[i] = -1
	}
	return q
}

func (q *Sequencer) chord() chord {
	return progression[max(q.bar, 0)%len(progression)]
}

// section names the current layer set. Call with q.mu held.
func (q *Sequencer) section() string {
	switch {
	case q.dropping:
		return "DROP"
	case q.energy >= levelPeak:
		return "PEAK"
	case q.energy >= levelWork:
		return "WORK"
	case q.energy >= levelWarm:
		return "WARM"
	default:
		return "IDLE"
	}
}

// record folds a line onto the scale, writes it into the loop, and plays it
// right away. Tool calls answer with a chiptune arpeggio of the current chord.
func (q *Sequencer) record(line string, evt EventType) int {
	freq, idx := noteFor(line)

	q.mu.Lock()
	q.current = evt
	q.energy = math.Min(1, q.energy+energyBump[evt])
	if evt == EvtError {
		q.dropNext = true
	}
	q.notes[q.head] = freq
	q.noteIdx[q.head] = idx
	q.square[q.head] = evt == EvtError
	q.writtenAt[q.head] = q.ticks
	q.head = (q.head + 1) % stepCount
	c := q.chord()
	q.mu.Unlock()

	if evt == EvtToolCall {
		q.synth.triggerArp(c)
	} else {
		q.synth.triggerLead(freq, evt == EvtError, 1)
	}
	return idx
}

// settle tells the arrangement the agent has stopped: no more thinking bells or
// tool-call hats; the energy then drains on its own.
func (q *Sequencer) settle() {
	q.mu.Lock()
	q.current = EvtOutput
	q.mu.Unlock()
}

// step plays one sixteenth.
func (q *Sequencer) step(step int) beatInfo {
	q.mu.Lock()
	q.ticks++
	q.energy *= energyDecay
	slam := false
	if step == 0 {
		q.bar++
		slam = q.dropping
		q.dropping = q.dropNext
		q.dropNext = false
	}
	bar, e, drop, rolling := q.bar, q.energy, q.dropping, q.dropNext
	c := q.chord()
	evt := q.current
	note := q.notes[step]
	idx := q.noteIdx[step]
	square := q.square[step]
	age := q.ticks - q.writtenAt[step]
	if note > 0 && age > noteLife {
		q.notes[step], q.noteIdx[step] = 0, -1
		note, idx = 0, -1
	}
	info := beatInfo{note: -1, chord: c.name, section: q.section(), energy: e}
	q.mu.Unlock()

	if step == 0 {
		q.synth.setChord(c)
	}
	// The filter breathes over sweepBars; energy decides how far it opens.
	pos := float64((bar%sweepBars)*stepCount+step) / float64(sweepBars*stepCount)
	lfo := 0.5 - 0.5*math.Cos(2*math.Pi*pos)
	q.synth.setSweep(lfo * (0.3 + 0.7*e))

	if drop {
		return info // pad only; the sidechain lets go and it swells
	}

	work, warm, peak := e >= levelWork, e >= levelWarm, e >= levelPeak

	if step%4 == 0 {
		amp := 0.6 + 0.4*math.Min(1, e/levelWork)
		if slam && step == 0 {
			amp = 1
		}
		q.synth.triggerKick(amp)
	}
	if slam && step == 0 {
		q.synth.triggerHat(1, true)
	}

	if work && (step == 4 || step == 12) {
		q.synth.triggerSnare(1)
	}
	if rolling && step >= 13 {
		q.synth.triggerSnare(0.3 + 0.15*float64(step-13)) // roll into the drop
	}

	switch {
	case step%4 == 2 && warm:
		q.synth.triggerHat(0.9, true)
	case step == 14 && peak:
		q.synth.triggerHat(0.7, true)
	case step%2 == 1:
		q.synth.triggerHat(0.3+0.25*math.Min(1, e/levelWork), false)
	case work:
		q.synth.triggerHat(0.4, false)
	}

	// Octave-bouncing eighths on the chord root.
	if warm && step%2 == 0 {
		f := c.root
		if step%4 == 2 {
			f *= 2
		}
		q.synth.triggerBass(f)
	}

	if peak && step%8 == 0 {
		q.synth.triggerArp(c)
	}
	if evt == EvtThinking && step%2 == 0 {
		// A slow up-and-down through the chord while the agent thinks.
		q.synth.triggerBell(c.pad[[]int{0, 1, 2, 1}[step/2%4]] * 2)
	}

	if note > 0 {
		// Notes play at full strength for a third of their life, then fade.
		amp := math.Min(1, 1.5-1.5*float64(age)/noteLife)
		if amp > 0.02 {
			q.synth.triggerLead(note, square, amp)
			info.note = idx
		}
	}
	return info
}
