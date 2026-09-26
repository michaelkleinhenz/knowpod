package audio

import "bytes"

// Sniff identifies common audio container formats from the first bytes of a file. It
// returns the media type and a file extension, or ("", "") when the format is unknown.
func Sniff(head []byte) (contentType, ext string) {
	switch {
	case len(head) >= 12 && string(head[0:4]) == "RIFF" && string(head[8:12]) == "WAVE":
		return "audio/wav", "wav"
	case bytes.HasPrefix(head, []byte("fLaC")):
		return "audio/flac", "flac"
	case bytes.HasPrefix(head, []byte("OggS")):
		return "audio/ogg", "ogg"
	case bytes.HasPrefix(head, []byte("ID3")),
		len(head) >= 2 && head[0] == 0xFF && head[1]&0xE0 == 0xE0 && head[1]&0x06 != 0:
		return "audio/mpeg", "mp3"
	case len(head) >= 2 && head[0] == 0xFF && head[1]&0xF6 == 0xF0:
		return "audio/aac", "aac"
	case len(head) >= 8 && string(head[4:8]) == "ftyp":
		return "audio/mp4", "m4a"
	case bytes.HasPrefix(head, []byte{0x1A, 0x45, 0xDF, 0xA3}):
		return "audio/webm", "webm"
	}
	return "", ""
}

// ExtensionFor returns a file extension for an audio media type ("bin" if unknown).
func ExtensionFor(contentType string) string {
	switch contentType {
	case "audio/wav", "audio/x-wav", "audio/wave":
		return "wav"
	case "audio/flac", "audio/x-flac":
		return "flac"
	case "audio/ogg", "audio/opus":
		return "ogg"
	case "audio/mpeg", "audio/mp3":
		return "mp3"
	case "audio/aac":
		return "aac"
	case "audio/mp4", "audio/x-m4a", "audio/m4a":
		return "m4a"
	case "audio/webm":
		return "webm"
	}
	return "bin"
}
