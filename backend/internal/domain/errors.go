// Package domain holds the framework-free domain models and the sentinel errors shared by
// all layers.
package domain

import "errors"

var (
	// ErrNotFound is returned when an entity does not exist.
	ErrNotFound = errors.New("not found")
	// ErrDuplicate is returned when creating an entity would violate a uniqueness constraint.
	ErrDuplicate = errors.New("duplicate")
)
