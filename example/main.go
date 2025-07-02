package main

import (
	"github.com/codewandler/audio-go"
	"github.com/gordonklaus/portaudio"
	"io"
	"time"
)

func main() {
	portaudio.Initialize()
	defer portaudio.Terminate()

	a, err := audio.NewAudioIO(audio.Config{
		PlayLatency:    20 * time.Millisecond,
		CaptureLatency: 20 * time.Millisecond,
	})
	if err != nil {
		panic(err)
	}

	n, err := io.Copy(a, a)
	if err != nil {
		panic(err)
	}
	println(n)
}
