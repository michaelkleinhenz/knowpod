package audio

import (
	"testing"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/audio/audiotest"
)

func TestSniff(t *testing.T) {
	for name, tc := range map[string]struct {
		head []byte
		want string
	}{
		"wav":     {audiotest.WAV(8000, 16, audiotest.Samples(1, 10, 16)), "audio/wav"},
		"flac":    {[]byte("fLaC\x00\x00\x00\x22"), "audio/flac"},
		"ogg":     {[]byte("OggS\x00\x02"), "audio/ogg"},
		"mp3 id3": {[]byte("ID3\x04\x00"), "audio/mpeg"},
		"mp3":     {[]byte{0xFF, 0xFB, 0x90, 0x64}, "audio/mpeg"},
		"aac":     {[]byte{0xFF, 0xF1, 0x50, 0x80}, "audio/aac"},
		"m4a":     {[]byte("\x00\x00\x00\x20ftypM4A "), "audio/mp4"},
		"webm":    {[]byte{0x1A, 0x45, 0xDF, 0xA3, 0x9F}, "audio/webm"},
		"unknown": {[]byte("hello world"), ""},
	} {
		if got, _ := Sniff(tc.head); got != tc.want {
			t.Errorf("%s: Sniff = %q, want %q", name, got, tc.want)
		}
	}
}
