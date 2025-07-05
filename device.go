package audio

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"github.com/gordonklaus/portaudio"
	"github.com/smallnest/ringbuffer"
	"log/slog"
)

type PortAudioDevice struct {
	stream          *portaudio.Stream
	micBuffer       *ringbuffer.RingBuffer
	playbackBuffer  *ringbuffer.RingBuffer
	sampleRate      int
	channels        int
	framesPerBuffer int
	outputPCM16Buf  []byte
}

func NewDevice(sampleRate, channels int) (*PortAudioDevice, error) {
	fpb := 512
	bufSize := int(float64(sampleRate) * 2.0 * 0.1)
	device := &PortAudioDevice{
		sampleRate:      sampleRate,
		channels:        channels,
		framesPerBuffer: fpb,
		micBuffer:       ringbuffer.New(bufSize).SetBlocking(true),
		playbackBuffer:  ringbuffer.New(bufSize).SetBlocking(true),
		outputPCM16Buf:  make([]byte, fpb*channels*2),
	}

	// Get default devices
	inputDevice, err := portaudio.DefaultInputDevice()
	if err != nil {
		return nil, fmt.Errorf("failed to get default input device: %w", err)
	}

	outputDevice, err := portaudio.DefaultOutputDevice()
	if err != nil {
		return nil, fmt.Errorf("failed to get default output device: %w", err)
	}

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

	if err := stream.Start(); err != nil {
		return nil, fmt.Errorf("failed to start audio stream: %w", err)
	}

	return device, nil
}

func (d *PortAudioDevice) processAudio(input, output []float32) {
	if len(input) > 0 {
		var buf bytes.Buffer
		buf.Grow(len(input) * 2)
		for _, sample := range input {
			if sample > 1.0 {
				sample = 1.0
			} else if sample < -1.0 {
				sample = -1.0
			}
			val := int16(sample * 32767)
			_ = binary.Write(&buf, binary.LittleEndian, val)
		}
		_, err := d.micBuffer.Write(buf.Bytes())
		if err != nil {
			slog.Error("failed to write to micBuffer", slog.Any("err", err))
		}
	}

	// --- Handle output: read from playbackBuffer or fill with silence
	bytesNeeded := len(output) * 2
	available := d.playbackBuffer.Length()
	bytesToRead := bytesNeeded
	if available < bytesNeeded {
		bytesToRead = available
	}
	n, _ := d.playbackBuffer.Read(d.outputPCM16Buf[:bytesToRead])
	for i := n; i < bytesNeeded; i++ {
		d.outputPCM16Buf[i] = 0
	}
	for i := 0; i < len(output); i++ {
		j := i * 2
		if j+1 < len(d.outputPCM16Buf) {
			val := int16(d.outputPCM16Buf[j]) | int16(d.outputPCM16Buf[j+1])<<8
			output[i] = float32(val) / 32767.0
		} else {
			output[i] = 0
		}
	}
}

func (d *PortAudioDevice) Read(p []byte) (n int, err error) {
	return d.micBuffer.Read(p)
}

func (d *PortAudioDevice) Write(p []byte) (n int, err error) {
	return d.playbackBuffer.Write(p)
}

func (d *PortAudioDevice) Close() error {
	_ = d.stream.Stop()
	_ = d.stream.Close()
	d.micBuffer.CloseWriter()
	d.playbackBuffer.CloseWriter()
	return nil
}
