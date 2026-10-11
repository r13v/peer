package boardhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

const (
	// retryEvery is how often a peer that does not run the server checks
	// the live one and tries to take the lock over.
	retryEvery     = 2 * time.Second
	clientTimeout  = 2 * time.Second
	shutdownWithin = 2 * time.Second
	readHeaderTime = 10 * time.Second
)

// Status is what the TUI shows about the board server.
type Status struct {
	URL   string // the home page, set while a server of this PEER_HOME answers
	Owner bool   // this process runs it
	Err   error
}

// Health is the answer of GET /api/health.
type Health struct {
	OK       bool   `json:"ok"`
	PeerHome string `json:"peerHome"`
	Port     int    `json:"port"`
	Version  string `json:"version"`
}

// Run keeps a board server of cfg.PeerHome available until ctx ends. The
// process holding $PEER_HOME/server.lock runs the server; the others use
// it, keep workspace registered with it, and take the lock over when its
// owner exits. report gets every change of the status.
func Run(ctx context.Context, cfg Config, workspace string, report func(Status)) {
	lockPath := filepath.Join(cfg.PeerHome, "server.lock")
	client := NewClient(cfg.Port)
	var last string
	show := func(st Status) {
		key := fmt.Sprint(st.URL, st.Owner, st.Err)
		if key != last {
			last = key
			report(st)
		}
	}
	for ctx.Err() == nil {
		if unlock, ok := tryLock(lockPath); ok {
			err := serve(ctx, cfg, workspace, func() { show(Status{URL: client.base + "/", Owner: true}) })
			unlock()
			if err != nil {
				show(Status{Err: err})
			}
		} else {
			show(check(ctx, client, cfg, workspace))
		}
		select {
		case <-ctx.Done():
		case <-time.After(retryEvery):
		}
	}
}

// tryLock takes server.lock without waiting; unlock releases it.
func tryLock(path string) (unlock func(), ok bool) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, false
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, false
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, false
	}
	unlock = func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}

	return unlock, true
}

// serve runs the server until ctx ends; ready is called once it listens.
// localhost may resolve to either loopback address, so the server listens
// on both; a system without IPv6 gets 127.0.0.1 alone.
func serve(ctx context.Context, cfg Config, workspace string, ready func()) error {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", cfg.Port))
	if err != nil {
		return fmt.Errorf("board server: port %d is busy; set PEER_PORT to a free port (%w)", cfg.Port, err)
	}
	listeners := []net.Listener{ln}
	ln6, err := net.Listen("tcp", fmt.Sprintf("[::1]:%d", cfg.Port))
	switch {
	case err == nil:
		listeners = append(listeners, ln6)
	case errors.Is(err, syscall.EADDRINUSE):
		// localhost could open the program on [::1] instead of peer.
		ln.Close()
		return fmt.Errorf("board server: port %d is busy on [::1]; set PEER_PORT to a free port (%w)", cfg.Port, err)
	}
	s := New(cfg)
	go s.Register(workspace)
	// Cancelling base ends the SSE streams, which Shutdown would wait for.
	base, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv := &http.Server{
		Handler:           s.Handler(),
		ReadHeaderTimeout: readHeaderTime,
		BaseContext:       func(net.Listener) context.Context { return base },
	}
	failed := make(chan error, len(listeners))
	for _, l := range listeners {
		go func() { failed <- srv.Serve(l) }()
	}
	ready()

	select {
	case <-ctx.Done():
	case err = <-failed:
	}
	cancel()
	stop, done := context.WithTimeout(context.Background(), shutdownWithin)
	defer done()
	_ = srv.Shutdown(stop)
	if errors.Is(err, http.ErrServerClosed) {
		err = nil
	}

	return err
}

// check reports the server another process runs and registers workspace
// with it. Registration repeats on every check: it is a no-op for a known
// workspace, and a server that a takeover restarted knows none.
func check(ctx context.Context, c *Client, cfg Config, workspace string) Status {
	h, err := c.Health(ctx)
	var se *syntaxError
	switch {
	case errors.As(err, &se) || err == nil && !h.OK:
		return Status{Err: fmt.Errorf("board server: port %d is used by another program; set PEER_PORT to a free port", cfg.Port)}
	case err != nil:
		return Status{Err: fmt.Errorf("board server: waiting for it on port %d", cfg.Port)}
	case h.PeerHome != cfg.PeerHome || h.Port != cfg.Port:
		return Status{Err: fmt.Errorf("board server: port %d serves peer of PEER_HOME %s; set PEER_PORT to a free port", cfg.Port, h.PeerHome)}
	}
	if err := c.AddWorkspace(ctx, workspace); err != nil {
		return Status{Err: fmt.Errorf("board server: register %s: %w", workspace, err)}
	}

	return Status{URL: c.base + "/"}
}

// Client calls a board server as the TUI does, with the headers that a
// write needs.
type Client struct {
	base string
	http *http.Client
}

// NewClient calls the server at http://localhost:port, dialing
// 127.0.0.1 when localhost does not connect.
func NewClient(port int) *Client {
	var d net.Dialer
	fallback := fmt.Sprintf("127.0.0.1:%d", port)
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		conn, err := d.DialContext(ctx, network, addr)
		if err != nil {
			return d.DialContext(ctx, network, fallback)
		}
		return conn, nil
	}

	return &Client{base: fmt.Sprintf("http://localhost:%d", port), http: &http.Client{Timeout: clientTimeout, Transport: transport}}
}

// syntaxError is an answer that is not the board API's JSON.
type syntaxError struct{ err error }

func (e *syntaxError) Error() string { return "not a peer board server: " + e.err.Error() }

// Health asks the server who it is.
func (c *Client) Health(ctx context.Context) (Health, error) {
	var h Health
	err := c.do(ctx, http.MethodGet, "/api/health", nil, &h)

	return h, err
}

// AddWorkspace registers a directory whose boards the home page lists.
func (c *Client) AddWorkspace(ctx context.Context, path string) error {
	return c.do(ctx, http.MethodPost, "/api/workspaces", map[string]string{"path": path}, nil)
}

func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var payload []byte
	if body != nil {
		var err error
		if payload, err = json.Marshal(body); err != nil {
			return err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Peer", "1")
		req.Header.Set("Origin", c.base)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return &syntaxError{fmt.Errorf("%s %s answered %s", method, path, resp.Status)}
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return &syntaxError{err}
	}

	return nil
}
