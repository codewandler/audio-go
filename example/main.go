package main

import (
	"errors"
	"github.com/codewandler/audio-go"
	"github.com/gordonklaus/portaudio"
	"io"
	"log"
	"time"
)

func main() {
	portaudio.Initialize()
	defer portaudio.Terminate()

	dev, err := audio.NewDevice(8000, 1)
	if err != nil {
		panic(err)
	}

	go func() {
		<-time.After(10 * time.Second)
		_ = dev.Close()
	}()

	_, err = io.Copy(dev, dev)
	if err != nil {
		if errors.Is(err, io.ErrClosedPipe) {
			log.Println("closed")
			return
		}

		log.Fatalf("error: %s", err.Error())
	}
}
