package audio

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/audio/audiotest"
)

func TestSpeechChunks(t *testing.T) {
	for _, tc := range []struct {
		name               string
		rate, bits, ch     int
		seconds            int
		chunk              time.Duration
		wantChunks, wantHz int
	}{
		{"48k stereo 24bit", 48000, 24, 2, 25, 10 * time.Second, 3, 16000},
		{"44.1k mono", 44100, 16, 1, 10, 4 * time.Second, 3, 16000},
		{"8k stays 8k", 8000, 16, 1, 5, time.Minute, 1, 8000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			wavPath, flacPath := filepath.Join(dir, "in.wav"), filepath.Join(dir, "in.flac")
			_ = os.WriteFile(wavPath, audiotest.WAV(tc.rate, tc.bits, audiotest.Samples(tc.ch, tc.rate*tc.seconds, tc.bits)), 0o600)
			if _, err := EncodeFLAC(context.Background(), wavPath, flacPath); err != nil {
				t.Fatal(err)
			}

			var chunks [][]byte
			if err := SpeechChunks(flacPath, tc.chunk, func(wav []byte) error {
				chunks = append(chunks, append([]byte(nil), wav...))
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if len(chunks) != tc.wantChunks {
				t.Fatalf("chunks = %d, want %d", len(chunks), tc.wantChunks)
			}
			var total time.Duration
			for i, c := range chunks {
				info, err := ReadWAVInfo(bytes.NewReader(c), int64(len(c)))
				if err != nil {
					t.Fatalf("chunk %d: %v", i, err)
				}
				if info.Channels != 1 || info.BitsPerSample != 16 || int(info.SampleRate) != tc.wantHz {
					t.Fatalf("chunk %d format = %+v", i, info)
				}
				if i < len(chunks)-1 && info.Duration() != tc.chunk {
					t.Fatalf("chunk %d duration = %v", i, info.Duration())
				}
				total += info.Duration()
			}
			if want := time.Duration(tc.seconds) * time.Second; total < want-10*time.Millisecond || total > want+10*time.Millisecond {
				t.Fatalf("total duration = %v, want %v", total, want)
			}
		})
	}
}
