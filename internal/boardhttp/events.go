package boardhttp

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/r13v/peer/internal/board"
)

const (
	pollEvery = 500 * time.Millisecond
	pingEvery = 15 * time.Second
)

// boardEvent is the data of an SSE "board" event.
type boardEvent struct {
	Revision int64  `json:"revision"`
	Hash     string `json:"hash"`
	External bool   `json:"external"`
}

// hub keeps one poller per watched board, shared by its subscribers.
type hub struct {
	mu       sync.Mutex
	watchers map[string]*watcher
}

type watcher struct {
	subs map[chan boardEvent]struct{}
	stop chan struct{}
}

func newHub() *hub { return &hub{watchers: map[string]*watcher{}} }

// join subscribes to dir's changes; first is the state the subscriber was
// sent, which starts a new poller. leave ends the subscription and stops
// the poller after its last subscriber.
func (h *hub) join(dir string, first boardEvent) (events chan boardEvent, leave func()) {
	events = make(chan boardEvent, 1)
	h.mu.Lock()
	defer h.mu.Unlock()
	w := h.watchers[dir]
	if w == nil {
		w = &watcher{subs: map[chan boardEvent]struct{}{}, stop: make(chan struct{})}
		h.watchers[dir] = w
		go h.poll(dir, w, first)
	}
	w.subs[events] = struct{}{}

	leave = func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		delete(w.subs, events)
		if len(w.subs) == 0 {
			close(w.stop)
			delete(h.watchers, dir)
		}
	}

	return events, leave
}

// poll sends every new hash of board.json. A hash that changed while the
// revision did not grow means a write outside peer, such as a git
// checkout or an editor.
func (h *hub) poll(dir string, w *watcher, last boardEvent) {
	t := time.NewTicker(pollEvery)
	defer t.Stop()
	for {
		select {
		case <-w.stop:
			return
		case <-t.C:
		}
		ev := readEvent(dir, last.Revision)
		if ev.Hash == last.Hash {
			continue
		}
		ev.External = ev.Revision <= last.Revision
		last = ev
		h.mu.Lock()
		for c := range w.subs {
			latest(c, ev)
		}
		h.mu.Unlock()
	}
}

// latest puts ev in c, dropping an older event the subscriber has not
// taken yet. The poller is the only sender, so the second send never
// blocks.
func latest(c chan boardEvent, ev boardEvent) {
	select {
	case c <- ev:
	default:
		select {
		case <-c:
		default:
		}
		c <- ev
	}
}

// readEvent reads the revision and hash of board.json without full
// validation: the tab validates by reloading the snapshot. A missing file
// has the empty hash, and a file without a readable revision keeps
// lastRev, so both still reach the tab as external changes and it shows
// the error instead of a stale board.
func readEvent(dir string, lastRev int64) boardEvent {
	data, err := os.ReadFile(filepath.Join(dir, "board.json"))
	if err != nil {
		return boardEvent{Revision: lastRev}
	}
	var head struct {
		Revision *int64 `json:"revision"`
	}
	ev := boardEvent{Revision: lastRev, Hash: board.Hash(data)}
	if json.Unmarshal(data, &head) == nil && head.Revision != nil {
		ev.Revision = *head.Revision
	}

	return ev
}

func (s *Server) subscribe(w http.ResponseWriter, r *http.Request) {
	dir, err := board.DecodeKey(r.PathValue("key"))
	if err != nil {
		writeError(w, err)
		return
	}
	first := readEvent(dir, 0)
	events, leave := s.events.join(dir, first)
	defer leave()

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	rc := http.NewResponseController(w)
	ping := time.NewTicker(pingEvery)
	defer ping.Stop()
	ev, isPing := first, false
	for {
		var err error
		if !isPing {
			data, _ := json.Marshal(ev)
			_, err = fmt.Fprintf(w, "event: board\ndata: %s\n\n", data)
		} else {
			_, err = fmt.Fprint(w, ": ping\n\n")
		}
		if err != nil || rc.Flush() != nil {
			return
		}
		select {
		case <-r.Context().Done():
			return
		case ev = <-events:
			isPing = false
		case <-ping.C:
			isPing = true
		}
	}
}
