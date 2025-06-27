package audio

import (
	"encoding/binary"
	"errors"
	"sync"
	"time"

	"github.com/MarkKremer/microphone/v2"
	"github.com/gopxl/beep/v2"
	"github.com/gopxl/beep/v2/speaker"
)

const (
	defaultPlayLatency    = 200 * time.Millisecond // defaultPlayLatency speaker buffer = 200 ms
	defaultCaptureLatency = 200 * time.Millisecond // defaultPlayLatency capture buffer = 200 ms
	defaultSampleRate     = 48_000                 // defaultSampleRate is the default sample rate
	bytesPerSample        = 2                      // 16-bit mono PCM
	captureFrames         = 1024                   // mic pull size
)

type Config struct {
	PlaySampleRate    int
	PlayLatency       time.Duration
	CaptureSampleRate int
	CaptureLatency    time.Duration
}

// NewAudioIO returns an io.ReadWriter that speaks 16-bit MONO PCM.
// ctx / framesPerBuffer are ignored for API compatibility.
func NewAudioIO(
	config Config,
) (*AudioIO, error) {
	if config.PlayLatency == 0 {
		config.PlayLatency = defaultPlayLatency
	}
	if config.PlaySampleRate == 0 {
		config.PlaySampleRate = defaultSampleRate
	}
	if config.CaptureSampleRate == 0 {
		config.CaptureSampleRate = defaultSampleRate
	}
	if config.CaptureLatency == 0 {
		config.CaptureLatency = defaultCaptureLatency
	}

	var (
		playSR    = beep.SampleRate(config.PlaySampleRate)
		captureSR = beep.SampleRate(config.CaptureSampleRate)
	)

	// --------------- playback side ------------------------------------------
	if err := speaker.Init(playSR, playSR.N(config.PlayLatency)); err != nil {
		return nil, err
	}

	// channel feeding the one global streamer
	playCh := make(chan [2]float64, config.PlaySampleRate*5) // buffer 5s of audio
	playStreamer := newChanStreamer(playCh)
	speaker.Play(playStreamer)

	// --------------- capture side -------------------------------------------
	mic, _, err := microphone.OpenDefaultStream(captureSR, 1) // 1 = mono
	if err != nil {
		return nil, err
	}
	if err := mic.Start(); err != nil {
		return nil, err
	}

	a := &AudioIO{
		mic:          mic,
		playCh:       playCh,
		playStreamer: playStreamer,
		readBuf:      make([]byte, 0, 160),
	}
	a.readCond = sync.NewCond(&a.readMu)

	go a.captureLoop()
	return a, nil
}

// ---------------------------------------------------------------------------

type AudioIO struct {
	mic          *microphone.Streamer
	playStreamer *chanStreamer
	playCh       chan [2]float64
	readMu       sync.Mutex
	readBuf      []byte
	readCond     *sync.Cond // 🚨 new condition variable
}

// --------------------------- io.Reader --------------------------------------

func (a *AudioIO) Read(p []byte) (int, error) {
	a.readMu.Lock()
	defer a.readMu.Unlock()

	for len(a.readBuf) == 0 {
		a.readCond.Wait() // 🚨 wait for signal
	}

	n := copy(p, a.readBuf)
	a.readBuf = a.readBuf[n:]
	return n, nil
}

// --------------------------- io.Writer --------------------------------------

func (a *AudioIO) Write(b []byte) (int, error) {
	if len(b)%bytesPerSample != 0 {
		return 0, errors.New("audio: Write expects 16-bit mono PCM")
	}

	for i := 0; i < len(b); i += bytesPerSample {
		v := int16(binary.LittleEndian.Uint16(b[i:]))
		f := float64(v) / 32768.0    // range -1..1
		a.playCh <- [2]float64{f, f} // duplicate to stereo
	}
	return len(b), nil
}

// ---------------------------------------------------------------------------

func (a *AudioIO) captureLoop() {
	frames := make([][2]float64, captureFrames)

	for {
		n, ok := a.mic.Stream(frames)
		if !ok {
			return
		}

		mono := stereoSamplesToPCM16Mono(frames[:n])

		a.readMu.Lock()
		a.readBuf = append(a.readBuf, mono...)
		a.readCond.Signal()
		a.readMu.Unlock()
	}
}

// ClearOutputBuffer clears output buffer
func (a *AudioIO) ClearOutputBuffer() {
	a.playStreamer.Flush()
}

// ---------------------- conversion helpers ---------------------------------

func stereoSamplesToPCM16Mono(s [][2]float64) []byte {
	b := make([]byte, len(s)*bytesPerSample)
	for i, v := range s {
		m := int16(clamp(v[0]) * 32767) // take left channel
		binary.LittleEndian.PutUint16(b[i*2:], uint16(m))
	}
	return b
}

func clamp(f float64) float64 {
	switch {
	case f > 1:
		return 1
	case f < -1:
		return -1
	default:
		return f
	}
}

// ------------------------- chanStreamer ------------------------------------

// chanStreamer pulls samples from a channel. When the channel is empty it
// plays silence, avoiding glitches while waiting for more data.
type chanStreamer struct {
	ch <-chan [2]float64
}

func newChanStreamer(ch <-chan [2]float64) *chanStreamer { return &chanStreamer{ch: ch} }

func (c *chanStreamer) Stream(buf [][2]float64) (int, bool) {
	for i := range buf {
		select {
		case smp := <-c.ch:
			buf[i] = smp
		default:
			buf[i] = [2]float64{}
		}
	}
	return len(buf), true
}

// Flush discards all pending audio samples in the channel.
func (c *chanStreamer) Flush() {
	for {
		select {
		case <-c.ch:
		default:
			return
		}
	}
}

func (c *chanStreamer) Err() error { return nil }
