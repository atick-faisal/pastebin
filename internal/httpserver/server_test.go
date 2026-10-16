package httpserver

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"pastebin/internal/textstate"
)

func testServer(t *testing.T, handler *Server) (*httptest.Server, *http.Client) {
	t.Helper()
	server := httptest.NewServer(handler)
	client := server.Client()
	client.Timeout = 5 * time.Second
	t.Cleanup(server.Close)
	t.Cleanup(client.CloseIdleConnections)
	return server, client
}

func openStream(t *testing.T, client *http.Client, url string) (*http.Response, *bufio.Reader) {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, url+"/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	// Even a stale reconnect hint must receive current state, without replay.
	request.Header.Set("Last-Event-ID", "999999")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { response.Body.Close() })
	if response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("SSE response: %s, %v", response.Status, response.Header)
	}
	if response.Header.Get("Cache-Control") != "no-cache" || response.Header.Get("X-Accel-Buffering") != "no" {
		t.Fatal("SSE cache/proxy buffering headers missing")
	}
	return response, bufio.NewReader(response.Body)
}

func snapshotMessage(revision uint64, boxes ...wireBox) message {
	return message{Type: "snapshot", Revision: revision, Boxes: boxes}
}

func textMessage(revision, id uint64, text string) message {
	return message{Type: "text", Revision: revision, Box: &wireBox{id, text}}
}

func listMessage(revision uint64, ids ...uint64) message {
	return message{Type: "boxes", Revision: revision, IDs: ids}
}

func decode(t *testing.T, data []byte) message {
	t.Helper()
	var m message
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("decode %q: %v", data, err)
	}
	return m
}

func readMessage(reader *bufio.Reader) (message, error) {
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return message{}, err
		}
		if strings.HasPrefix(line, "data: ") {
			var m message
			err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &m)
			return m, err
		}
	}
}

func wantMessage(t *testing.T, reader *bufio.Reader, want message) {
	t.Helper()
	got, err := readMessage(reader)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("message = %+v, %v; want %+v", got, err, want)
	}
}

// request sends a change and returns its status and raw reply.
func request(t *testing.T, client *http.Client, method, url, body string) (int, []byte) {
	t.Helper()
	request, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	// Chunked requests cannot bypass the bound via missing Content-Length.
	request.ContentLength = -1
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, data
}

// send makes a change that must be accepted and returns its reply. It
// reports errors instead of failing, so test goroutines can call it.
func send(client *http.Client, method, url, body string) (message, error) {
	request, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		return message{}, err
	}
	response, err := client.Do(request)
	if err != nil {
		return message{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return message{}, fmt.Errorf("%s %s status: %s", method, url, response.Status)
	}
	var m message
	err = json.NewDecoder(response.Body).Decode(&m)
	return m, err
}

func change(t *testing.T, client *http.Client, method, url, body string) message {
	t.Helper()
	m, err := send(client, method, url, body)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// replica applies stream messages the way a browser would, so tests can check
// that a subscriber converges on the server's state.
type replica struct {
	revision uint64
	boxes    []wireBox
}

func (r *replica) apply(m message) error {
	if r.boxes == nil && m.Type != "snapshot" {
		return fmt.Errorf("%s message before the first snapshot", m.Type)
	}
	if r.boxes != nil && m.Revision <= r.revision {
		return fmt.Errorf("revision %d after %d", m.Revision, r.revision)
	}
	switch m.Type {
	case "snapshot":
		r.boxes = m.Boxes
	case "boxes":
		next := make([]wireBox, len(m.IDs))
		for i, id := range m.IDs {
			next[i] = wireBox{ID: id}
			if j := r.find(id); j >= 0 {
				next[i].Text = r.boxes[j].Text
			}
		}
		r.boxes = next
	case "text":
		j := r.find(m.Box.ID)
		if j < 0 {
			return fmt.Errorf("text for unknown box %d at revision %d", m.Box.ID, m.Revision)
		}
		r.boxes[j].Text = m.Box.Text
	default:
		return fmt.Errorf("unknown message type %q", m.Type)
	}
	r.revision = m.Revision
	return nil
}

func (r *replica) find(id uint64) int {
	return slices.IndexFunc(r.boxes, func(box wireBox) bool { return box.ID == id })
}

func wantConverged(t *testing.T, r *replica, handler *Server) {
	t.Helper()
	want := decode(t, snapshotData(handler.state.Current()))
	if r.revision != want.Revision || !reflect.DeepEqual(r.boxes, want.Boxes) {
		t.Fatalf("replica = %d %+v; server = %d %+v", r.revision, r.boxes, want.Revision, want.Boxes)
	}
}

func TestFiveSubscribersConvergeAndReconnect(t *testing.T) {
	handler := New()
	server, client := testServer(t, handler)
	var responses []*http.Response
	var readers []*bufio.Reader
	for range 5 {
		response, reader := openStream(t, client, server.URL)
		responses = append(responses, response)
		readers = append(readers, reader)
		wantMessage(t, reader, snapshotMessage(0, wireBox{ID: 1}))
	}

	for _, step := range []struct {
		method, path, body string
		want               message
	}{
		{http.MethodPost, "/boxes", "", listMessage(1, 1, 2)},
		{http.MethodPut, "/boxes/2", "first\nsecond\r\n🙂\"\\", textMessage(2, 2, "first\nsecond\r\n🙂\"\\")},
		{http.MethodPut, "/boxes/1", "other browser", textMessage(3, 1, "other browser")},
		{http.MethodDelete, "/boxes/1", "", listMessage(4, 2)},
		{http.MethodPut, "/boxes/2", "", textMessage(5, 2, "")},
	} {
		// The sender's SSE connection uses this client too; it must get its echo.
		got := change(t, client, step.method, server.URL+step.path, step.body)
		if !reflect.DeepEqual(got, step.want) {
			t.Fatalf("%s %s reply = %+v; want %+v", step.method, step.path, got, step.want)
		}
		for _, reader := range readers {
			wantMessage(t, reader, step.want)
		}
	}
	responses[0].Body.Close()
	waitSubscribers(t, handler, 4)
	latest := change(t, client, http.MethodPut, server.URL+"/boxes/2", "changed while disconnected")
	for _, reader := range readers[1:] {
		wantMessage(t, reader, latest)
	}
	_, reconnected := openStream(t, client, server.URL)
	wantMessage(t, reconnected, snapshotMessage(6, wireBox{2, "changed while disconnected"}))

	// A restarted process has a new revision baseline.
	fresh, freshClient := testServer(t, New())
	_, restarted := openStream(t, freshClient, fresh.URL)
	wantMessage(t, restarted, snapshotMessage(0, wireBox{ID: 1}))
}

func TestConcurrentChangesPublishInRevisionOrder(t *testing.T) {
	handler := New()
	server, client := testServer(t, handler)
	_, reader := openStream(t, client, server.URL)
	const writes, adds = 32, textstate.MaxBoxes - 1
	const count = writes + adds
	observed := make(chan []message, 1)
	readErrors := make(chan error, 1)
	go func() {
		var messages []message
		for {
			m, err := readMessage(reader)
			if err != nil {
				readErrors <- err
				return
			}
			messages = append(messages, m)
			if m.Revision == count {
				observed <- messages
				return
			}
		}
	}()
	replies := make(chan message, count)
	var wg sync.WaitGroup
	accept := func(method, path, body string) {
		reply, err := send(client, method, server.URL+path, body)
		if err != nil {
			t.Error(err)
			return
		}
		replies <- reply
	}
	for i := range writes {
		wg.Go(func() { accept(http.MethodPut, "/boxes/1", fmt.Sprintf("writer %d", i)) })
	}
	for range adds {
		wg.Go(func() { accept(http.MethodPost, "/boxes", "") })
	}
	wg.Wait()
	close(replies)
	byRevision := make(map[uint64]message)
	for reply := range replies {
		if _, duplicate := byRevision[reply.Revision]; duplicate {
			t.Fatalf("duplicate revision %d", reply.Revision)
		}
		byRevision[reply.Revision] = reply
	}
	if len(byRevision) != count {
		t.Fatalf("accepted %d changes, want %d", len(byRevision), count)
	}
	select {
	case messages := <-observed:
		var r replica
		for _, m := range messages {
			if err := r.apply(m); err != nil {
				t.Fatal(err)
			}
			// A slow reader may be resynced; every other message is the
			// accepted change itself.
			if m.Type != "snapshot" && !reflect.DeepEqual(m, byRevision[m.Revision]) {
				t.Fatalf("stream sent %+v; change replied %+v", m, byRevision[m.Revision])
			}
		}
		wantConverged(t, &r, handler)
	case err := <-readErrors:
		t.Fatal(err)
	case <-time.After(5 * time.Second):
		t.Fatal("subscriber did not converge")
	}
}

func TestSlowSubscriberCoalescesPerBoxAndResyncs(t *testing.T) {
	handler := New()
	sub := handler.subscribe()
	defer handler.unsubscribe(sub)
	call := func(method, path, body string) {
		t.Helper()
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(method, path, strings.NewReader(body)))
		if response.Code != http.StatusOK {
			t.Fatalf("%s %s status = %d: %s", method, path, response.Code, response.Body)
		}
	}
	var r replica
	deliver := func() []message {
		t.Helper()
		var messages []message
		snapshot, events := handler.take(sub)
		if snapshot != nil {
			messages = append(messages, decode(t, snapshotData(*snapshot)))
		}
		for _, e := range events {
			messages = append(messages, decode(t, e.data))
		}
		for _, m := range messages {
			if err := r.apply(m); err != nil {
				t.Fatal(err)
			}
		}
		wantConverged(t, &r, handler)
		return messages
	}

	// A change made before the first delivery is in the initial snapshot and
	// is not sent again.
	call(http.MethodPut, "/boxes/1", "before the first delivery")
	if got := deliver(); !reflect.DeepEqual(got, []message{snapshotMessage(1, wireBox{1, "before the first delivery"})}) {
		t.Fatalf("first delivery = %+v", got)
	}

	// However many writes it misses, a backlog keeps one event per box.
	call(http.MethodPost, "/boxes", "")
	for i := range 100 {
		call(http.MethodPut, "/boxes/1", fmt.Sprintf("one %d", i))
		call(http.MethodPut, "/boxes/2", fmt.Sprintf("two %d", i))
	}
	want := []message{listMessage(2, 1, 2), textMessage(201, 1, "one 99"), textMessage(202, 2, "two 99")}
	if got := deliver(); !reflect.DeepEqual(got, want) {
		t.Fatalf("coalesced delivery = %+v; want %+v", got, want)
	}

	// Coalesced texts stay in revision order around one box list, so an added
	// box arrives before its first text.
	call(http.MethodPut, "/boxes/1", "before add")
	call(http.MethodPost, "/boxes", "")
	call(http.MethodPut, "/boxes/3", "new box")
	call(http.MethodPut, "/boxes/1", "after add")
	want = []message{listMessage(204, 1, 2, 3), textMessage(205, 3, "new box"), textMessage(206, 1, "after add")}
	if got := deliver(); !reflect.DeepEqual(got, want) {
		t.Fatalf("delivery around an add = %+v; want %+v", got, want)
	}

	// A second pending box list would move the first one past box 4's text,
	// so the backlog is replaced by the current snapshot.
	call(http.MethodPost, "/boxes", "")
	call(http.MethodPut, "/boxes/4", "short-lived")
	call(http.MethodDelete, "/boxes/4", "")
	if got := deliver(); len(got) != 1 || got[0].Type != "snapshot" || got[0].Revision != 209 {
		t.Fatalf("resync delivery = %+v", got)
	}
}

func TestRejectedChangesLeaveStateUnchanged(t *testing.T) {
	handler := New()
	server, client := testServer(t, handler)
	sub := handler.subscribe()
	defer handler.unsubscribe(sub)
	handler.take(sub)
	check := func(t *testing.T, method, path, body string, status int) {
		t.Helper()
		before := handler.state.Current().Revision
		got, reply := request(t, client, method, server.URL+path, body)
		if got != status {
			t.Fatalf("%s %s status = %d, want %d: %s", method, path, got, status, reply)
		}
		_, events := handler.take(sub)
		revision := handler.state.Current().Revision
		if status == http.StatusOK {
			if revision != before+1 || len(events) != 1 || !bytes.Equal(events[0].data, reply) {
				t.Fatal("accepted change was not published as its reply")
			}
		} else if revision != before || len(events) != 0 {
			t.Fatal("rejected change changed state or published an event")
		}
	}
	atLimit := strings.Repeat("🙂", textstate.MaxTextBytes/4)
	for _, tt := range []struct {
		name, method, path, body string
		status                   int
	}{
		{"text at byte limit", http.MethodPut, "/boxes/1", atLimit, http.StatusOK},
		{"text over byte limit", http.MethodPut, "/boxes/1", strings.Repeat("a", textstate.MaxTextBytes+1), http.StatusRequestEntityTooLarge},
		{"invalid UTF-8", http.MethodPut, "/boxes/1", "\xff", http.StatusBadRequest},
		{"malformed box ID", http.MethodPut, "/boxes/one", "text", http.StatusNotFound},
		{"unknown box", http.MethodPut, "/boxes/99", "text", http.StatusNotFound},
		{"remove last box", http.MethodDelete, "/boxes/1", "", http.StatusConflict},
		{"remove unknown box", http.MethodDelete, "/boxes/99", "", http.StatusNotFound},
		{"add box", http.MethodPost, "/boxes", "", http.StatusOK},
		{"remove added box", http.MethodDelete, "/boxes/2", "", http.StatusOK},
		{"write to removed box", http.MethodPut, "/boxes/2", "late draft", http.StatusNotFound},
		{"remove removed box", http.MethodDelete, "/boxes/2", "", http.StatusNotFound},
	} {
		t.Run(tt.name, func(t *testing.T) { check(t, tt.method, tt.path, tt.body, tt.status) })
	}
	t.Run("box limit", func(t *testing.T) {
		for range textstate.MaxBoxes - 1 {
			check(t, http.MethodPost, "/boxes", "", http.StatusOK)
		}
		check(t, http.MethodPost, "/boxes", "", http.StatusConflict)
	})
	if got := handler.state.Current().Boxes[0].Text; got != atLimit {
		t.Fatal("at-limit text was not kept intact")
	}
}

func waitSubscribers(t *testing.T, handler *Server, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		handler.mu.Lock()
		got := len(handler.subscribers)
		handler.mu.Unlock()
		if got == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("subscribers = %d, want %d", got, want)
		}
		time.Sleep(time.Millisecond)
	}
}

// stalledWriter models a socket blocked until its write deadline expires.
type stalledWriter struct {
	header   http.Header
	deadline time.Time
	started  chan struct{}
}

func (w *stalledWriter) Header() http.Header { return w.header }
func (w *stalledWriter) WriteHeader(int)     {}
func (w *stalledWriter) Flush()              {}
func (w *stalledWriter) SetWriteDeadline(deadline time.Time) error {
	w.deadline = deadline
	return nil
}
func (w *stalledWriter) Write([]byte) (int, error) {
	close(w.started)
	<-time.After(time.Until(w.deadline))
	return 0, os.ErrDeadlineExceeded
}

func TestBlockedWriteDoesNotBlockOtherClientsAndCleansUp(t *testing.T) {
	handler := New()
	handler.writeTimeout = 100 * time.Millisecond
	server, client := testServer(t, handler)
	response, reader := openStream(t, client, server.URL)
	wantMessage(t, reader, snapshotMessage(0, wireBox{ID: 1}))
	writer := &stalledWriter{header: make(http.Header), started: make(chan struct{})}
	done := make(chan struct{})
	go func() {
		handler.ServeHTTP(writer, httptest.NewRequest(http.MethodGet, "/events", nil))
		close(done)
	}()
	select {
	case <-writer.started:
	case <-time.After(2 * time.Second):
		t.Fatal("stalled stream did not start")
	}
	latest := change(t, client, http.MethodPut, server.URL+"/boxes/1", "healthy client keeps working")
	wantMessage(t, reader, latest)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("write deadline did not release stalled handler")
	}
	waitSubscribers(t, handler, 1)
	response.Body.Close()
	waitSubscribers(t, handler, 0)
}

func TestIdleStreamSurvivesWriteTimeoutAndHeartbeats(t *testing.T) {
	handler := New()
	handler.writeTimeout = 10 * time.Millisecond
	handler.heartbeat = 40 * time.Millisecond
	server, client := testServer(t, handler)
	_, reader := openStream(t, client, server.URL)
	wantMessage(t, reader, snapshotMessage(0, wireBox{ID: 1}))
	// The empty line terminates the initial snapshot, then a heartbeat arrives
	// after the earlier write deadline would have expired.
	if line, err := reader.ReadString('\n'); err != nil || line != "\n" {
		t.Fatalf("SSE separator = %q, %v", line, err)
	}
	if line, err := reader.ReadString('\n'); err != nil || line != ": keepalive\n" {
		t.Fatalf("heartbeat = %q, %v", line, err)
	}
	latest := change(t, client, http.MethodPut, server.URL+"/boxes/1", "still connected")
	wantMessage(t, reader, latest)
}

func TestRoutingAndBodyReadFailure(t *testing.T) {
	handler := New()
	for _, tt := range []struct {
		method string
		path   string
		status int
	}{
		{http.MethodPost, "/text", http.StatusNotFound},
		{http.MethodGet, "/boxes", http.StatusMethodNotAllowed},
		{http.MethodGet, "/boxes/1", http.StatusMethodNotAllowed},
		{http.MethodPost, "/events", http.StatusMethodNotAllowed},
		{http.MethodGet, "/missing", http.StatusNotFound},
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(tt.method, tt.path, nil))
		if response.Code != tt.status {
			t.Fatalf("%s %s status = %d, want %d", tt.method, tt.path, response.Code, tt.status)
		}
	}
	request := httptest.NewRequest(http.MethodPut, "/boxes/1", nil)
	request.Body = io.NopCloser(failingReader{})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || handler.state.Current().Revision != 0 {
		t.Fatal("body read failure was not rejected without changing state")
	}
}

func TestEmbeddedBrowserAssets(t *testing.T) {
	handler := New()
	for _, tt := range []struct {
		path        string
		contentType string
		contains    string
	}{
		{"/", "text/html; charset=utf-8", `<script src="/app.js" defer></script>`},
		{"/app.js", "text/javascript; charset=utf-8", `new EventSource("/events")`},
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, tt.path, nil))
		if response.Code != http.StatusOK || response.Header().Get("Content-Type") != tt.contentType {
			t.Fatalf("GET %s: status %d, headers %v", tt.path, response.Code, response.Header())
		}
		if response.Header().Get("Cache-Control") != "no-store" || !strings.Contains(response.Body.String(), tt.contains) {
			t.Fatalf("GET %s: missing cache policy or asset content", tt.path)
		}
	}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }
