package audio

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math"
	"os"
	"testing"

	"github.com/pion/opus"
)

// testdata/recorder.ops is a synthetic tone laid out like a Pocket recorder's OPS file:
// 151 SILK wideband 20 ms packets padded to 80 bytes, except packet 60, written short as
// at a pause (see testdata/mkops.py).
const (
	fixturePackets = 151
	fixtureShort   = 60
)

func readFixture(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/recorder.ops")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestSplitOPSFindsShortPackets(t *testing.T) {
	ops, err := SplitOPS(readFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(ops.Packets) != fixturePackets {
		t.Fatalf("%d packets, want %d", len(ops.Packets), fixturePackets)
	}
	for i, p := range ops.Packets {
		if short := len(p) < opsSlot; short != (i == fixtureShort) {
			t.Errorf("packet %d is %d bytes", i, len(p))
		}
	}
	if ops.SampleRate != 16000 || ops.Frames != fixturePackets*320 {
		t.Fatalf("rate %d, frames %d", ops.SampleRate, ops.Frames)
	}
	// Every packet is a whole one: all of them decode.
	dec, _ := opus.NewDecoderWithOutput(16000, 1)
	pcm := make([]int16, 5760)
	for i, p := range ops.Packets {
		if n, err := dec.DecodeToInt16(p, pcm); err != nil || n != 320 {
			t.Fatalf("packet %d: %d samples, %v", i, n, err)
		}
	}
}

func TestOPSToWAV(t *testing.T) {
	var out bytes.Buffer
	if err := OPSToWAV(readFixture(t), &out); err != nil {
		t.Fatal(err)
	}
	info, err := parse(t, out.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if info.SampleRate != 16000 || info.Channels != 1 || info.BitsPerSample != 16 || info.Frames() != fixturePackets*320 {
		t.Fatalf("info = %+v, frames %d", info, info.Frames())
	}
	if int64(out.Len()) != info.DataOffset+info.DataSize {
		t.Fatalf("file is %d bytes, header says %d", out.Len(), info.DataOffset+info.DataSize)
	}
	// The tone is there, not silence (libopus decodes the fixture to about -27 dB).
	var sum float64
	pcm := out.Bytes()[info.DataOffset:]
	for i := 0; i+1 < len(pcm); i += 2 {
		v := float64(int16(binary.LittleEndian.Uint16(pcm[i:])))
		sum += v * v
	}
	if rms := math.Sqrt(sum/float64(len(pcm)/2)) / 32768; rms < 0.02 {
		t.Fatalf("RMS %.3f: the audio is (nearly) silent", rms)
	}
}

func TestOPSToWAVCutOff(t *testing.T) {
	// A file that ends in the middle of a packet still decodes; the rest of that packet is
	// silence.
	data := readFixture(t)
	data = data[:len(data)-30]
	var out bytes.Buffer
	if err := OPSToWAV(data, &out); err != nil {
		t.Fatal(err)
	}
	info, err := parse(t, out.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if info.Frames() != fixturePackets*320 {
		t.Fatalf("frames %d", info.Frames())
	}
}

func TestSplitOPSCutOffInPacketHeader(t *testing.T) {
	data := readFixture(t)
	ops, err := SplitOPS(append(data[:len(data):len(data)], 0x4B))
	if err != nil {
		t.Fatal(err)
	}
	if len(ops.Packets) != fixturePackets {
		t.Fatalf("%d packets, want %d", len(ops.Packets), fixturePackets)
	}
}

func TestOPSToWAVRejectsOtherData(t *testing.T) {
	random := make([]byte, 4000)
	for i := range random {
		random[i] = byte(i*7919 + i>>3)
	}
	for name, data := range map[string][]byte{
		"empty":  nil,
		"mp3":    append([]byte{0xFF, 0xF3, 0x48, 0xC4}, make([]byte, 400)...),
		"wav":    wavHeader16(16000, 100),
		"stereo": {0x4F, 0x41, 0x07, 0x00},
		"random": random,
	} {
		var out bytes.Buffer
		if err := OPSToWAV(data, &out); !errors.Is(err, ErrNotOPS) {
			t.Errorf("%s: err = %v, want ErrNotOPS", name, err)
		}
		if out.Len() != 0 {
			t.Errorf("%s: wrote %d bytes", name, out.Len())
		}
	}
}
