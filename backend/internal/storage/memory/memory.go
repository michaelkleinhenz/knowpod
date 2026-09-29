// Package memory provides an in-memory ports.ObjectStore, used by tests.
package memory

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sort"
	"sync"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/ports"
)

// Object is a stored object.
type Object struct {
	Data        []byte
	ContentType string
}

// Store is an in-memory object store. Set Err to make every Put fail.
type Store struct {
	mu      sync.Mutex
	objects map[string]Object
	Err     error
}

// New builds an empty store.
func New() *Store { return &Store{objects: map[string]Object{}} }

func (s *Store) Put(_ context.Context, key string, body io.Reader, size int64, contentType string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Err != nil {
		return s.Err
	}
	data, err := io.ReadAll(body)
	if err != nil {
		return err
	}
	if int64(len(data)) != size {
		return errors.New("size mismatch")
	}
	s.objects[key] = Object{Data: data, ContentType: contentType}
	return nil
}

func (s *Store) Get(_ context.Context, key string, offset, length int64) (io.ReadCloser, error) {
	o, ok := s.Object(key)
	if !ok {
		return nil, domain.ErrNotFound
	}
	data := o.Data[min(offset, int64(len(o.Data))):]
	if length >= 0 && length < int64(len(data)) {
		data = data[:length]
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func (s *Store) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.objects, key)
	return nil
}

// Object returns the stored object under key.
func (s *Store) Object(key string) (Object, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	o, ok := s.objects[key]
	return o, ok
}

// List calls fn with every stored object, ordered by key.
func (s *Store) List(_ context.Context, fn func(ports.ObjectInfo) error) error {
	s.mu.Lock()
	infos := make([]ports.ObjectInfo, 0, len(s.objects))
	for k, o := range s.objects {
		infos = append(infos, ports.ObjectInfo{Key: k, Size: int64(len(o.Data)), ContentType: o.ContentType})
	}
	s.mu.Unlock()
	sort.Slice(infos, func(i, j int) bool { return infos[i].Key < infos[j].Key })
	for _, info := range infos {
		if err := fn(info); err != nil {
			return err
		}
	}
	return nil
}
