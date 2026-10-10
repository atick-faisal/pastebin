package textstate

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

func TestReplacementOrder(t *testing.T) {
	var state State
	if got := state.Current(); got != (Snapshot{}) {
		t.Fatalf("initial snapshot = %+v, want empty revision 0", got)
	}

	for i, text := range []string{"first", "second", "second", ""} {
		want := Snapshot{Text: text, Revision: uint64(i + 1)}
		got, err := state.Replace(text)
		if err != nil || got != want {
			t.Fatalf("Replace = %+v, %v; want %+v, nil", got, err, want)
		}
		if got := state.Current(); got != want {
			t.Fatalf("Current = %+v, want %+v", got, want)
		}
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
		{"replacement character", "\ufffd", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var state State
			before, err := state.Replace("keep this")
			if err != nil {
				t.Fatal(err)
			}
			got, err := state.Replace(tt.text)
			if !errors.Is(err, tt.err) {
				t.Fatalf("Replace error = %v, want %v", err, tt.err)
			}
			if tt.err != nil {
				if got != (Snapshot{}) {
					t.Fatal("rejected write returned a snapshot")
				}
				if state.Current() != before {
					t.Fatal("rejected write changed shared state")
				}
				// A rejection must not consume a revision either.
				next, err := state.Replace("after rejection")
				if err != nil || next.Revision != before.Revision+1 {
					t.Fatalf("next revision = %d, error = %v", next.Revision, err)
				}
				return
			}
			if got.Text != tt.text || got.Revision != before.Revision+1 || state.Current() != got {
				t.Fatal("accepted write did not become the next snapshot")
			}
		})
	}
}

func TestConcurrentReplacementsAndReads(t *testing.T) {
	const workers, writesPerWorker = 32, 32
	const total = workers * writesPerWorker
	var state State
	var wg sync.WaitGroup
	accepted := make(chan Snapshot, total)
	observed := make(chan Snapshot, total)
	start := make(chan struct{})
	for worker := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			var previousRevision uint64
			for write := range writesPerWorker {
				text := fmt.Sprintf("worker %d write %d", worker, write)
				snapshot, err := state.Replace(text)
				if err != nil {
					t.Error(err)
					return
				}
				if snapshot.Text != text || snapshot.Revision <= previousRevision {
					t.Error("replacement returned wrong text or non-increasing revision")
				}
				accepted <- snapshot
				current := state.Current()
				if current.Revision < snapshot.Revision {
					t.Error("read rolled back an accepted revision")
				}
				observed <- current
				previousRevision = current.Revision
			}
		}()
	}
	close(start)
	wg.Wait()
	close(accepted)
	close(observed)

	byRevision := make(map[uint64]Snapshot, total)
	for snapshot := range accepted {
		if _, exists := byRevision[snapshot.Revision]; exists {
			t.Fatalf("duplicate revision %d", snapshot.Revision)
		}
		byRevision[snapshot.Revision] = snapshot
	}
	for revision := uint64(1); revision <= total; revision++ {
		if _, exists := byRevision[revision]; !exists {
			t.Fatalf("missing revision %d", revision)
		}
	}
	for snapshot := range observed {
		if want := byRevision[snapshot.Revision]; snapshot != want {
			t.Fatalf("read mixed text and revision: %+v, want %+v", snapshot, want)
		}
	}
	if got := state.Current(); got != byRevision[total] {
		t.Fatalf("final snapshot = %+v, want last accepted write %+v", got, byRevision[total])
	}
}
