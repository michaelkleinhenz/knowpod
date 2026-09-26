// Package service contains the application logic: device authentication, the resumable
// upload protocol and the archive processing stage. It depends only on the ports.
package service

import (
	"errors"
	"fmt"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain"
)

var (
	ErrNotFound         = domain.ErrNotFound
	errDuplicate        = domain.ErrDuplicate
	ErrUnauthorized     = errors.New("invalid or revoked device token")
	ErrInvalidInput     = errors.New("invalid input")
	ErrConflict         = errors.New("recording already exists with a different size or checksum")
	ErrTooLarge         = errors.New("upload exceeds the declared or maximum size")
	ErrChecksumMismatch = errors.New("checksum mismatch; upload has been reset, resend from offset 0")
	ErrInvalidAudio     = errors.New("uploaded file is not a supported WAV file")
)

// OffsetMismatchError is returned when a chunk does not start at the current upload offset.
// The client should resume from Current.
type OffsetMismatchError struct{ Current int64 }

func (e *OffsetMismatchError) Error() string {
	return fmt.Sprintf("offset mismatch; current offset is %d", e.Current)
}

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidInput, fmt.Sprintf(format, args...))
}
