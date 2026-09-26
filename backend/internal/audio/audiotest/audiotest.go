// Package audiotest generates WAV files for tests.
package audiotest

import (
	"bytes"
	"encoding/binary"
	"math"
	"math/rand"
)

// Samples returns deterministic test audio: a sine tone plus a little noise per channel,
// scaled to the given bit depth.
func Samples(channels, frames, bits int) [][]int32 {
	rng := rand.New(rand.NewSource(1))
	amp := float64(int64(1)<<(bits-1)-1) * 0.6
	out := make([][]int32, channels)
	for c := range out {
		out[c] = make([]int32, frames)
		for i := range out[c] {
			v := math.Sin(2*math.Pi*440*float64(i)/16000+float64(c)) * amp
			v += (rng.Float64() - 0.5) * amp * 0.02
			out[c][i] = int32(v)
		}
	}
	return out
}

// WAV encodes the samples as an integer PCM WAV file.
func WAV(sampleRate, bits int, samples [][]int32) []byte {
	channels := len(samples)
	frames := len(samples[0])
	bps := bits / 8
	dataSize := frames * channels * bps

	var b bytes.Buffer
	b.WriteString("RIFF")
	_ = binary.Write(&b, binary.LittleEndian, uint32(36+dataSize))
	b.WriteString("WAVEfmt ")
	_ = binary.Write(&b, binary.LittleEndian, uint32(16))
	_ = binary.Write(&b, binary.LittleEndian, uint16(1))
	_ = binary.Write(&b, binary.LittleEndian, uint16(channels))
	_ = binary.Write(&b, binary.LittleEndian, uint32(sampleRate))
	_ = binary.Write(&b, binary.LittleEndian, uint32(sampleRate*channels*bps))
	_ = binary.Write(&b, binary.LittleEndian, uint16(channels*bps))
	_ = binary.Write(&b, binary.LittleEndian, uint16(bits))
	b.WriteString("data")
	_ = binary.Write(&b, binary.LittleEndian, uint32(dataSize))
	for i := 0; i < frames; i++ {
		for c := 0; c < channels; c++ {
			v := samples[c][i]
			switch bps {
			case 1:
				b.WriteByte(byte(v + 128))
			case 2:
				_ = binary.Write(&b, binary.LittleEndian, int16(v))
			case 3:
				b.Write([]byte{byte(v), byte(v >> 8), byte(v >> 16)})
			}
		}
	}
	return b.Bytes()
}
