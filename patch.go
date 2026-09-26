package main

import (
	"math/rand"
	"os"
	"strings"
)

// A patch is a complete sound set (kick, snare, hats, bass, pad and lead)
// designed to work together. One is picked at random when the synth starts,
// with small random tweaks, so every session sounds a little different. All of
// them stay synthwave / minimal techno; chiptune is an accent, never the base.
// Set CLANKER_PATCH to a patch name to pick one.

type wave int

const (
	waveSaw wave = iota
	waveSquare
	waveTriangle
	waveReese // two saws detuned against each other
	wavePWM   // a pulse whose width slowly sweeps
	waveBell  // two-operator FM, glassy
)

type kickTone struct {
	base, depth      float64 // Hz: settles at base, starts at base+depth
	decay, bendDecay float64 // seconds
	click            float64 // level of the attack click
}

type snareTone struct {
	cutoff, decay            float64 // noise high-pass (Hz) and length (s)
	body, bodyDecay, bodyMix float64 // tonal body: Hz, seconds, level
	claps                    int     // noise bursts: 1 is a snare, 3 a clap
	gate                     float64 // seconds before a hard cut (80s gated snare); 0 = none
}

type hatTone struct {
	cutoff, closed, open float64 // Hz, and decay seconds
}

type bassTone struct {
	wave                   wave
	cutoff, env, damp, sub float64 // filter base and envelope (Hz), 1/Q, sine sub level
	decay, envDecay        float64 // seconds
}

type padTone struct {
	wave           wave
	cutoff, detune float64
}

type leadTone struct {
	wave        wave
	cutoff, env float64 // filter base and pluck envelope (Hz)
}

type patch struct {
	name  string
	kick  kickTone
	snare snareTone
	hat   hatTone
	bass  bassTone
	pad   padTone
	lead  leadTone
}

var patches = []patch{
	{
		name:  "NIGHT SHIFT", // the original sound
		kick:  kickTone{base: 45, depth: 120, decay: 0.32, bendDecay: 0.05},
		snare: snareTone{cutoff: 1500, decay: 0.25, body: 185, bodyDecay: 0.07, bodyMix: 0.6, claps: 1},
		hat:   hatTone{cutoff: 6000, closed: 0.05, open: 0.3},
		bass:  bassTone{wave: waveSaw, cutoff: 220, env: 900, damp: 0.7, sub: 0.6, decay: 0.28, envDecay: 0.08},
		pad:   padTone{wave: waveSaw, cutoff: 450, detune: 0.004},
		lead:  leadTone{wave: waveSaw, cutoff: 500, env: 2600},
	},
	{
		name:  "VHS", // outrun: deep kick, gated 80s snare, lush strings
		kick:  kickTone{base: 40, depth: 90, decay: 0.6, bendDecay: 0.07},
		snare: snareTone{cutoff: 1200, decay: 0.45, body: 180, bodyDecay: 0.1, bodyMix: 0.7, claps: 1, gate: 0.22},
		hat:   hatTone{cutoff: 6000, closed: 0.04, open: 0.35},
		bass:  bassTone{wave: waveSaw, cutoff: 260, env: 1100, damp: 0.6, sub: 0.7, decay: 0.3, envDecay: 0.1},
		pad:   padTone{wave: waveSaw, cutoff: 650, detune: 0.007},
		lead:  leadTone{wave: waveSaw, cutoff: 700, env: 2800},
	},
	{
		name:  "WAREHOUSE", // minimal techno: punchy kick, clap, reese, dark pad
		kick:  kickTone{base: 50, depth: 150, decay: 0.22, bendDecay: 0.03, click: 0.35},
		snare: snareTone{cutoff: 1100, decay: 0.18, claps: 3},
		hat:   hatTone{cutoff: 6000, closed: 0.02, open: 0.18},
		bass:  bassTone{wave: waveReese, cutoff: 180, env: 700, damp: 0.8, sub: 0.5, decay: 0.26, envDecay: 0.1},
		pad:   padTone{wave: wavePWM, cutoff: 380, detune: 0.003},
		lead:  leadTone{wave: waveSquare, cutoff: 450, env: 1800},
	},
	{
		name:  "ACID", // cyberpunk techno: a squelchy resonant bass
		kick:  kickTone{base: 48, depth: 140, decay: 0.26, bendDecay: 0.035, click: 0.25},
		snare: snareTone{cutoff: 1300, decay: 0.16, claps: 3},
		hat:   hatTone{cutoff: 6000, closed: 0.04, open: 0.25},
		bass:  bassTone{wave: waveSaw, cutoff: 140, env: 2200, damp: 0.22, sub: 0.3, decay: 0.22, envDecay: 0.12},
		pad:   padTone{wave: waveSaw, cutoff: 420, detune: 0.005},
		lead:  leadTone{wave: waveSaw, cutoff: 600, env: 2400},
	},
	{
		name:  "NEON ARCADE", // synthwave with a chiptune lead
		kick:  kickTone{base: 48, depth: 130, decay: 0.25, bendDecay: 0.04, click: 0.15},
		snare: snareTone{cutoff: 2500, decay: 0.07, body: 420, bodyDecay: 0.035, bodyMix: 1.2, claps: 1}, // rimshot
		hat:   hatTone{cutoff: 6000, closed: 0.025, open: 0.2},
		bass:  bassTone{wave: waveSquare, cutoff: 240, env: 800, damp: 0.8, sub: 0.3, decay: 0.24, envDecay: 0.07},
		pad:   padTone{wave: wavePWM, cutoff: 550, detune: 0.004},
		lead:  leadTone{wave: waveSquare, cutoff: 900, env: 2000},
	},
	{
		name:  "NEON RAIN", // dark cyberpunk: long kick, shaker, soft choir, glassy lead
		kick:  kickTone{base: 42, depth: 80, decay: 0.55, bendDecay: 0.08},
		snare: snareTone{cutoff: 1400, decay: 0.3, body: 190, bodyDecay: 0.08, bodyMix: 0.5, claps: 1, gate: 0.2},
		hat:   hatTone{cutoff: 3500, closed: 0.07, open: 0.3}, // shaker
		bass:  bassTone{wave: waveReese, cutoff: 200, env: 600, damp: 0.7, sub: 0.45, decay: 0.3, envDecay: 0.12},
		pad:   padTone{wave: waveTriangle, cutoff: 900, detune: 0.004},
		lead:  leadTone{wave: waveBell, cutoff: 3000},
	},
}

// pickPatch honours CLANKER_PATCH, otherwise picks at random, then nudges the
// decays, filters and detune by up to ±10%.
func pickPatch(rng *rand.Rand) patch {
	p := patches[rng.Intn(len(patches))]
	if want := normPatchName(os.Getenv("CLANKER_PATCH")); want != "" {
		for _, candidate := range patches {
			if normPatchName(candidate.name) == want {
				p = candidate
			}
		}
	}
	nudge := func(v float64) float64 { return v * (0.9 + 0.2*rng.Float64()) }
	p.kick.decay = nudge(p.kick.decay)
	p.snare.decay = nudge(p.snare.decay)
	p.bass.cutoff = nudge(p.bass.cutoff)
	p.bass.decay = nudge(p.bass.decay)
	p.pad.cutoff = nudge(p.pad.cutoff)
	p.pad.detune = nudge(p.pad.detune)
	p.lead.cutoff = nudge(p.lead.cutoff)
	return p
}

func normPatchName(s string) string {
	return strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(s), " ", ""))
}
