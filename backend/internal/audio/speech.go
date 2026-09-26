package audio

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/mewkiz/flac"
)

// SpeechSampleRate is the sample rate speech is reduced to before transcription. 16 kHz
// keeps everything speech recognition needs at a third of the size of 48 kHz audio.
const SpeechSampleRate = 16000

// SpeechChunks decodes a FLAC file and calls emit with consecutive WAV files of at most
// chunk length each: mono, 16 bit, at most SpeechSampleRate. Keeping chunks short keeps each
// transcription request small enough for the AI provider. Memory use is bounded by one chunk.
func SpeechChunks(flacPath string, chunk time.Duration, emit func(wav []byte) error) error {
	stream, err := flac.ParseFile(flacPath)
	if err != nil {
		return fmt.Errorf("open FLAC: %w", err)
	}
	defer stream.Close()

	inRate := int(stream.Info.SampleRate)
	outRate := min(inRate, SpeechSampleRate)
	bps := int(stream.Info.BitsPerSample)
	perChunk := int(chunk.Seconds() * float64(outRate))
	if perChunk <= 0 {
		return errors.New("chunk length too short")
	}

	buf := make([]int16, 0, perChunk)
	flush := func() error {
		if len(buf) == 0 {
			return nil
		}
		err := emit(encodeWAV16(buf, outRate))
		buf = buf[:0]
		return err
	}
	dec := newDecimator(inRate, outRate, func(v int16) error {
		buf = append(buf, v)
		if len(buf) == perChunk {
			return flush()
		}
		return nil
	})

	for {
		f, err := stream.ParseNext()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("decode FLAC: %w", err)
		}
		nch := len(f.Subframes)
		for i := 0; i < int(f.BlockSize); i++ {
			var sum int64
			for c := 0; c < nch; c++ {
				sum += int64(f.Subframes[c].Samples[i])
			}
			if err := dec.push(to16(sum/int64(nch), bps)); err != nil {
				return err
			}
		}
	}
	if err := dec.finish(); err != nil {
		return err
	}
	return flush()
}

// to16 scales a sample of the given bit depth to 16 bits.
func to16(v int64, bps int) int16 {
	switch {
	case bps > 16:
		v >>= bps - 16
	case bps < 16:
		v <<= 16 - bps
	}
	return int16(max(-32768, min(32767, v)))
}

// decimator lowers the sample rate by averaging the input samples that fall into each output
// sample's interval. The averaging doubles as a simple low-pass filter against aliasing,
// which is adequate for speech recognition.
type decimator struct {
	step  float64 // input samples per output sample
	n     int64   // input samples seen
	cur   int64   // output sample being accumulated
	sum   int64
	count int64
	emit  func(int16) error
}

func newDecimator(inRate, outRate int, emit func(int16) error) *decimator {
	return &decimator{step: float64(inRate) / float64(outRate), emit: emit}
}

func (d *decimator) push(v int16) error {
	k := int64(float64(d.n) / d.step)
	d.n++
	if k != d.cur && d.count > 0 {
		if err := d.emit(int16(d.sum / d.count)); err != nil {
			return err
		}
		d.sum, d.count = 0, 0
	}
	d.cur = k
	d.sum += int64(v)
	d.count++
	return nil
}

func (d *decimator) finish() error {
	if d.count == 0 {
		return nil
	}
	defer func() { d.sum, d.count = 0, 0 }()
	return d.emit(int16(d.sum / d.count))
}

// encodeWAV16 writes mono 16-bit PCM samples as a WAV file.
func encodeWAV16(samples []int16, rate int) []byte {
	var b bytes.Buffer
	dataSize := len(samples) * 2
	b.Grow(44 + dataSize)
	b.WriteString("RIFF")
	_ = binary.Write(&b, binary.LittleEndian, uint32(36+dataSize))
	b.WriteString("WAVEfmt ")
	for _, v := range []any{uint32(16), uint16(1), uint16(1), uint32(rate), uint32(rate * 2), uint16(2), uint16(16)} {
		_ = binary.Write(&b, binary.LittleEndian, v)
	}
	b.WriteString("data")
	_ = binary.Write(&b, binary.LittleEndian, uint32(dataSize))
	_ = binary.Write(&b, binary.LittleEndian, samples)
	return b.Bytes()
}
