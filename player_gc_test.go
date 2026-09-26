package main

import (
	"math/rand"
	"runtime"
	"testing"
	"time"

	"github.com/ebitengine/oto/v3"
)

type silentSource struct{}

func (silentSource) Read(b []byte) (int, error) {
	for i := range b {
		b[i] = 0
	}
	return len(b), nil
}

// TestPlayerStaysAlive forces the GC while a kept player is playing. Before the
// fix (ctx.NewPlayer(synth).Play() with the player discarded) the runtime
// cleanup closes the player and IsPlaying flips to false.
func TestPlayerStaysAlive(t *testing.T) {
	ctx, ready, err := oto.NewContext(&oto.NewContextOptions{
		SampleRate:   44100,
		ChannelCount: 1,
		Format:       oto.FormatSignedInt16LE,
	})
	if err != nil {
		t.Skipf("no audio device: %v", err)
	}
	<-ready

	player := ctx.NewPlayer(silentSource{})
	player.Play()
	defer player.Close()

	for i := 0; i < 40; i++ {
		runtime.GC()
		// Allocate garbage so the collector actually has work to do.
		_ = make([]byte, rand.Intn(1<<16))
		time.Sleep(20 * time.Millisecond)
	}

	if !player.IsPlaying() {
		t.Fatal("player stopped playing after GC; the player reference was collected")
	}
}
