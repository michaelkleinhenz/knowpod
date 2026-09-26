package audio

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/mewkiz/flac"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/audio/audiotest"
)

// decodeFLAC returns the per-channel samples of a FLAC file.
func decodeFLAC(t *testing.T, path string) (*flac.Stream, [][]int32) {
	t.Helper()
	stream, err := flac.ParseFile(path)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	out := make([][]int32, stream.Info.NChannels)
	for {
		f, err := stream.ParseNext()
		if err != nil {
			break
		}
		for c, sf := range f.Subframes {
			out[c] = append(out[c], sf.Samples...)
		}
	}
	return stream, out
}

func TestEncodeFLACIsLossless(t *testing.T) {
	for _, tc := range []struct {
		name           string
		rate, bits, ch int
		frames         int
	}{
		{"16bit-stereo", 16000, 16, 2, 10000}, // not a multiple of the block size
		{"8bit-mono", 8000, 8, 1, 5000},
		{"24bit-mono", 48000, 24, 1, 4096},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			want := audiotest.Samples(tc.ch, tc.frames, tc.bits)
			wav := audiotest.WAV(tc.rate, tc.bits, want)
			wavPath := filepath.Join(dir, "in.wav")
			flacPath := filepath.Join(dir, "out.flac")
			if err := os.WriteFile(wavPath, wav, 0o600); err != nil {
				t.Fatal(err)
			}

			info, err := EncodeFLAC(context.Background(), wavPath, flacPath)
			if err != nil {
				t.Fatal(err)
			}
			if info.Frames() != uint64(tc.frames) {
				t.Fatalf("frames = %d", info.Frames())
			}

			stream, got := decodeFLAC(t, flacPath)
			if stream.Info.SampleRate != uint32(tc.rate) || int(stream.Info.BitsPerSample) != tc.bits ||
				stream.Info.NSamples != uint64(tc.frames) {
				t.Fatalf("stream info = %+v", stream.Info)
			}
			for c := range want {
				if len(got[c]) != len(want[c]) {
					t.Fatalf("channel %d: %d samples, want %d", c, len(got[c]), len(want[c]))
				}
				for i := range want[c] {
					if got[c][i] != want[c][i] {
						t.Fatalf("channel %d sample %d = %d, want %d", c, i, got[c][i], want[c][i])
					}
				}
			}

			// The encoder only uses fixed predictors and Rice parameters up to 14, so noisy
			// 24-bit audio may not shrink; 8/16-bit speech-like audio must.
			st, _ := os.Stat(flacPath)
			if tc.bits <= 16 && st.Size() >= int64(len(wav)) {
				t.Errorf("FLAC (%d bytes) is not smaller than WAV (%d bytes)", st.Size(), len(wav))
			}
		})
	}
}

func TestEncodeFLACRemovesOutputOnError(t *testing.T) {
	dir := t.TempDir()
	wavPath := filepath.Join(dir, "in.wav")
	flacPath := filepath.Join(dir, "out.flac")
	_ = os.WriteFile(wavPath, audiotest.WAV(8000, 16, audiotest.Samples(1, 50000, 16)), 0o600)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := EncodeFLAC(ctx, wavPath, flacPath); err == nil {
		t.Fatal("expected error for cancelled context")
	}
	if _, err := os.Stat(flacPath); !os.IsNotExist(err) {
		t.Fatalf("partial output not removed: %v", err)
	}
}
