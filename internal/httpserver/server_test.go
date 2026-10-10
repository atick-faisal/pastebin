package httpserver

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
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

func readSnapshot(reader *bufio.Reader) (textstate.Snapshot, error) {
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return textstate.Snapshot{}, err
		}
		if strings.HasPrefix(line, "data: ") {
			var snapshot textstate.Snapshot
			err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &snapshot)
			return snapshot, err
		}
	}
}

func wantSnapshot(t *testing.T, reader *bufio.Reader, want textstate.Snapshot) {
	t.Helper()
	got, err := readSnapshot(reader)
	if err != nil || got != want {
		t.Fatalf("snapshot = %+v, %v; want %+v", got, err, want)
	}
}

func postText(client *http.Client, url, text string) (textstate.Snapshot, error) {
	response, err := client.Post(url+"/text", "text/plain; charset=utf-8", strings.NewReader(text))
	if err != nil {
		return textstate.Snapshot{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return textstate.Snapshot{}, fmt.Errorf("POST status: %s", response.Status)
	}
	var snapshot textstate.Snapshot
	err = json.NewDecoder(response.Body).Decode(&snapshot)
	return snapshot, err
}

func TestFiveSubscribersSenderDeliveryAndReconnect(t *testing.T) {
	handler := New()
	server, client := testServer(t, handler)
	var responses []*http.Response
	var readers []*bufio.Reader
	for range 5 {
		response, reader := openStream(t, client, server.URL)
		responses = append(responses, response)
		readers = append(readers, reader)
		wantSnapshot(t, reader, textstate.Snapshot{})
	}

	for i, text := range []string{"first\nsecond\r\n🙂\"\\", "other browser", ""} {
		// The sender's SSE connection uses this client too; it must get its echo.
		want := textstate.Snapshot{Text: text, Revision: uint64(i + 1)}
		got, err := postText(client, server.URL, text)
		if err != nil || got != want {
			t.Fatalf("POST = %+v, %v; want %+v", got, err, want)
		}
		for _, reader := range readers {
			wantSnapshot(t, reader, want)
		}
	}
	responses[0].Body.Close()
	waitSubscribers(t, handler, 4)
	latest, err := postText(client, server.URL, "changed while disconnected")
	if err != nil {
		t.Fatal(err)
	}
	for _, reader := range readers[1:] {
		wantSnapshot(t, reader, latest)
	}
	_, reconnected := openStream(t, client, server.URL)
	wantSnapshot(t, reconnected, latest)

	// A restarted process has a new revision baseline.
	fresh, freshClient := testServer(t, New())
	_, restarted := openStream(t, freshClient, fresh.URL)
	wantSnapshot(t, restarted, textstate.Snapshot{})
}

func TestConcurrentPostsPublishInRevisionOrder(t *testing.T) {
	handler := New()
	server, client := testServer(t, handler)
	_, reader := openStream(t, client, server.URL)
	wantSnapshot(t, reader, textstate.Snapshot{})
	const count = 32
	observed := make(chan []textstate.Snapshot, 1)
	readErrors := make(chan error, 1)
	go func() {
		var snapshots []textstate.Snapshot
		for {
			snapshot, err := readSnapshot(reader)
			if err != nil {
				readErrors <- err
				return
			}
			snapshots = append(snapshots, snapshot)
			if snapshot.Revision == count {
				observed <- snapshots
				return
			}
		}
	}()
	accepted := make(chan textstate.Snapshot, count)
	var wg sync.WaitGroup
	for i := range count {
		wg.Go(func() {
			text := fmt.Sprintf("writer %d", i)
			snapshot, err := postText(client, server.URL, text)
			if err != nil || snapshot.Text != text {
				t.Errorf("POST = %+v, %v; want text %q", snapshot, err, text)
				return
			}
			accepted <- snapshot
		})
	}
	wg.Wait()
	close(accepted)
	byRevision := make(map[uint64]textstate.Snapshot)
	for snapshot := range accepted {
		if _, duplicate := byRevision[snapshot.Revision]; duplicate {
			t.Fatalf("duplicate revision %d", snapshot.Revision)
		}
		byRevision[snapshot.Revision] = snapshot
	}
	if len(byRevision) != count {
		t.Fatalf("accepted %d writes, want %d", len(byRevision), count)
	}
	select {
	case snapshots := <-observed:
		var previous uint64
		for _, snapshot := range snapshots {
			if snapshot.Revision <= previous || snapshot != byRevision[snapshot.Revision] {
				t.Fatalf("out-of-order or mixed snapshot: %+v after %d", snapshot, previous)
			}
			previous = snapshot.Revision
		}
	case err := <-readErrors:
		t.Fatal(err)
	case <-time.After(5 * time.Second):
		t.Fatal("subscriber did not converge")
	}
}

func TestAtomicSubscriptionAndBoundedQueue(t *testing.T) {
	for range 100 {
		handler := New()
		done := make(chan struct{})
		go func() {
			handler.accept("racing write")
			close(done)
		}()
		initial, updates := handler.subscribe()
		<-done
		var snapshot textstate.Snapshot
		if err := json.Unmarshal(initial, &snapshot); err != nil {
			t.Fatal(err)
		}
		if snapshot.Revision == 0 {
			select {
			case data := <-updates:
				json.Unmarshal(data, &snapshot)
			default:
				t.Fatal("write fell into a gap between initial state and subscription")
			}
		}
		if snapshot != (textstate.Snapshot{Text: "racing write", Revision: 1}) {
			t.Fatalf("snapshot = %+v", snapshot)
		}
		for range 100 {
			if _, err := handler.accept("latest"); err != nil {
				t.Fatal(err)
			}
		}
		if len(updates) != 1 {
			t.Fatalf("pending queue size = %d, want 1", len(updates))
		}
		if err := json.Unmarshal(<-updates, &snapshot); err != nil {
			t.Fatal(err)
		}
		if snapshot != (textstate.Snapshot{Text: "latest", Revision: 101}) {
			t.Fatalf("coalesced snapshot = %+v", snapshot)
		}
		handler.unsubscribe(updates)
	}
}

func TestHTTPTextLimitsAndRejections(t *testing.T) {
	handler := New()
	server, client := testServer(t, handler)
	_, updates := handler.subscribe()
	defer handler.unsubscribe(updates)
	for _, tt := range []struct {
		name   string
		text   string
		status int
	}{
		{"at byte limit", strings.Repeat("🙂", textstate.MaxTextBytes/4), http.StatusOK},
		{"over byte limit", strings.Repeat("a", textstate.MaxTextBytes+1), http.StatusRequestEntityTooLarge},
		{"invalid UTF-8", "\xff", http.StatusBadRequest},
	} {
		t.Run(tt.name, func(t *testing.T) {
			before := handler.state.Current()
			request, err := http.NewRequest(http.MethodPost, server.URL+"/text", strings.NewReader(tt.text))
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
			if response.StatusCode != tt.status {
				t.Fatalf("status = %d, want %d", response.StatusCode, tt.status)
			}
			if tt.status == http.StatusOK {
				var got textstate.Snapshot
				if err := json.NewDecoder(response.Body).Decode(&got); err != nil {
					t.Fatal(err)
				}
				if got.Text != tt.text || got.Revision != before.Revision+1 {
					t.Fatal("at-limit text was not accepted intact")
				}
				<-updates
			} else {
				if handler.state.Current() != before || len(updates) != 0 {
					t.Fatal("rejected write changed state or broadcast an update")
				}
			}
		})
	}
	got, err := postText(client, server.URL, "valid after rejection")
	if err != nil || got.Revision != 2 {
		t.Fatalf("next write = %+v, %v; want revision 2", got, err)
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
	wantSnapshot(t, reader, textstate.Snapshot{})
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
	latest, err := postText(client, server.URL, "healthy client keeps working")
	if err != nil {
		t.Fatal(err)
	}
	wantSnapshot(t, reader, latest)
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
	wantSnapshot(t, reader, textstate.Snapshot{})
	// The empty line terminates the initial snapshot, then a heartbeat arrives
	// after the earlier write deadline would have expired.
	if line, err := reader.ReadString('\n'); err != nil || line != "\n" {
		t.Fatalf("SSE separator = %q, %v", line, err)
	}
	if line, err := reader.ReadString('\n'); err != nil || line != ": keepalive\n" {
		t.Fatalf("heartbeat = %q, %v", line, err)
	}
	latest, err := postText(client, server.URL, "still connected")
	if err != nil {
		t.Fatal(err)
	}
	wantSnapshot(t, reader, latest)
}

func TestRoutingAndBodyReadFailure(t *testing.T) {
	handler := New()
	for _, tt := range []struct {
		method string
		path   string
		status int
	}{
		{http.MethodGet, "/text", http.StatusMethodNotAllowed},
		{http.MethodPost, "/events", http.StatusMethodNotAllowed},
		{http.MethodGet, "/missing", http.StatusNotFound},
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(tt.method, tt.path, nil))
		if response.Code != tt.status {
			t.Fatalf("%s %s status = %d, want %d", tt.method, tt.path, response.Code, tt.status)
		}
	}
	request := httptest.NewRequest(http.MethodPost, "/text", nil)
	request.Body = io.NopCloser(failingReader{})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || handler.state.Current() != (textstate.Snapshot{}) {
		t.Fatal("body read failure was not rejected without changing state")
	}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }
