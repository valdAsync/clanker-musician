# clanker-musician

A night-shift security monitor for coding agents: a psychedelic, 80s-horror
camera feed and a synthwave / minimal-techno soundtrack that builds and drops
with what the agent is doing.

![clanker-musician reacting to a pi session](https://raw.githubusercontent.com/valdAsync/clanker-musician/master/assets/demo.gif)

## In pi (side column)

```bash
pi install npm:@valdasync/pi-clanker-musician
pi --tui-mode fullscreen
```

Prebuilt engines ship for macOS and Linux (x64 and arm64); Linux needs ALSA
or PulseAudio. Music plays on the machine pi runs on. From a checkout (needs
Go), run `pi --tui-mode fullscreen --extension ./pi/clanker` instead.

Then in the chat:

- `/clanker-musician`: turn the column and music on or off
- `/clanker-musician mute`, `/clanker-musician unmute`: silence the music and
  keep the picture

POWER in the column is the context window left (100% minus the usage in
pi's footer), so it drains as the session grows and recharges after a
compaction. Cameras cut when the agent plays a new lead note, stay up for at
least 4 s, and drift on their own every 15–25 s when nothing happens.

In fullscreen mode the conversation rewraps beside the column. In regular
mode the column is an overlay on the right edge. It hides below 100 columns.
Only one session plays at a time.

Each start picks a random sound set (patch), shown as the tape's label:
NIGHT SHIFT, VHS, WAREHOUSE, ACID, NEON ARCADE or NEON RAIN. Set
`CLANKER_PATCH=acid` (any patch name, case and spaces ignored) to choose one.
The arrangement moves to a new phrase only when the agent has done something,
and holds while it's idle.

The extension builds `./clanker-musician` with Go on first use, and again
whenever the Go sources are newer. Set `CLANKER_MUSICIAN_BIN` to use a
different binary.

## Standalone

```bash
go build -o clanker-musician .
some-agent | ./clanker-musician
```

Each line on stdin is an event. Lines containing `tool`/`edit`, `think`, or
`error`/`fail` get their own sounds and visuals.

## Engine protocol

`clanker-musician engine` is what the pi extension runs. It exchanges JSON
lines on stdin and stdout; see `engine.go`.

## Tests

```bash
go test -race ./...
node --test --experimental-strip-types pi/clanker/*.test.ts
```
