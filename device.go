package audio

import (
	"fmt"
	"github.com/gordonklaus/portaudio"
	"io"
	"sync"
	"time"
)

type PortAudioDevice struct {
	stream          *portaudio.Stream
	inputBuffer     []float32
	outputBuffer    []float32
	inputData       chan []byte
	outputData      chan []byte
	sampleRate      int
	channels        int
	framesPerBuffer int
	mu              sync.RWMutex
	closed          bool
}

func NewDevice(sampleRate, channels int) (*PortAudioDevice, error) {
	device := &PortAudioDevice{
		sampleRate:      sampleRate,
		channels:        channels,
		framesPerBuffer: 512,
		inputData:       make(chan []byte, 10),
		outputData:      make(chan []byte, 10),
	}

	// Initialize buffers
	device.inputBuffer = make([]float32, device.framesPerBuffer*channels)
	device.outputBuffer = make([]float32, device.framesPerBuffer*channels)

	// Get default devices
	inputDevice, err := portaudio.DefaultInputDevice()
	if err != nil {
		return nil, fmt.Errorf("failed to get default input device: %w", err)
	}

	outputDevice, err := portaudio.DefaultOutputDevice()
	if err != nil {
		return nil, fmt.Errorf("failed to get default output device: %w", err)
	}

	// Create stream parameters
	inputParams := portaudio.StreamParameters{
		Input: portaudio.StreamDeviceParameters{
			Device:   inputDevice,
			Channels: channels,
			Latency:  inputDevice.DefaultLowInputLatency,
		},
		SampleRate:      float64(sampleRate),
		FramesPerBuffer: device.framesPerBuffer,
	}

	outputParams := portaudio.StreamParameters{
		Output: portaudio.StreamDeviceParameters{
			Device:   outputDevice,
			Channels: channels,
			Latency:  outputDevice.DefaultLowOutputLatency,
		},
		SampleRate:      float64(sampleRate),
		FramesPerBuffer: device.framesPerBuffer,
	}

	// Create duplex stream
	stream, err := portaudio.OpenStream(portaudio.StreamParameters{
		Input:           inputParams.Input,
		Output:          outputParams.Output,
		SampleRate:      float64(sampleRate),
		FramesPerBuffer: device.framesPerBuffer,
	}, device.processAudio)

	if err != nil {
		return nil, fmt.Errorf("failed to create audio stream: %w", err)
	}

	device.stream = stream

	// Start the stream
	if err := stream.Start(); err != nil {
		return nil, fmt.Errorf("failed to start audio stream: %w", err)
	}

	return device, nil
}

func (d *PortAudioDevice) processAudio(input, output []float32) {
	if d.closed {
		return
	}

	// Convert input from float32 to PCM16 bytes
	if len(input) > 0 {
		pcm16Data := make([]byte, len(input)*2)
		for i, sample := range input {
			// Convert float32 [-1.0, 1.0] to int16 [-32768, 32767]
			val := int16(sample * 32767)
			pcm16Data[i*2] = byte(val)
			pcm16Data[i*2+1] = byte(val >> 8)
		}

		select {
		case d.inputData <- pcm16Data:
		default:
			// Drop data if buffer is full
		}
	}

	// Convert output from PCM16 bytes to float32
	select {
	case pcm16Data := <-d.outputData:
		for i := 0; i < len(output) && i*2+1 < len(pcm16Data); i++ {
			// Convert int16 to float32
			val := int16(pcm16Data[i*2]) | int16(pcm16Data[i*2+1])<<8
			output[i] = float32(val) / 32767.0
		}
	default:
		// Output silence if no data
		for i := range output {
			output[i] = 0
		}
	}
}

func (d *PortAudioDevice) Read(p []byte) (n int, err error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	if d.closed {
		return 0, io.EOF
	}

	select {
	case data := <-d.inputData:
		n = copy(p, data)
		return n, nil
	case <-time.After(100 * time.Millisecond):
		// Return silence if no data available
		for i := range p {
			p[i] = 0
		}
		return len(p), nil
	}
}

func (d *PortAudioDevice) Write(p []byte) (n int, err error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	if d.closed {
		return 0, io.ErrClosedPipe
	}

	select {
	case d.outputData <- p:
		return len(p), nil
	default:
		// Drop data if buffer is full
		return len(p), nil
	}
}

func (d *PortAudioDevice) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.closed {
		return nil
	}

	d.closed = true

	if d.stream != nil {
		if err := d.stream.Stop(); err != nil {
			return fmt.Errorf("failed to stop audio stream: %w", err)
		}
		if err := d.stream.Close(); err != nil {
			return fmt.Errorf("failed to close audio stream: %w", err)
		}
	}

	close(d.inputData)
	close(d.outputData)

	return nil
}
