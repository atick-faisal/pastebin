// Package textstate holds the authoritative shared text for one server process.
package textstate

import (
	"errors"
	"sync"
	"unicode/utf8"
)

// MaxTextBytes is the inclusive limit on UTF-8 text, measured in bytes.
const MaxTextBytes = 10 * 1024 * 1024

var (
	ErrTooLarge    = errors.New("text exceeds the 10 MiB limit")
	ErrInvalidUTF8 = errors.New("text is not valid UTF-8")
)

// Snapshot is a consistent copy of the text and its accepted revision.
type Snapshot struct {
	Text     string
	Revision uint64
}

// State serializes access to shared text. Its zero value is empty at revision 0.
// A State must not be copied after first use.
type State struct {
	mu       sync.Mutex
	snapshot Snapshot
}

// Current returns the latest accepted snapshot.
func (s *State) Current() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshot
}

// Replace accepts a complete replacement, even when the text is unchanged.
// Lock acquisition defines acceptance order; each accepted write advances the
// revision once. Invalid writes leave the state unchanged and return no snapshot.
func (s *State) Replace(text string) (Snapshot, error) {
	if len(text) > MaxTextBytes {
		return Snapshot{}, ErrTooLarge
	}
	if !utf8.ValidString(text) {
		return Snapshot{}, ErrInvalidUTF8
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.snapshot = Snapshot{Text: text, Revision: s.snapshot.Revision + 1}
	return s.snapshot, nil
}
