# clanker-musician

A night-shift security monitor for coding agents: a psychedelic, 80s-horror
camera feed and a synthwave / minimal-techno soundtrack that builds and drops
with what the agent is doing.

## In pi (side column)

```
pi --tui-mode fullscreen --extension ./pi/clanker
```

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

The extension builds `./clanker-musician` with Go on first use, and again
whenever the Go sources are newer. Set `CLANKER_MUSICIAN_BIN` to use a
different binary.

## Standalone

```
go build -o clanker-musician .
some-agent | ./clanker-musician
```

Each line on stdin is an event. Lines containing `tool`/`edit`, `think`, or
`error`/`fail` get their own sounds and visuals.

## Engine protocol

`clanker-musician engine` is what the pi extension runs. It exchanges JSON
lines on stdin and stdout; see `engine.go`.

## Tests

```
go test -race ./...
node --test --experimental-strip-types pi/clanker/events.test.ts
```

Open tuning questions are in `TUNING.md`.
