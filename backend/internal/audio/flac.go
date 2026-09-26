package audio

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/mewkiz/flac"
	"github.com/mewkiz/flac/frame"
	"github.com/mewkiz/flac/meta"
)

// flacBlockSize is the number of samples per channel in each FLAC frame (libFLAC's default).
const flacBlockSize = 4096

// EncodeFLAC transcodes the WAV file at wavPath losslessly into a FLAC file at flacPath and
// returns the parsed WAV info. The file is processed block by block, so memory use is
// independent of the recording length. On error, the partial output file is removed.
func EncodeFLAC(ctx context.Context, wavPath, flacPath string) (info *WAVInfo, err error) {
	in, err := os.Open(wavPath)
	if err != nil {
		return nil, err
	}
	defer in.Close()
	st, err := in.Stat()
	if err != nil {
		return nil, err
	}
	info, err = ReadWAVInfo(in, st.Size())
	if err != nil {
		return nil, err
	}
	if _, err := in.Seek(info.DataOffset, io.SeekStart); err != nil {
		return nil, err
	}

	out, err := os.Create(flacPath)
	if err != nil {
		return nil, err
	}
	closed := false
	defer func() {
		if !closed {
			_ = out.Close()
		}
		if err != nil {
			_ = os.Remove(flacPath)
		}
	}()

	enc, err := flac.NewEncoder(out, &meta.StreamInfo{
		BlockSizeMin:  flacBlockSize,
		BlockSizeMax:  flacBlockSize,
		SampleRate:    info.SampleRate,
		NChannels:     uint8(info.Channels),
		BitsPerSample: uint8(info.BitsPerSample),
		NSamples:      info.Frames(),
	})
	if err != nil {
		return nil, fmt.Errorf("flac encoder: %w", err)
	}

	src := bufio.NewReaderSize(io.LimitReader(in, info.DataSize), 1<<16)
	nch := int(info.Channels)
	bytesPerSample := int(info.BitsPerSample / 8)
	buf := make([]byte, flacBlockSize*int(info.BlockAlign))
	samples := make([][]int32, nch)
	for c := range samples {
		samples[c] = make([]int32, flacBlockSize)
	}

	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		n, rerr := io.ReadFull(src, buf)
		frames := n / int(info.BlockAlign)
		if frames > 0 {
			decodePCM(buf[:frames*int(info.BlockAlign)], samples, bytesPerSample)
			f := &frame.Frame{
				Header: frame.Header{
					HasFixedBlockSize: true,
					BlockSize:         uint16(frames),
					SampleRate:        info.SampleRate,
					Channels:          frame.Channels(nch - 1), // independent channels: mono, LR, LRC, …
					BitsPerSample:     uint8(info.BitsPerSample),
				},
				Subframes: make([]*frame.Subframe, nch),
			}
			for c := range f.Subframes {
				// Verbatim subframes are analysed by the encoder, which picks the best
				// predictor for each one.
				f.Subframes[c] = &frame.Subframe{
					SubHeader: frame.SubHeader{Pred: frame.PredVerbatim},
					Samples:   samples[c][:frames],
					NSamples:  frames,
				}
			}
			if err := enc.WriteFrame(f); err != nil {
				return nil, fmt.Errorf("flac encode: %w", err)
			}
		}
		if errors.Is(rerr, io.EOF) || errors.Is(rerr, io.ErrUnexpectedEOF) {
			break
		}
		if rerr != nil {
			return nil, rerr
		}
	}

	// Close rewrites the StreamInfo block (MD5, sample count) and closes the file.
	closed = true
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("flac finalize: %w", err)
	}
	return info, nil
}

// decodePCM de-interleaves little-endian integer PCM into per-channel samples. 8-bit WAV
// samples are unsigned and are re-centred around zero as FLAC expects.
func decodePCM(buf []byte, samples [][]int32, bytesPerSample int) {
	nch := len(samples)
	for i, off := 0, 0; off < len(buf); i++ {
		for c := 0; c < nch; c++ {
			var v int32
			switch bytesPerSample {
			case 1:
				v = int32(buf[off]) - 128
			case 2:
				v = int32(int16(uint16(buf[off]) | uint16(buf[off+1])<<8))
			case 3:
				v = int32(uint32(buf[off])<<8|uint32(buf[off+1])<<16|uint32(buf[off+2])<<24) >> 8
			}
			samples[c][i] = v
			off += bytesPerSample
		}
	}
}
