# Tuning notes

Open questions to decide after listening. Nothing here is implemented yet.

## Energy saturates at PEAK on fast streams

The arrangement follows one energy value (0–1) in `synth.go`:

| Knob | Where | Now |
|---|---|---|
| Bump per line | `energyBump` | output 0.08, thinking 0.08, tool 0.12, error 0.05 |
| Decay per step (125 ms) | `energyDecay` | 0.992, halves in ~11 s of silence |
| Section thresholds | `levelWarm` / `levelWork` / `levelPeak` | 0.15 / 0.4 / 0.7 |

Steady state is roughly `energy ≈ bump-per-step / (1 - energyDecay)`. With tool
calls alone, that settles at PEAK for one call every ≲2.5 s, WORK for one every
2.5–4.5 s, and WARM below that.

Simulated session (tool call every ~0.6 s during the burst), one entry per bar:

```
IDLE ×5 → thinking: WARM → tool burst: WORK → PEAK ×13 → error: DROP → PEAK
→ silence: WORK ×4 → WARM ×8 → IDLE
```

- **Claude Code hooks** (a tool call every few seconds) should sit mostly in
  WORK and reach PEAK only during dense bursts. Probably fine as is.
- **Fast piped text** (many lines per second) stays at PEAK for the whole
  session, so the build-up is lost.

Options if PEAK comes too easily:

1. Lower the bumps (for example halve them) so WORK is the normal busy state.
2. Scale each bump by `(1 - energy)` so the last stretch to PEAK is harder to
   reach.
3. Measure event *rate* over a window instead of summing bumps. This is the
   most robust, but it's more code.

Also check: the fall from PEAK back to IDLE takes about 14 bars (~28 s). If
that feels too slow, lower `energyDecay` to about 0.985.

## Other things to judge by ear

- **Bass may be thin on laptop speakers.** The roots sit at A1–C2 (44–65 Hz).
  Options: move `progression` roots up an octave, or raise the bass
  filter/gain in `bass.next` and `Synth.Read`.
- **The lead can get busy.** Once all 16 loop slots are filled, a note plays on
  every sixteenth. Options: shorten `noteLife`, or only play loop notes on
  eighths when energy is low.
- **Mix balance.** Measured RMS: kick 0.145, snare 0.046, bass 0.043, lead 0.037,
  pad 0.033. Gains live in `Synth.Read`. `TestSynthRendersCleanLoop` guards
  that the kick keeps punching through.

## Listening

- `afplay /tmp/clanker-session.wav`: the 88 s simulated session above
  (regenerated only by hand).
- `CLANKER_WAV=/tmp/clanker-loop.wav go test -run TestSynthRendersCleanLoop`
  renders an 8-bar loop from the current code.

## Parked ideas

- **Claude Code plugin:** hooks (`UserPromptSubmit`, `PreToolUse`,
  `PostToolUse`, `Stop`, `SessionStart`) send events to a
  `clanker-musician listen` mode over a Unix socket, and tmux auto-splits a
  pane.
- **Visualizer moods:** the agent's state biases the random camera pick
  (about 70% from the matching mood), and errors cut straight to NO SIGNAL or
  glitch. Worth doing once real hook events drive it.
