// Package httpserver exposes box changes and an ordered SSE stream of them.
package httpserver

import (
	"cmp"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"sync"
	"time"

	"pastebin/internal/textstate"
)

//go:embed web/index.html web/app.js
var assets embed.FS

// Server owns one shared box state. Construct it with New.
type Server struct {
	// mu covers every change together with its publication, and every
	// subscription, so streams see changes in the order they were accepted.
	mu           sync.Mutex
	state        *textstate.State
	subscribers  map[*subscriber]struct{}
	mux          *http.ServeMux
	writeTimeout time.Duration
	heartbeat    time.Duration
}

// New returns a server with one empty box at revision zero.
func New() *Server {
	s := &Server{
		state:        textstate.New(),
		subscribers:  make(map[*subscriber]struct{}),
		mux:          http.NewServeMux(),
		writeTimeout: 5 * time.Second,
		heartbeat:    15 * time.Second,
	}
	s.mux.HandleFunc("POST /boxes", s.add)
	s.mux.HandleFunc("DELETE /boxes/{id}", s.remove)
	s.mux.HandleFunc("PUT /boxes/{id}", s.write)
	s.mux.HandleFunc("GET /events", s.events)
	for path, asset := range map[string]string{"/{$}": "web/index.html", "/app.js": "web/app.js"} {
		s.mux.HandleFunc("GET "+path, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			if asset == "web/index.html" {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
			} else {
				w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			}
			data, _ := assets.ReadFile(asset)
			w.Write(data)
		})
	}
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

// message is the JSON of every SSE event and change reply. Type is "snapshot"
// (every box), "text" (one box's new text) or "boxes" (the box IDs in order
// after an add or remove; an added box is empty). One revision orders all three.
type message struct {
	Type     string    `json:"type"`
	Revision uint64    `json:"revision"`
	Box      *wireBox  `json:"box,omitempty"`
	IDs      []uint64  `json:"ids,omitempty"`
	Boxes    []wireBox `json:"boxes,omitempty"`
}

type wireBox struct {
	ID   uint64 `json:"id"`
	Text string `json:"text"`
}

// event is one accepted change, encoded once and shared by every subscriber.
type event struct {
	box      uint64 // the written box's ID, or listKey for an add or remove
	revision uint64
	data     []byte
}

// listKey marks box-list events in a backlog; box IDs start at 1.
const listKey = 0

func encode(m message) []byte {
	// These string and integer payloads cannot fail JSON encoding.
	data, _ := json.Marshal(m)
	return data
}

func snapshotData(snapshot textstate.Snapshot) []byte {
	boxes := make([]wireBox, len(snapshot.Boxes))
	for i, box := range snapshot.Boxes {
		boxes[i] = wireBox(box)
	}
	return encode(message{Type: "snapshot", Revision: snapshot.Revision, Boxes: boxes})
}

func listEvent(snapshot textstate.Snapshot) event {
	ids := make([]uint64, len(snapshot.Boxes))
	for i, box := range snapshot.Boxes {
		ids[i] = box.ID
	}
	return event{listKey, snapshot.Revision, encode(message{Type: "boxes", Revision: snapshot.Revision, IDs: ids})}
}

func textEvent(snapshot textstate.Snapshot, id uint64) event {
	i := slices.IndexFunc(snapshot.Boxes, func(box textstate.Box) bool { return box.ID == id })
	box := wireBox(snapshot.Boxes[i])
	return event{id, snapshot.Revision, encode(message{Type: "text", Revision: snapshot.Revision, Box: &box})}
}

// subscriber is one stream's undelivered events, guarded by Server.mu.
type subscriber struct {
	ready   chan struct{}    // holds one wakeup while there is something to deliver
	pending map[uint64]event // the newest undelivered event per box, keyed by box ID or listKey
	resync  bool             // deliver the current snapshot instead of pending
}

// queue keeps a slow stream's backlog bounded by the box limit. Coalescing a
// box's text only moves it to a later revision, which is safe: no box is
// written before it is added or after it is removed. Coalescing a box list
// could move an add past that box's first text, so a second pending list
// falls back to a full resync instead.
func (sub *subscriber) queue(e event) {
	if _, behind := sub.pending[listKey]; behind && e.box == listKey {
		sub.resync = true
		clear(sub.pending)
	}
	if !sub.resync {
		sub.pending[e.box] = e
	}
	select {
	case sub.ready <- struct{}{}:
	default:
	}
}

// change applies one state change and publishes its event under one lock, so
// concurrent requests cannot publish revisions out of order.
func (s *Server) change(apply func() (textstate.Snapshot, error), toEvent func(textstate.Snapshot) event) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	snapshot, err := apply()
	if err != nil {
		return nil, err
	}
	e := toEvent(snapshot)
	for sub := range s.subscribers {
		sub.queue(e)
	}
	return e.data, nil
}

// subscribe starts a stream that first needs a full snapshot. Taking that
// snapshot later, under the same lock as publication, leaves no gap between it
// and the stream's first queued change.
func (s *Server) subscribe() *subscriber {
	s.mu.Lock()
	defer s.mu.Unlock()
	sub := &subscriber{ready: make(chan struct{}, 1), pending: make(map[uint64]event), resync: true}
	sub.ready <- struct{}{}
	s.subscribers[sub] = struct{}{}
	return sub
}

func (s *Server) unsubscribe(sub *subscriber) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.subscribers, sub)
}

// take empties a backlog in revision order. On a resync it returns the
// current snapshot instead, which already includes every dropped event; the
// caller encodes it outside the lock, since it can hold every box's text.
func (s *Server) take(sub *subscriber) (*textstate.Snapshot, []event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sub.resync {
		sub.resync = false
		snapshot := s.state.Current()
		return &snapshot, nil
	}
	events := slices.SortedFunc(maps.Values(sub.pending), func(a, b event) int {
		return cmp.Compare(a.revision, b.revision)
	})
	clear(sub.pending)
	return nil, events
}

// add appends an empty box and replies with the "boxes" message.
func (s *Server) add(w http.ResponseWriter, r *http.Request) {
	data, err := s.change(s.state.Add, listEvent)
	respond(w, data, err)
}

// remove deletes a box and replies with the "boxes" message.
func (s *Server) remove(w http.ResponseWriter, r *http.Request) {
	id, err := boxID(r)
	if err != nil {
		respond(w, nil, err)
		return
	}
	data, err := s.change(func() (textstate.Snapshot, error) { return s.state.Remove(id) }, listEvent)
	respond(w, data, err)
}

// write replaces one box's text with the raw UTF-8 body and replies with the
// "text" message.
func (s *Server) write(w http.ResponseWriter, r *http.Request) {
	id, err := boxID(r)
	if err != nil {
		respond(w, nil, err)
		return
	}
	text, err := readText(w, r)
	if err != nil {
		respond(w, nil, err)
		return
	}
	data, err := s.change(
		func() (textstate.Snapshot, error) { return s.state.Write(id, text) },
		func(snapshot textstate.Snapshot) event { return textEvent(snapshot, id) },
	)
	respond(w, data, err)
}

// boxID parses the path ID. A malformed ID names no box, so it is unknown too.
func boxID(r *http.Request) (uint64, error) {
	id, err := strconv.ParseUint(r.PathValue("id"), 10, 64)
	if err != nil {
		return 0, textstate.ErrUnknownBox
	}
	return id, nil
}

var errUnreadable = errors.New("could not read text")

func readText(w http.ResponseWriter, r *http.Request) (string, error) {
	r.Body = http.MaxBytesReader(w, r.Body, textstate.MaxTextBytes)
	defer r.Body.Close()
	text, err := io.ReadAll(r.Body)
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		return "", textstate.ErrTooLarge
	}
	if err != nil {
		return "", errUnreadable
	}
	return string(text), nil
}

func respond(w http.ResponseWriter, data []byte, err error) {
	if err != nil {
		http.Error(w, err.Error(), status(err))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(data)
}

func status(err error) int {
	switch {
	case errors.Is(err, textstate.ErrUnknownBox):
		// Includes writes to a box another browser removed; the writer drops it.
		return http.StatusNotFound
	case errors.Is(err, textstate.ErrTooManyBoxes), errors.Is(err, textstate.ErrLastBox):
		return http.StatusConflict
	case errors.Is(err, textstate.ErrTooLarge):
		return http.StatusRequestEntityTooLarge
	default:
		return http.StatusBadRequest
	}
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
	sub := s.subscribe()
	defer s.unsubscribe(sub)

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
	deliver := func() error {
		snapshot, events := s.take(sub)
		if snapshot != nil {
			return send(snapshotData(*snapshot))
		}
		for _, e := range events {
			if err := send(e.data); err != nil {
				return err
			}
		}
		return nil
	}
	ticker := time.NewTicker(s.heartbeat)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-sub.ready:
			if err := deliver(); err != nil {
				return
			}
		case <-ticker.C:
			if err := send(nil); err != nil {
				return
			}
		}
	}
}
