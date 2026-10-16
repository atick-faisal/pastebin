// Package textstate holds the authoritative shared text boxes for one server process.
package textstate

import (
	"errors"
	"slices"
	"sync"
	"unicode/utf8"
)

const (
	// MaxTextBytes is the inclusive limit on one box's UTF-8 text, measured in bytes.
	MaxTextBytes = 10 * 1024 * 1024
	// MaxBoxes is the inclusive limit on boxes, which bounds memory to about
	// MaxBoxes × MaxTextBytes on a shared host.
	MaxBoxes = 10
)

var (
	ErrTooLarge     = errors.New("text exceeds the 10 MiB limit")
	ErrInvalidUTF8  = errors.New("text is not valid UTF-8")
	ErrUnknownBox   = errors.New("box does not exist")
	ErrTooManyBoxes = errors.New("box limit of 10 reached")
	ErrLastBox      = errors.New("the last box cannot be removed")
)

// Box is one text box. IDs are never reused, so a write aimed at a removed
// box cannot land in a box created after it.
type Box struct {
	ID   uint64
	Text string
}

// Snapshot is a consistent copy of every box, in creation order, and the
// revision that produced it.
type Snapshot struct {
	Boxes    []Box
	Revision uint64
}

// State serializes access to shared boxes. Construct it with New.
// A State must not be copied after first use.
type State struct {
	mu       sync.Mutex
	boxes    []Box
	lastID   uint64
	revision uint64
}

// New returns one empty box at revision 0, so the page is never empty.
func New() *State {
	return &State{boxes: []Box{{ID: 1}}, lastID: 1}
}

// Current returns the latest accepted snapshot.
func (s *State) Current() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshot()
}

// Add appends an empty box, which is last in the returned snapshot.
func (s *State) Add() (Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.boxes) >= MaxBoxes {
		return Snapshot{}, ErrTooManyBoxes
	}
	s.lastID++
	s.boxes = append(s.boxes, Box{ID: s.lastID})
	return s.accept(), nil
}

// Remove deletes a box. The last remaining box cannot be removed.
func (s *State) Remove(id uint64) (Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.find(id)
	if i < 0 {
		return Snapshot{}, ErrUnknownBox
	}
	if len(s.boxes) == 1 {
		return Snapshot{}, ErrLastBox
	}
	s.boxes = slices.Delete(s.boxes, i, i+1)
	return s.accept(), nil
}

// Write replaces one box's whole text, even when the text is unchanged.
func (s *State) Write(id uint64, text string) (Snapshot, error) {
	// Validate before locking, so scanning 10 MiB never delays other writers.
	if len(text) > MaxTextBytes {
		return Snapshot{}, ErrTooLarge
	}
	if !utf8.ValidString(text) {
		return Snapshot{}, ErrInvalidUTF8
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.find(id)
	if i < 0 {
		return Snapshot{}, ErrUnknownBox
	}
	s.boxes[i].Text = text
	return s.accept(), nil
}

// accept advances the one global revision. Lock acquisition defines acceptance
// order across adds, removes and writes; rejected calls never reach here, so
// they leave the state unchanged and consume no revision.
func (s *State) accept() Snapshot {
	s.revision++
	return s.snapshot()
}

func (s *State) snapshot() Snapshot {
	// Cloning keeps callers from mutating shared boxes; the texts themselves
	// are immutable strings, so this copies at most MaxBoxes small headers.
	return Snapshot{Boxes: slices.Clone(s.boxes), Revision: s.revision}
}

func (s *State) find(id uint64) int {
	return slices.IndexFunc(s.boxes, func(box Box) bool { return box.ID == id })
}
