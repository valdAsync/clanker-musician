package main

import "math/rand"

// Every phraseBars bars the band picks a new combination: chord progression,
// drum groove, bass line, which loop slots the lead plays, a chiptune
// counter-melody, arp shape and lead tone. Changes land on phrase boundaries,
// so the music varies without ever lurching mid-bar.
const phraseBars = 8

var (
	chAm = chord{"Am", 55.00, [3]float64{220.00, 261.63, 329.63}}
	chF  = chord{"F", 43.65, [3]float64{220.00, 261.63, 349.23}}
	chC  = chord{"C", 65.41, [3]float64{196.00, 261.63, 329.63}}
	chG  = chord{"G", 49.00, [3]float64{196.00, 246.94, 293.66}}
	chDm = chord{"Dm", 73.42, [3]float64{220.00, 293.66, 349.23}}
	chEm = chord{"Em", 41.20, [3]float64{196.00, 246.94, 329.63}}
)

// progressions all stay in A minor, so the pentatonic lead fits every chord.
// The first is the synthwave staple i–VI–III–VII.
var progressions = [][4]chord{
	{chAm, chF, chC, chG},
	{chAm, chG, chF, chG},
	{chF, chG, chAm, chAm},
	{chAm, chDm, chF, chEm},
	{chAm, chF, chG, chEm},
	{chDm, chF, chAm, chG},
}

// pattern turns a 16-step string into levels: 'x' full, 'o' ghost, '.' rest.
func pattern(s string) (p [stepCount]float64) {
	for i := 0; i < stepCount && i < len(s); i++ {
		switch s[i] {
		case 'x':
			p[i] = 1
		case 'o':
			p[i] = 0.4
		}
	}
	return p
}

// groove is one bar of drums. Every groove kicks on the downbeat, so drops
// always slam back in on beat one.
type groove struct {
	name               string
	kick, snare, chats [stepCount]float64
}

var grooves = []groove{
	{"four", pattern("x...x...x...x..."), pattern("....x.......x..."), pattern(".x.x.x.x.x.x.x.x")},
	{"pickup", pattern("x...x...x...x.o."), pattern("....x.......x..o"), pattern(".x.x.x.x.x.x.xox")},
	{"rolling", pattern("x...x...x...x..."), pattern("....x..o....x..."), pattern("oxoxoxoxoxoxoxox")},
	{"broken", pattern("x.....x...x....."), pattern("....x.......x..."), pattern(".x.x.x.x.x.x.x.x")},
	{"halftime", pattern("x.........x....."), pattern("........x......."), pattern("xoxoxoxoxoxoxoxo")},
}

// grooveOdds keeps four-on-the-floor the most common groove.
var grooveOdds = []int{0, 0, 0, 1, 2, 2, 3, 4}

// bassLines: '1' root, '2' octave, '5' fifth, '.' rest.
var bassLines = []struct{ name, notes string }{
	{"bounce", "1.2.1.2.1.2.1.2."},
	{"rolling", "..21..21..21..21"},
	{"arp", "1.5.2.5.1.5.2.5."},
	{"synth", "1..1..2.1..1..2."},
	{"pulse", "1.......1.....2."},
}

// leadMasks decide which of the 16 loop slots sound, so the agent's melody
// breathes instead of hammering every sixteenth.
var leadMasks = []string{
	"xxxxxxxxxxxxxxxx",
	"x.x.x.x.x.x.x.x.",
	"x..x..x.x..x..x.",
	"x.xx.x.xx.x.x.xx",
	"x.....x...x.....",
}

var arpShapes = [][]int{
	{0, 1, 2, 3},
	{3, 2, 1, 0},
	{0, 1, 2, 3, 2, 1},
	{0, 2, 1, 3},
}

// phrase is everything the band has chosen for the next phraseBars bars.
type phrase struct {
	prog   [4]chord
	groove groove
	bass   string
	mask   string
	fill   int // last-bar fill: 0 snare run, 1 kick drop

	// The chiptune counter-melody: per step, a chord-tone degree (0–2, 3 is
	// the root an octave up) or -1 for a rest. motifB answers motifA in the
	// second half of the phrase.
	motifA, motifB [stepCount]int
	motifOn        bool
	duty           float64 // pulse width of the counter-melody voice

	arpShape []int
	arpTick  int
	detune   float64
	feedback float64
}

func newPhrase(rng *rand.Rand) phrase {
	p := phrase{
		prog:     progressions[rng.Intn(len(progressions))],
		groove:   grooves[grooveOdds[rng.Intn(len(grooveOdds))]],
		bass:     bassLines[rng.Intn(len(bassLines))].notes,
		mask:     leadMasks[rng.Intn(len(leadMasks))],
		fill:     rng.Intn(2),
		motifOn:  rng.Float64() < 0.75,
		duty:     []float64{0.125, 0.25, 0.5}[rng.Intn(3)],
		arpShape: arpShapes[rng.Intn(len(arpShapes))],
		arpTick:  sampleRate / []int{16, 20, 24}[rng.Intn(3)],
		detune:   []float64{0.003, 0.006, 0.01}[rng.Intn(3)],
		feedback: []float64{0.3, 0.4, 0.5}[rng.Intn(3)],
	}
	p.motifA, p.motifB = newMotif(rng)
	return p
}

// newMotif writes a short hook: 4–7 notes, mostly on eighths, moving by step
// through the chord. The answer either mirrors the contour or shifts the
// rhythm, so the second half of the phrase replies instead of repeating.
func newMotif(rng *rand.Rand) (a, b [stepCount]int) {
	for i := range a {
		a[i], b[i] = -1, -1
	}
	hits := 4 + rng.Intn(4)
	deg := rng.Intn(3)
	for placed := 0; placed < hits; {
		s := rng.Intn(stepCount)
		if s%2 == 1 && rng.Float64() < 0.7 {
			continue // favour eighths
		}
		if a[s] != -1 {
			continue
		}
		a[s] = deg
		placed++
		deg = min(3, max(0, deg+rng.Intn(3)-1))
	}
	if rng.Intn(2) == 0 {
		for i, d := range a {
			if d >= 0 {
				b[i] = 3 - d // mirrored contour
			}
		}
	} else {
		for i, d := range a {
			b[(i+2)%stepCount] = d // same notes, pushed an eighth later
		}
	}
	return a, b
}

// motifFreq is the pitch of a motif degree over a chord, two octaves above the
// pad so it sits clear of the lead.
func motifFreq(c chord, deg int) float64 {
	if deg == 3 {
		return c.pad[0] * 4
	}
	return c.pad[deg] * 2
}

// bassFreq is the bass line character at step over a chord, or 0 for a rest.
func bassFreq(line string, step int, c chord) float64 {
	if step >= len(line) {
		return 0
	}
	switch line[step] {
	case '1':
		return c.root
	case '2':
		return c.root * 2
	case '5':
		return c.root * 1.5
	}
	return 0
}
