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

func (o *osc) triangle(freq float64) float64 {
	s := 4*math.Abs(o.phase-0.5) - 1
	o.advance(freq / sampleRate)
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

// lead plays the agent's notes through a plucked low-pass. The patch picks its
// wave: detuned saws (the synthwave lead), a square, or a glassy FM bell.
// Errors swap it for a hollow square.
type lead struct {
	a, b     osc
	filt     svf
	freq     float64
	amp, env float64
	detune   float64 // how far apart the two saws sit; set per phrase
	square   bool
	tone     leadTone
}

func (v *lead) next(sweep float64) float64 {
	if v.amp < 1e-4 {
		return 0
	}
	var s float64
	switch {
	case v.square:
		width := 0.5
		if v.tone.wave == waveSquare {
			width = 0.15 // still sounds wrong next to the patch's own square
		}
		s = v.a.pulse(v.freq, width) * 0.7
	case v.tone.wave == waveSquare:
		s = v.a.pulse(v.freq, 0.5) * 0.42
	case v.tone.wave == waveBell:
		mod := v.b.sine(v.freq * 2)
		s = math.Sin(2*math.Pi*v.a.phase+(1.2+3*v.env)*mod) * 0.55
		v.a.advance(v.freq / sampleRate)
	default:
		s = (v.a.saw(v.freq*(1+v.detune)) + v.b.saw(v.freq*(1-v.detune))) * 0.5
	}
	out, _ := v.filt.run(s, v.tone.cutoff+sweep*1800+v.env*v.tone.env, 0.8)
	out *= v.amp
	v.amp *= leadDecay
	v.env *= leadEnvDecay
	return out
}

// arp is the chiptune bit: a 25% pulse flicking through chord tones every
// few tens of milliseconds, like a console faking a chord on one channel.
type arp struct {
	o     osc
	notes []float64
	tick  int // samples per note
	idx   int
	left  int
	amp   float64
}

func (v *arp) next() float64 {
	if v.amp < 1e-4 || len(v.notes) == 0 {
		return 0
	}
	if v.left <= 0 {
		v.left = v.tick
		v.idx = (v.idx + 1) % len(v.notes)
	}
	v.left--
	s := v.o.pulse(v.notes[v.idx], 0.25) * v.amp
	v.amp *= arpDecay
	return s
}

// bass is a plucked oscillator with a sine under it, the filter opening with
// the sweep. The patch picks saw, square or reese; a low damp makes it acid.
type bass struct {
	o, o2, sub      osc
	filt            svf
	freq            float64
	amp, env        float64
	tone            bassTone
	decay, envDecay float64 // per-sample multipliers from the tone
}

func (v *bass) next(sweep float64) float64 {
	if v.amp < 1e-4 {
		return 0
	}
	var s float64
	switch v.tone.wave {
	case waveSquare:
		s = v.o.pulse(v.freq, 0.5) * 0.28
	case waveReese:
		s = (v.o.saw(v.freq*1.008) + v.o2.saw(v.freq*0.992)) * 0.4
	default:
		s = v.o.saw(v.freq) * 0.7
	}
	s += v.sub.sine(v.freq) * v.tone.sub
	out, _ := v.filt.run(s, v.tone.cutoff+sweep*500+v.env*v.tone.env, v.tone.damp)
	out *= v.amp
	v.amp *= v.decay
	v.env *= v.envDecay
	return out
}

// pad holds the chord: six detuned oscillators, dark and slow, swelling in on
// each change. The patch picks saw strings, PWM strings or a soft triangle choir.
type pad struct {
	osc   [6]osc
	lfo   osc // sweeps the pulse width for PWM
	filt  svf
	notes [3]float64
	amp   float64
	tone  padTone
}

func (v *pad) next(sweep float64) float64 {
	if v.notes[0] == 0 {
		return 0
	}
	width := 0.5 + 0.2*v.lfo.sine(0.3)
	up, down := 1+v.tone.detune, 1-v.tone.detune
	var s float64
	for i, n := range v.notes {
		a, b := &v.osc[2*i], &v.osc[2*i+1]
		switch v.tone.wave {
		case wavePWM:
			s += (a.pulse(n*up, width) + b.pulse(n*down, 1-width)) * 0.8
		case waveTriangle:
			s += (a.triangle(n*up) + b.triangle(n*down)) * 1.3
		default:
			s += a.saw(n*up) + b.saw(n*down)
		}
	}
	v.amp += (1 - v.amp) * padAttack
	out, _ := v.filt.run(s/6, v.tone.cutoff+sweep*1500, 1.1)
	return out * v.amp
}

// chip is the counter-melody: a short pulse-wave pluck, the 8-bit lead of an
// old console, with its pulse width chosen per phrase.
type chip struct {
	o               osc
	freq, amp, duty float64
}

func (v *chip) next() float64 {
	if v.amp < 1e-4 {
		return 0
	}
	s := v.o.pulse(v.freq, v.duty) * v.amp
	v.amp *= chipDecay
	return s
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

// kick is a sine with a fast pitch drop and an optional click on the attack.
type kick struct {
	o                osc
	amp, bend, click float64
	tone             kickTone
	decay, bendDecay float64 // per-sample multipliers from the tone
}

func (v *kick) next() float64 {
	if v.amp < 1e-4 {
		return 0
	}
	s := v.o.sine(v.tone.base+v.tone.depth*v.bend) * v.amp
	if v.click > 1e-4 {
		s += (rand.Float64()*2 - 1) * v.click
		v.click *= clickDecay
	}
	v.amp *= v.decay
	v.bend *= v.bendDecay
	return s
}

// noise covers the snare and both hats: filtered noise plus an optional tone.
// Claps retrigger the noise a few times; a gate cuts the tail short, like an
// 80s gated reverb snare.
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

	claps, clapsLeft, clapWait int
	clapAmp                    float64
	gate, age                  int // samples; gate 0 = no gate
}

func (v *noise) next() float64 {
	if v.clapsLeft > 0 {
		if v.clapWait--; v.clapWait <= 0 {
			v.amp = v.clapAmp
			v.clapsLeft--
			v.clapWait = clapGap
		}
	}
	if v.amp < 1e-4 && v.bodyAmp < 1e-4 {
		return 0
	}
	if v.age++; v.gate > 0 && v.age > v.gate {
		v.amp *= gateCut
		v.bodyAmp *= gateCut
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
	leadDecay    = decayFor(0.4)
	leadEnvDecay = decayFor(0.12)
	arpDecay     = decayFor(0.7)
	bellDecay    = decayFor(0.6)
	chipDecay    = decayFor(0.22)
	padAttack    = 1 / (0.6 * sampleRate)
	clickDecay   = decayFor(0.004)
	gateCut      = decayFor(0.015)
	duckRelease  = decayFor(0.4)
)

const clapGap = sampleRate / 100 // 10 ms between clap bursts

// --- SYNTH ---

type Synth struct {
	lead  lead
	arp   arp
	chip  chip
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
	muted      bool    // keeps playing silently, so the visuals still follow the music
	feedback   float64 // echo feedback, set per phrase
	patch      patch   // the sound set picked at startup

	mu sync.Mutex
}

// newSynth starts with a random patch (or CLANKER_PATCH).
func newSynth(rate float64) *Synth {
	return newSynthPatch(rate, pickPatch(rand.New(rand.NewSource(time.Now().UnixNano()))))
}

func newSynthPatch(rate float64, p patch) *Synth {
	s := &Synth{delay: make([]float64, int(rate*0.375)), feedback: 0.4, patch: p} // dotted eighth at 120 BPM
	s.kick.tone = p.kick
	s.kick.decay, s.kick.bendDecay = decayFor(p.kick.decay), decayFor(p.kick.bendDecay)
	s.snare = noise{
		cutoff: p.snare.cutoff, decay: decayFor(p.snare.decay), passBright: true,
		bodyFreq: p.snare.body, bodyDecay: decayFor(max(p.snare.bodyDecay, 0.001)),
		claps: max(p.snare.claps, 1), gate: int(p.snare.gate * sampleRate),
	}
	s.hat = noise{cutoff: p.hat.cutoff, decay: decayFor(p.hat.closed), passBright: true}
	s.ohat = noise{cutoff: p.hat.cutoff * 0.85, decay: decayFor(p.hat.open), passBright: true}
	s.bass.tone = p.bass
	s.bass.decay, s.bass.envDecay = decayFor(p.bass.decay), decayFor(p.bass.envDecay)
	s.pad.tone = p.pad
	s.lead.tone = p.lead
	s.lead.detune = 0.006
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
		chip := s.chip.next() * 0.11
		snare := s.snare.next() * 0.50

		dry := s.kick.next()*0.95 + snare +
			s.hat.next()*0.16 + s.ohat.next()*0.13 +
			(s.bass.next(s.sweep)*0.65+s.pad.next(s.sweep)*0.18)*pump +
			lead + arp + chip + bell

		// Only the melodic parts and the snare go to the echo; kick and bass
		// stay dry so the low end doesn't turn to mud.
		delayed := s.delay[s.delayIndex]
		s.delay[s.delayIndex] = lead + arp + chip + bell + snare*0.35 + delayed*s.feedback
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
	s.kick.click = s.kick.tone.click * amp
	s.duck = amp
	s.mu.Unlock()
}

func (s *Synth) triggerSnare(amp float64) {
	s.mu.Lock()
	s.snare.amp, s.snare.bodyAmp = amp, amp*s.patch.snare.bodyMix
	s.snare.clapAmp, s.snare.clapsLeft, s.snare.clapWait = amp, s.snare.claps-1, clapGap
	s.snare.age = 0
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

// triggerArp runs through the chord's tones in the given order, one every tick
// samples. Degree 3 is the root an octave above the others.
func (s *Synth) triggerArp(c chord, shape []int, tick int) {
	notes := make([]float64, len(shape))
	for i, d := range shape {
		notes[i] = motifFreq(c, d)
	}
	s.mu.Lock()
	s.arp.notes, s.arp.tick = notes, tick
	s.arp.idx, s.arp.left, s.arp.amp = 0, tick, 1
	s.mu.Unlock()
}

func (s *Synth) triggerChip(freq, duty, amp float64) {
	s.mu.Lock()
	s.chip.freq, s.chip.duty, s.chip.amp = freq, duty, amp
	s.mu.Unlock()
}

// setTone applies a phrase's lead detune and echo feedback.
func (s *Synth) setTone(detune, feedback float64) {
	s.mu.Lock()
	s.lead.detune, s.feedback = detune, feedback
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
	rng       *rand.Rand // picks phrases; only used with mu held
	ph        phrase     // what the band plays for the current phrase

	// Phrases follow the agent: a new one only at a phrase boundary after
	// input, on the slam after an error's drop, or after a long idle.
	phraseStart int  // bar the current phrase began on
	phraseTick  int  // step count when it began
	activity    bool // input arrived during this phrase
}

func newSequencer(synth *Synth) *Sequencer {
	return newSequencerRand(synth, rand.New(rand.NewSource(time.Now().UnixNano())))
}

// newSequencerRand takes the random source, so tests can pin an arrangement.
func newSequencerRand(synth *Synth, rng *rand.Rand) *Sequencer {
	q := &Sequencer{synth: synth, bar: -1, rng: rng}
	q.ph = newPhrase(rng)
	for i := range q.noteIdx {
		q.noteIdx[i] = -1
	}
	return q
}

// idleDrift is how long a phrase loops with no input before the band moves on
// anyway: 32 bars, about a minute.
const idleDrift = 32 * stepCount

// phrasePos is the bar's position in the current phrase. Call with q.mu held.
func (q *Sequencer) phrasePos() int {
	return (max(q.bar, 0) - q.phraseStart) % phraseBars
}

// chord is the chord of the current bar. Call with q.mu held.
func (q *Sequencer) chord() chord {
	return q.ph.prog[q.phrasePos()%len(q.ph.prog)]
}

// nextPhrase starts a new phrase on the current bar. Call with q.mu held.
func (q *Sequencer) nextPhrase() {
	q.ph = newPhrase(q.rng)
	q.phraseStart = max(q.bar, 0)
	q.phraseTick = q.ticks
	q.activity = false
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
	q.activity = true
	q.energy = math.Min(1, q.energy+energyBump[evt])
	if evt == EvtError {
		q.dropNext = true
	}
	q.notes[q.head] = freq
	q.noteIdx[q.head] = idx
	q.square[q.head] = evt == EvtError
	q.writtenAt[q.head] = q.ticks
	q.head = (q.head + 1) % stepCount
	c, ph := q.chord(), q.ph
	q.mu.Unlock()

	if evt == EvtToolCall {
		q.synth.triggerArp(c, ph.arpShape, ph.arpTick)
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
	slam, fresh := false, false
	if step == 0 {
		q.bar++
		slam = q.dropping
		q.dropping = q.dropNext
		q.dropNext = false
		switch {
		case slam:
			q.nextPhrase() // the beat comes back on a fresh scene
			fresh = true
		case q.bar > 0 && q.phrasePos() == 0:
			if q.activity || q.ticks-q.phraseTick >= idleDrift {
				q.nextPhrase()
				fresh = true
			}
			// Otherwise nothing happened: loop the same phrase.
		case q.bar == 0:
			fresh = true // apply the starting phrase's tone
		}
	}
	bar, e, drop, rolling := q.bar, q.energy, q.dropping, q.dropNext
	pos := q.phrasePos()
	c, ph := q.chord(), q.ph
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
		if fresh {
			q.synth.setTone(ph.detune, ph.feedback)
		}
	}
	// The filter breathes over sweepBars; energy decides how far it opens.
	sweepPos := float64((bar%sweepBars)*stepCount+step) / float64(sweepBars*stepCount)
	lfo := 0.5 - 0.5*math.Cos(2*math.Pi*sweepPos)
	q.synth.setSweep(lfo * (0.3 + 0.7*e))

	if drop {
		return info // pad only; the sidechain lets go and it swells
	}

	work, warm, peak := e >= levelWork, e >= levelWarm, e >= levelPeak
	drive := math.Min(1, e/levelWork)

	// The phrase's last bar ends on a fill, unless an error is rolling into a drop.
	fill := pos == phraseBars-1 && step >= 12 && !rolling
	kickDrop := fill && warm && ph.fill == 1
	snareRun := fill && work && ph.fill == 0

	if k := ph.groove.kick[step]; k > 0 && !kickDrop {
		amp := (0.6 + 0.4*drive) * k
		if slam && step == 0 {
			amp = 1
		}
		q.synth.triggerKick(amp)
	}
	if slam && step == 0 {
		q.synth.triggerHat(1, true)
	}

	if sn := ph.groove.snare[step]; work && sn > 0 {
		q.synth.triggerSnare(sn)
	}
	if snareRun {
		q.synth.triggerSnare(0.35 + 0.15*float64(step-12))
	}
	if rolling && step >= 13 {
		q.synth.triggerSnare(0.3 + 0.15*float64(step-13)) // roll into the drop
	}

	switch {
	case step%4 == 2 && warm:
		q.synth.triggerHat(0.9, true)
	case step == 14 && (peak || kickDrop):
		q.synth.triggerHat(0.7, true)
	case ph.groove.chats[step] > 0:
		q.synth.triggerHat(ph.groove.chats[step]*(0.3+0.25*drive), false)
	case work:
		q.synth.triggerHat(0.4, false)
	}

	if warm && !kickDrop {
		if f := bassFreq(ph.bass, step, c); f > 0 {
			q.synth.triggerBass(f)
		}
	}

	// The chiptune counter-melody: the hook, then its answer in the second half.
	if warm && ph.motifOn {
		motif := ph.motifA
		if pos >= phraseBars/2 {
			motif = ph.motifB
		}
		if d := motif[step]; d >= 0 {
			q.synth.triggerChip(motifFreq(c, d), ph.duty, 0.6+0.4*math.Min(1, e/levelPeak))
		}
	}

	if peak && step%8 == 0 {
		q.synth.triggerArp(c, ph.arpShape, ph.arpTick)
	}
	if evt == EvtThinking && step%2 == 0 {
		// A slow up-and-down through the chord while the agent thinks.
		q.synth.triggerBell(c.pad[[]int{0, 1, 2, 1}[step/2%4]] * 2)
	}

	if note > 0 && ph.mask[step] == 'x' {
		// Notes play at full strength for a third of their life, then fade.
		amp := math.Min(1, 1.5-1.5*float64(age)/noteLife)
		if amp > 0.02 {
			q.synth.triggerLead(note, square, amp)
			info.note = idx
		}
	}
	return info
}
