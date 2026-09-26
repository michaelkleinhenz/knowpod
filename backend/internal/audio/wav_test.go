package audio

import (
	"bytes"
	"encoding/binary"
	"errors"
	"testing"
	"time"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/audio/audiotest"
)

func parse(t *testing.T, b []byte) (*WAVInfo, error) {
	t.Helper()
	return ReadWAVInfo(bytes.NewReader(b), int64(len(b)))
}

func TestReadWAVInfo(t *testing.T) {
	wav := audiotest.WAV(16000, 16, audiotest.Samples(2, 16000, 16))
	info, err := parse(t, wav)
	if err != nil {
		t.Fatal(err)
	}
	if info.SampleRate != 16000 || info.Channels != 2 || info.BitsPerSample != 16 {
		t.Fatalf("info = %+v", info)
	}
	if info.DataOffset != 44 || info.Frames() != 16000 || info.Duration() != time.Second {
		t.Fatalf("offset=%d frames=%d duration=%v", info.DataOffset, info.Frames(), info.Duration())
	}
}

func TestReadWAVInfoSkipsOddSizedChunks(t *testing.T) {
	wav := audiotest.WAV(8000, 8, audiotest.Samples(1, 100, 8))
	// Insert a 3-byte LIST chunk (plus pad byte) between fmt and data.
	extra := append([]byte("LIST"), 3, 0, 0, 0, 'a', 'b', 'c', 0)
	b := append(append(append([]byte{}, wav[:36]...), extra...), wav[36:]...)
	info, err := parse(t, b)
	if err != nil {
		t.Fatal(err)
	}
	if info.DataOffset != 56 || info.Frames() != 100 {
		t.Fatalf("offset=%d frames=%d", info.DataOffset, info.Frames())
	}
}

func TestReadWAVInfoUnknownDataSize(t *testing.T) {
	wav := audiotest.WAV(8000, 16, audiotest.Samples(1, 100, 16))
	binary.LittleEndian.PutUint32(wav[40:44], 0xFFFFFFFF)
	info, err := parse(t, wav)
	if err != nil {
		t.Fatal(err)
	}
	if info.Frames() != 100 {
		t.Fatalf("frames = %d", info.Frames())
	}
}

func TestReadWAVInfoRejects(t *testing.T) {
	float := audiotest.WAV(8000, 16, audiotest.Samples(1, 10, 16))
	binary.LittleEndian.PutUint16(float[20:22], 3) // IEEE float

	for name, tc := range map[string]struct {
		b    []byte
		want error
	}{
		"garbage":    {[]byte("definitely not audio at all"), ErrNotWAV},
		"empty":      {nil, ErrNotWAV},
		"float":      {float, ErrUnsupportedFormat},
		"no samples": {audiotest.WAV(8000, 16, [][]int32{{}}), ErrNotWAV},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parse(t, tc.b); !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}
