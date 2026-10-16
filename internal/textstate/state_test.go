package textstate

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
)

func equal(a, b Snapshot) bool {
	return a.Revision == b.Revision && slices.Equal(a.Boxes, b.Boxes)
}

// wantRejected checks that a rejected call returned no snapshot, left the
// state as it was, and did not consume a revision.
func wantRejected(t *testing.T, state *State, before Snapshot, got Snapshot, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
	if !equal(got, Snapshot{}) {
		t.Fatal("rejected call returned a snapshot")
	}
	if !equal(state.Current(), before) {
		t.Fatal("rejected call changed shared state")
	}
}

func TestNewStartsWithOneEmptyBox(t *testing.T) {
	if got, want := New().Current(), (Snapshot{Boxes: []Box{{ID: 1}}}); !equal(got, want) {
		t.Fatalf("initial snapshot = %+v, want %+v", got, want)
	}
}

func TestChangesShareOneOrderedRevision(t *testing.T) {
	state := New()
	steps := []struct {
		name   string
		change func() (Snapshot, error)
		boxes  []Box
	}{
		{"write first", func() (Snapshot, error) { return state.Write(1, "one") }, []Box{{1, "one"}}},
		{"add second", state.Add, []Box{{1, "one"}, {2, ""}}},
		{"add third", state.Add, []Box{{1, "one"}, {2, ""}, {3, ""}}},
		{"write third", func() (Snapshot, error) { return state.Write(3, "three") }, []Box{{1, "one"}, {2, ""}, {3, "three"}}},
		{"unchanged write", func() (Snapshot, error) { return state.Write(3, "three") }, []Box{{1, "one"}, {2, ""}, {3, "three"}}},
		{"remove first", func() (Snapshot, error) { return state.Remove(1) }, []Box{{2, ""}, {3, "three"}}},
		// The counter keeps going, so removed IDs are never handed out again.
		{"add after remove", state.Add, []Box{{2, ""}, {3, "three"}, {4, ""}}},
		{"empty write", func() (Snapshot, error) { return state.Write(3, "") }, []Box{{2, ""}, {3, ""}, {4, ""}}},
	}
	for i, step := range steps {
		want := Snapshot{Boxes: step.boxes, Revision: uint64(i + 1)}
		got, err := step.change()
		if err != nil || !equal(got, want) {
			t.Fatalf("%s = %+v, %v; want %+v, nil", step.name, got, err, want)
		}
		if got := state.Current(); !equal(got, want) {
			t.Fatalf("%s: Current = %+v, want %+v", step.name, got, want)
		}
	}
}

func TestSnapshotsAreCopies(t *testing.T) {
	state := New()
	snapshot, err := state.Write(1, "shared")
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Boxes[0].Text = "changed by caller"
	state.Current().Boxes[0].ID = 99
	if got := state.Current(); got.Boxes[0] != (Box{ID: 1, Text: "shared"}) {
		t.Fatalf("caller mutation leaked into state: %+v", got)
	}
}

func TestTextLimits(t *testing.T) {
	tests := []struct {
		name string
		text string
		err  error
	}{
		{"ASCII at limit", strings.Repeat("a", MaxTextBytes), nil},
		{"ASCII over limit", strings.Repeat("a", MaxTextBytes+1), ErrTooLarge},
		{"multibyte at limit", strings.Repeat("🙂", MaxTextBytes/4), nil},
		{"multibyte over limit", strings.Repeat("🙂", MaxTextBytes/4) + "a", ErrTooLarge},
		{"invalid UTF-8", "\xff", ErrInvalidUTF8},
		{"replacement character", "�", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := New()
			before, err := state.Write(1, "keep this")
			if err != nil {
				t.Fatal(err)
			}
			got, err := state.Write(1, tt.text)
			if tt.err != nil {
				wantRejected(t, state, before, got, err, tt.err)
				return
			}
			if err != nil || got.Boxes[0].Text != tt.text || got.Revision != before.Revision+1 {
				t.Fatalf("accepted write = revision %d, error %v", got.Revision, err)
			}
		})
	}
}

func TestBoxLimits(t *testing.T) {
	state := New()
	for range MaxBoxes - 1 {
		if _, err := state.Add(); err != nil {
			t.Fatal(err)
		}
	}
	full := state.Current()
	if len(full.Boxes) != MaxBoxes {
		t.Fatalf("boxes = %d, want %d", len(full.Boxes), MaxBoxes)
	}
	got, err := state.Add()
	wantRejected(t, state, full, got, err, ErrTooManyBoxes)

	for _, box := range full.Boxes[1:] {
		if _, err := state.Remove(box.ID); err != nil {
			t.Fatal(err)
		}
	}
	last := state.Current()
	got, err = state.Remove(last.Boxes[0].ID)
	wantRejected(t, state, last, got, err, ErrLastBox)

	// Removed and never-created IDs are both unknown, even when one box is left.
	for _, id := range []uint64{0, full.Boxes[1].ID, MaxBoxes + 1} {
		t.Run(fmt.Sprintf("unknown box %d", id), func(t *testing.T) {
			got, err := state.Write(id, "lost")
			wantRejected(t, state, last, got, err, ErrUnknownBox)
			got, err = state.Remove(id)
			wantRejected(t, state, last, got, err, ErrUnknownBox)
		})
	}

	// After rejections, the next change takes the very next revision.
	next, err := state.Add()
	if err != nil || next.Revision != last.Revision+1 {
		t.Fatalf("next revision = %d, error = %v; want %d", next.Revision, err, last.Revision+1)
	}
}

func TestConcurrentChangesAndReads(t *testing.T) {
	// More workers than MaxBoxes, so adds race against the limit and writes
	// race against removals by other workers.
	const workers, roundsPerWorker = 16, 32
	state := New()
	var wg sync.WaitGroup
	accepted := make(chan Snapshot, workers*roundsPerWorker*3)
	observed := make(chan Snapshot, workers*roundsPerWorker)
	start := make(chan struct{})
	for worker := range workers {
		wg.Go(func() {
			<-start
			var previous uint64
			check := func(snapshot Snapshot, err error, allowed ...error) bool {
				if err != nil {
					if !slices.ContainsFunc(allowed, func(e error) bool { return errors.Is(err, e) }) {
						t.Errorf("unexpected error: %v", err)
					}
					return false
				}
				if snapshot.Revision <= previous {
					t.Error("change returned a non-increasing revision")
				}
				if n := len(snapshot.Boxes); n < 1 || n > MaxBoxes {
					t.Errorf("snapshot holds %d boxes", n)
				}
				previous = snapshot.Revision
				accepted <- snapshot
				return true
			}
			for round := range roundsPerWorker {
				added, err := state.Add()
				if !check(added, err, ErrTooManyBoxes) {
					continue
				}
				id := added.Boxes[len(added.Boxes)-1].ID
				text := fmt.Sprintf("worker %d round %d", worker, round)
				written, err := state.Write(id, text)
				if check(written, err) && !slices.Contains(written.Boxes, Box{id, text}) {
					t.Error("write did not land in its box")
				}
				removed, err := state.Remove(id)
				check(removed, err)

				current := state.Current()
				if current.Revision < previous {
					t.Error("read rolled back an accepted revision")
				}
				observed <- current
			}
		})
	}
	close(start)
	wg.Wait()
	close(accepted)
	close(observed)

	byRevision := make(map[uint64]Snapshot)
	for snapshot := range accepted {
		if _, exists := byRevision[snapshot.Revision]; exists {
			t.Fatalf("duplicate revision %d", snapshot.Revision)
		}
		byRevision[snapshot.Revision] = snapshot
	}
	total := uint64(len(byRevision))
	for revision := uint64(1); revision <= total; revision++ {
		if _, exists := byRevision[revision]; !exists {
			t.Fatalf("missing revision %d", revision)
		}
	}
	for snapshot := range observed {
		if want := byRevision[snapshot.Revision]; !equal(snapshot, want) {
			t.Fatalf("read mixed boxes and revision: %+v, want %+v", snapshot, want)
		}
	}
	// Every worker removes what it added, so only the first box remains.
	final := state.Current()
	if !equal(final, byRevision[total]) || len(final.Boxes) != 1 || final.Boxes[0].ID != 1 {
		t.Fatalf("final snapshot = %+v, want the last accepted change with only box 1", final)
	}
}
