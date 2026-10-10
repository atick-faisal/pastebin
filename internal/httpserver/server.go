// Package httpserver exposes whole-text writes and authoritative SSE snapshots.
package httpserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"pastebin/internal/textstate"
)

// Server owns one shared text state. Construct it with New.
type Server struct {
	mu           sync.Mutex
	state        textstate.State
	current      []byte
	subscribers  map[chan []byte]struct{}
	mux          *http.ServeMux
	writeTimeout time.Duration
	heartbeat    time.Duration
}

// New returns an empty server at revision zero.
func New() *Server {
	s := &Server{
		current:      encodeSnapshot(textstate.Snapshot{}),
		subscribers:  make(map[chan []byte]struct{}),
		mux:          http.NewServeMux(),
		writeTimeout: 5 * time.Second,
		heartbeat:    15 * time.Second,
	}
	s.mux.HandleFunc("POST /text", s.replace)
	s.mux.HandleFunc("GET /events", s.events)
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

func encodeSnapshot(snapshot textstate.Snapshot) []byte {
	// This concrete string/integer payload cannot fail JSON encoding. Encoding
	// once per revision lets all subscribers share immutable snapshot bytes.
	data, _ := json.Marshal(struct {
		Text     string `json:"text"`
		Revision uint64 `json:"revision"`
	}{snapshot.Text, snapshot.Revision})
	return data
}

// accept serializes acceptance and publication together, so concurrent POSTs
// cannot publish revisions out of order.
func (s *Server) accept(text string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	snapshot, err := s.state.Replace(text)
	if err != nil {
		return nil, err
	}
	s.current = encodeSnapshot(snapshot)
	for updates := range s.subscribers {
		// A slow subscriber retains only the newest pending snapshot. Revisions
		// may be skipped, but whole-text snapshots still converge without replay.
		select {
		case <-updates:
		default:
		}
		updates <- s.current
	}
	return s.current, nil
}

func (s *Server) subscribe() ([]byte, chan []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	updates := make(chan []byte, 1)
	s.subscribers[updates] = struct{}{}
	// The initial snapshot is separate from the queue so it cannot be replaced
	// before delivery. The same lock covers subscribing and every publication.
	return s.current, updates
}

func (s *Server) unsubscribe(updates chan []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.subscribers, updates)
}

// replace accepts raw UTF-8 text and returns {"text": ..., "revision": ...}.
func (s *Server) replace(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, textstate.MaxTextBytes)
	defer r.Body.Close()
	text, err := io.ReadAll(r.Body)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			http.Error(w, textstate.ErrTooLarge.Error(), http.StatusRequestEntityTooLarge)
		} else {
			http.Error(w, "could not read text", http.StatusBadRequest)
		}
		return
	}
	data, err := s.accept(string(text))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(data)
}

// events always starts with a fresh snapshot, ignoring Last-Event-ID. Reconnects
// establish a new baseline, including when a new process starts at revision 0.
func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	controller := http.NewResponseController(w)
	if err := controller.SetWriteDeadline(time.Now().Add(s.writeTimeout)); err != nil {
		http.Error(w, "streaming is unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	initial, updates := s.subscribe()
	defer s.unsubscribe(updates)

	send := func(data []byte) error {
		if err := controller.SetWriteDeadline(time.Now().Add(s.writeTimeout)); err != nil {
			return err
		}
		var err error
		if data == nil {
			_, err = io.WriteString(w, ": keepalive\n\n")
		} else {
			_, err = fmt.Fprintf(w, "data: %s\n\n", data)
		}
		if err != nil {
			return err
		}
		if err := controller.Flush(); err != nil {
			return err
		}
		// Idle streams outlive the write timeout. Clear it after a successful
		// flush so the next heartbeat can establish its own deadline.
		return controller.SetWriteDeadline(time.Time{})
	}
	if err := send(initial); err != nil {
		return
	}
	ticker := time.NewTicker(s.heartbeat)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case data := <-updates:
			if err := send(data); err != nil {
				return
			}
		case <-ticker.C:
			if err := send(nil); err != nil {
				return
			}
		}
	}
}
