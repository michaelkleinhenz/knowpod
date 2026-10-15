package audio

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"github.com/pion/opus"
)

// ErrNotOPS is returned for input that is not a recorder's OPS file.
var ErrNotOPS = errors.New("not an OPS recording")

// An OPS file is what Pocket recorders built on a Jieli chip write instead of MP3: raw Opus
// packets one after another, without a container, header or timestamps. The recorder pads
// every packet to opsSlot bytes (Opus code 3 padding), except the last one before a pause or
// the end, which it writes as it is. Reading the file in steps of opsSlot therefore falls
// out of step after every pause; SplitOPS finds where the next packet really starts.
const opsSlot = 80

// opsCheckPackets is how many packets DecodeOPS decodes up front to tell an OPS file from
// other data before it writes anything.
const opsCheckPackets = 50

// OPS is an OPS file split into its Opus packets.
type OPS struct {
	Packets    [][]byte
	SampleRate int // output rate: the widest audio bandwidth among the packets
	Frames     int // samples per channel of the decoded audio
}

// SplitOPS splits an OPS file into its Opus packets. It fails with ErrNotOPS for data that
// isn't one.
func SplitOPS(data []byte) (*OPS, error) {
	if len(data) < 2 || !opsMono(data[0]) {
		return nil, ErrNotOPS
	}
	config := data[0] >> 3
	start := func(at int) bool { return opsPacketStart(data, at, config) }
	// One byte that merely looks like a packet start is not enough to go by. aligned says a
	// packet starts at at and the next two follow at full length (or the file ends); a
	// pause right after it is rare enough to not count. shortAt returns where the next
	// aligned run starts within the slot of a short packet at at, or -1.
	aligned := func(at int) bool {
		for n := range 3 {
			if next := at + n*opsSlot; next < len(data) && !start(next) {
				return false
			}
		}
		return true
	}
	shortAt := func(at int) int {
		for next := at + 2; next < at+opsSlot && next < len(data); next++ {
			if aligned(next) {
				return next
			}
		}
		return -1
	}
	if !aligned(0) && shortAt(0) < 0 {
		return nil, ErrNotOPS
	}

	ops := &OPS{}
	for pos := 0; pos < len(data); {
		end := pos + opsSlot
		if end >= len(data) {
			end = len(data) // the last packet, maybe short or cut off
		} else if !aligned(end) {
			if next := shortAt(pos); next >= 0 {
				end = next // a short packet: it ends where the next run of packets begins
			}
			// Otherwise the next packet is the short one, or the data is damaged: the slot
			// is taken as it is (and decodes as silence if it isn't a packet).
		}
		pkt := data[pos:end]
		frames, rate, err := opsPacketInfo(pkt)
		if err != nil && end == len(data) {
			break // the file ends in a cut-off packet start
		}
		if err != nil {
			return nil, fmt.Errorf("%w: packet at byte %d: %v", ErrNotOPS, pos, err)
		}
		ops.Packets = append(ops.Packets, pkt)
		ops.Frames += frames // at 48 kHz for now
		ops.SampleRate = max(ops.SampleRate, rate)
		pos = end
		if pos < len(data) && !start(pos) {
			// Damaged data: go on at the next run of packets, or stop.
			next := pos + 1
			for next < len(data) && !aligned(next) {
				next++
			}
			pos = next
		}
	}
	ops.Frames = ops.Frames * ops.SampleRate / 48000
	return ops, nil
}

// opsMono says the TOC byte is a mono packet's.
func opsMono(toc byte) bool { return toc&0x04 == 0 }

// opsPacketStart says a packet of the file's configuration (mode, bandwidth and frame
// length) starts at data[at]. The recorder never changes it within a file.
func opsPacketStart(data []byte, at int, config byte) bool {
	if at+1 >= len(data) || data[at]>>3 != config || !opsMono(data[at]) {
		return false
	}
	if data[at]&3 == 3 {
		// Code 3: a frame count of 1 to 48 and no VBR flag (the recorder writes CBR).
		n := data[at+1] & 0x3F
		return n >= 1 && n <= 48 && data[at+1]&0x80 == 0
	}
	return true
}

// opsPacketInfo returns the number of samples a packet decodes to at 48 kHz and the sample
// rate its bandwidth needs.
func opsPacketInfo(pkt []byte) (frames48k, sampleRate int, err error) {
	config := int(pkt[0] >> 3)
	var perFrame int // samples per frame at 48 kHz
	switch {
	case config < 12: // SILK: 10, 20, 40, 60 ms
		perFrame = []int{480, 960, 1920, 2880}[config%4]
		sampleRate = []int{8000, 12000, 16000}[config/4]
	case config < 16: // hybrid: 10, 20 ms
		perFrame = []int{480, 960}[config%2]
		sampleRate = []int{24000, 48000}[(config-12)/2]
	default: // CELT: 2.5, 5, 10, 20 ms
		perFrame = []int{120, 240, 480, 960}[config%4]
		sampleRate = []int{8000, 16000, 24000, 48000}[(config-16)/4]
	}
	count := 1
	switch pkt[0] & 3 {
	case 1, 2:
		count = 2
	case 3:
		if len(pkt) < 2 {
			return 0, 0, errors.New("truncated")
		}
		count = int(pkt[1] & 0x3F)
	}
	if count*perFrame > 5760 { // 120 ms, the most a packet may hold
		return 0, 0, errors.New("longer than 120 ms")
	}
	return count * perFrame, sampleRate, nil
}

// DecodeOPS splits an OPS file into its packets and decodes the first of them, so data that
// only looks like OPS fails with ErrNotOPS before anything is written.
func DecodeOPS(data []byte) (*OPS, error) {
	ops, err := SplitOPS(data)
	if err != nil {
		return nil, err
	}
	if err := ops.check(); err != nil {
		return nil, err
	}
	return ops, nil
}

// OPSToWAV decodes an OPS file into a WAV file (see WriteWAV).
func OPSToWAV(data []byte, w io.Writer) error {
	ops, err := DecodeOPS(data)
	if err != nil {
		return err
	}
	return ops.WriteWAV(w)
}

// WAVSize returns the size of the WAV file WriteWAV writes.
func (o *OPS) WAVSize() int64 { return 44 + 2*int64(o.Frames) }

// WriteWAV decodes the recording into a 16-bit mono PCM WAV file, at the sample rate of
// its audio bandwidth (16 kHz for the Pocket recorder's wideband speech). A packet that
// doesn't decode becomes silence of its length, so the timing stays right.
func (o *OPS) WriteWAV(w io.Writer) error {
	dec, err := opus.NewDecoderWithOutput(o.SampleRate, 1)
	if err != nil {
		return err
	}
	if _, err := w.Write(wavHeader16(o.SampleRate, o.Frames)); err != nil {
		return err
	}
	pcm := make([]int16, 5760)
	out := make([]byte, 0, 2*5760)
	for _, pkt := range o.Packets {
		frames48k, _, _ := opsPacketInfo(pkt)
		want := frames48k * o.SampleRate / 48000
		n, err := dec.DecodeToInt16(pkt, pcm)
		if err != nil {
			n = 0
		}
		n = min(n, want)
		clear(pcm[n:want])
		out = out[:0]
		for _, v := range pcm[:want] {
			out = binary.LittleEndian.AppendUint16(out, uint16(v))
		}
		if _, err := w.Write(out); err != nil {
			return err
		}
	}
	return nil
}

// check decodes the first packets: most of them failing means the data only looks like OPS.
func (o *OPS) check() error {
	dec, err := opus.NewDecoderWithOutput(o.SampleRate, 1)
	if err != nil {
		return err
	}
	pcm := make([]int16, 5760)
	n := min(len(o.Packets), opsCheckPackets)
	failed := 0
	for _, pkt := range o.Packets[:n] {
		if _, err := dec.DecodeToInt16(pkt, pcm); err != nil {
			failed++
		}
	}
	if failed*2 > n {
		return fmt.Errorf("%w: %d of the first %d packets don't decode", ErrNotOPS, failed, n)
	}
	return nil
}

// wavHeader16 returns the header of a 16-bit mono PCM WAV file with the given samples.
func wavHeader16(rate, frames int) []byte {
	data := uint32(frames * 2)
	h := make([]byte, 0, 44)
	h = append(h, "RIFF"...)
	h = binary.LittleEndian.AppendUint32(h, 36+data)
	h = append(h, "WAVEfmt "...)
	h = binary.LittleEndian.AppendUint32(h, 16)
	h = binary.LittleEndian.AppendUint16(h, formatPCM)
	h = binary.LittleEndian.AppendUint16(h, 1) // channels
	h = binary.LittleEndian.AppendUint32(h, uint32(rate))
	h = binary.LittleEndian.AppendUint32(h, uint32(rate*2)) // bytes per second
	h = binary.LittleEndian.AppendUint16(h, 2)              // block align
	h = binary.LittleEndian.AppendUint16(h, 16)             // bits per sample
	h = append(h, "data"...)
	return binary.LittleEndian.AppendUint32(h, data)
}
