// Package audio parses WAV files and transcodes them to FLAC.
package audio

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/recording"
)

// ErrNotWAV is returned for input that is not a RIFF/WAVE file.
var ErrNotWAV = errors.New("not a WAV file")

// ErrUnsupportedFormat is returned for WAV files whose audio encoding cannot be handled.
var ErrUnsupportedFormat = errors.New("unsupported WAV format")

const (
	formatPCM        = 0x0001
	formatExtensible = 0xFFFE
)

// WAVInfo describes the PCM stream of a WAV file and where its samples are.
type WAVInfo struct {
	SampleRate    uint32
	Channels      uint16
	BitsPerSample uint16
	BlockAlign    uint16 // bytes per frame (one sample of every channel)
	DataOffset    int64  // file offset of the first sample
	DataSize      int64  // bytes of sample data, a multiple of BlockAlign
}

// Frames returns the number of samples per channel.
func (i *WAVInfo) Frames() uint64 { return uint64(i.DataSize) / uint64(i.BlockAlign) }

// Duration returns the playing time.
func (i *WAVInfo) Duration() time.Duration {
	return time.Duration(i.Frames()) * time.Second / time.Duration(i.SampleRate)
}

// Format converts the info into the recording's format metadata.
func (i *WAVInfo) Format() *recording.Format {
	return &recording.Format{
		SampleRate:    i.SampleRate,
		Channels:      i.Channels,
		BitsPerSample: i.BitsPerSample,
		Frames:        i.Frames(),
		DurationMs:    i.Duration().Milliseconds(),
	}
}

// ReadWAVInfo parses the RIFF header of a WAV file of the given total size. Only integer PCM
// with 8, 16 or 24 bits per sample and 1–8 channels is accepted. A data chunk whose declared
// size is missing or overruns the file (common for recorders that stream the header before
// the length is known) is taken to extend to the end of the file.
func ReadWAVInfo(r io.ReadSeeker, fileSize int64) (*WAVInfo, error) {
	var hdr [12]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, ErrNotWAV
	}
	if string(hdr[0:4]) != "RIFF" || string(hdr[8:12]) != "WAVE" {
		return nil, ErrNotWAV
	}

	var info WAVInfo
	haveFmt := false
	pos := int64(12)
	for {
		var ch [8]byte
		if _, err := io.ReadFull(r, ch[:]); err != nil {
			return nil, fmt.Errorf("%w: no data chunk", ErrNotWAV)
		}
		pos += 8
		id := string(ch[0:4])
		size := int64(binary.LittleEndian.Uint32(ch[4:8]))

		switch id {
		case "fmt ":
			if err := parseFmt(r, size, &info); err != nil {
				return nil, err
			}
			haveFmt = true
		case "data":
			if !haveFmt {
				return nil, fmt.Errorf("%w: data chunk before fmt chunk", ErrNotWAV)
			}
			info.DataOffset = pos
			if size == 0 || size == 0xFFFFFFFF || pos+size > fileSize {
				size = fileSize - pos
			}
			info.DataSize = size - size%int64(info.BlockAlign)
			if info.DataSize <= 0 {
				return nil, fmt.Errorf("%w: no audio samples", ErrNotWAV)
			}
			return &info, nil
		}

		// Skip to the next chunk; chunks are padded to an even size.
		next := pos + size + size%2
		if _, err := r.Seek(next, io.SeekStart); err != nil {
			return nil, err
		}
		pos = next
	}
}

func parseFmt(r io.ReadSeeker, size int64, info *WAVInfo) error {
	if size < 16 {
		return fmt.Errorf("%w: fmt chunk too short", ErrNotWAV)
	}
	buf := make([]byte, min(size, 40))
	if _, err := io.ReadFull(r, buf); err != nil {
		return fmt.Errorf("%w: truncated fmt chunk", ErrNotWAV)
	}
	tag := binary.LittleEndian.Uint16(buf[0:2])
	info.Channels = binary.LittleEndian.Uint16(buf[2:4])
	info.SampleRate = binary.LittleEndian.Uint32(buf[4:8])
	info.BlockAlign = binary.LittleEndian.Uint16(buf[12:14])
	info.BitsPerSample = binary.LittleEndian.Uint16(buf[14:16])

	if tag == formatExtensible && len(buf) >= 26 {
		// WAVE_FORMAT_EXTENSIBLE: the real format tag leads the sub-format GUID.
		tag = binary.LittleEndian.Uint16(buf[24:26])
	}
	if tag != formatPCM {
		return fmt.Errorf("%w: encoding 0x%04x (only integer PCM is supported)", ErrUnsupportedFormat, tag)
	}
	switch info.BitsPerSample {
	case 8, 16, 24:
	default:
		return fmt.Errorf("%w: %d bits per sample", ErrUnsupportedFormat, info.BitsPerSample)
	}
	if info.Channels < 1 || info.Channels > 8 {
		return fmt.Errorf("%w: %d channels", ErrUnsupportedFormat, info.Channels)
	}
	if info.SampleRate < 1 || info.SampleRate > 655350 {
		return fmt.Errorf("%w: sample rate %d Hz", ErrUnsupportedFormat, info.SampleRate)
	}
	if want := info.Channels * (info.BitsPerSample / 8); info.BlockAlign != want {
		return fmt.Errorf("%w: block align %d, want %d", ErrUnsupportedFormat, info.BlockAlign, want)
	}
	return nil
}
