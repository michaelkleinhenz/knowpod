package service

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// Spool keeps in-flight and received WAV files on local disk until they are archived. Files
// are named by the (server-generated) recording ID. The size of a file is the upload offset,
// so bytes that arrived before a dropped connection count and the device resumes after them.
type Spool struct{ dir string }

// NewSpool creates the spool directory if needed.
func NewSpool(dir string) (*Spool, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, err
	}
	return &Spool{dir: dir}, nil
}

// WAVPath returns the path of the recording's WAV file.
func (s *Spool) WAVPath(id string) string { return filepath.Join(s.dir, id+".wav") }

// FLACPath returns the path of the recording's transcoded FLAC file.
func (s *Spool) FLACPath(id string) string { return filepath.Join(s.dir, id+".flac") }

// Size returns the number of bytes received so far (0 when no file exists).
func (s *Spool) Size(id string) (int64, error) {
	st, err := os.Stat(s.WAVPath(id))
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return st.Size(), nil
}

// Append copies at most limit bytes from r to the end of the file and syncs it. It returns
// the number of bytes written, which may be non-zero even when err is set.
func (s *Spool) Append(id string, r io.Reader, limit int64) (int64, error) {
	f, err := os.OpenFile(s.WAVPath(id), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o640)
	if err != nil {
		return 0, err
	}
	n, err := io.Copy(f, io.LimitReader(r, limit))
	if serr := f.Sync(); err == nil {
		err = serr
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return n, err
}

// SHA256 returns the hex SHA-256 of the WAV file.
func (s *Spool) SHA256(id string) (string, error) {
	f, err := os.Open(s.WAVPath(id))
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Remove deletes all files of the recording.
func (s *Spool) Remove(id string) error {
	var errs []error
	for _, p := range []string{s.WAVPath(id), s.FLACPath(id)} {
		if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
