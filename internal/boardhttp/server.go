// Package boardhttp serves boards to the browser on 127.0.0.1: the SPA,
// the board API and SSE. It writes boards only through internal/board,
// so the browser follows the same rules as the CLI.
package boardhttp

import (
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"syscall"

	"github.com/r13v/peer/internal/board"
)

const (
	// maxPackBody limits a browser pack; images go through the assets
	// endpoint, so a pack is text only.
	maxPackBody = 1 << 20
	// maxSmallBody limits the other JSON bodies.
	maxSmallBody = 64 << 10

	htmlCSP = "default-src 'self'; img-src 'self' data: blob:; style-src 'self' 'unsafe-inline'; frame-ancestors 'none'"
)

// assetName is the only file name an asset URL may give.
var assetName = regexp.MustCompile(`^[0-9a-f]{64}\.(png|jpg|gif|webp)$`)

var assetTypes = map[string]string{".png": "image/png", ".jpg": "image/jpeg", ".gif": "image/gif", ".webp": "image/webp"}

var uploadTypes = []string{"image/png", "image/jpeg", "image/gif", "image/webp"}

var errorStatus = map[string]int{
	board.KindConflict:           http.StatusConflict,
	board.KindInvalid:            http.StatusBadRequest,
	board.KindForbidden:          http.StatusBadRequest,
	board.KindNotFound:           http.StatusNotFound,
	board.KindUnsupportedVersion: http.StatusBadRequest,
	board.KindTooLarge:           http.StatusRequestEntityTooLarge,
	board.KindUnsupportedMedia:   http.StatusUnsupportedMediaType,
	board.KindRoom:               http.StatusBadRequest,
}

// Config describes the server of one PEER_HOME.
type Config struct {
	Port     int
	PeerHome string
	Version  string
	// Checkouts lists the known checkouts with their rooms. workspaces
	// are the registered ones, so that a checkout without rooms still
	// gets its group.
	Checkouts func(workspaces []string) ([]Checkout, error)
	// SPA is the built web/dist; it lacks index.html before make web.
	SPA fs.FS
}

// Checkout is a Git checkout that peer knows, with its store.
type Checkout struct {
	Path  string // canonical checkout root
	Store string // its directory under $PEER_HOME/repos
	Rooms []Room
}

// Room is a room of a checkout and the canonical paths of its boards.
type Room struct {
	ID        string
	Active    bool
	StartedAt string
	EndedAt   string
	Boards    []string
}

// Server answers the board API of one PEER_HOME.
type Server struct {
	cfg    Config
	hosts  []string // the accepted Host values; Origin must name the same one
	events *hub

	mu         sync.Mutex
	workspaces []string            // registration order
	found      map[string][]string // scan results by workspace
}

// New makes a server; Handler serves it.
func New(cfg Config) *Server {
	hosts := []string{fmt.Sprintf("localhost:%d", cfg.Port), fmt.Sprintf("127.0.0.1:%d", cfg.Port)}
	return &Server{cfg: cfg, hosts: hosts, events: newHub(), found: map[string][]string{}}
}

// Handler routes the contract's HTTP table behind the Host and Origin
// checks.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.index)
	mux.HandleFunc("GET /_app/", s.static)
	mux.HandleFunc("GET /b/{key}", s.boardPage)
	mux.HandleFunc("GET /b/{key}/assets/{file}", s.asset)
	mux.HandleFunc("GET /api/health", s.health)
	mux.HandleFunc("GET /api/catalog", s.catalog)
	mux.HandleFunc("POST /api/workspaces", s.addWorkspace)
	mux.HandleFunc("POST /api/rescan", s.rescan)
	mux.HandleFunc("GET /api/boards/{key}", s.getBoard)
	mux.HandleFunc("POST /api/boards/{key}/apply", s.apply)
	mux.HandleFunc("POST /api/boards/{key}/assets", s.upload)
	mux.HandleFunc("GET /api/boards/{key}/events", s.subscribe)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		api := strings.HasPrefix(r.URL.Path, "/api/")
		if reason := s.rejection(r, api); reason != "" {
			if api {
				writeJSON(w, http.StatusForbidden, &board.Error{Kind: board.KindForbidden, Message: reason})
			} else {
				htmlError(w, http.StatusForbidden, reason)
			}
			return
		}
		mux.ServeHTTP(w, r)
	})
}

// rejection says why r may not reach the handlers. Host defeats DNS
// rebinding; X-Peer forces a CORS preflight that the server never
// grants, and Origin names the server's own pages at the same Host.
func (s *Server) rejection(r *http.Request, api bool) string {
	origin, own := r.Header.Get("Origin"), "http://"+r.Host
	write := r.Method == http.MethodPost
	switch {
	case !slices.Contains(s.hosts, r.Host):
		return "the Host must be " + strings.Join(s.hosts, " or ")
	case write && r.Header.Get("X-Peer") != "1":
		return "a write needs the X-Peer: 1 header"
	case write && origin != own:
		return "a write needs Origin " + own
	case api && origin != "" && origin != own:
		return "requests from other sites are not allowed"
	}

	return ""
}

func (s *Server) index(w http.ResponseWriter, _ *http.Request) {
	page, err := fs.ReadFile(s.cfg.SPA, "index.html")
	if err != nil {
		htmlError(w, http.StatusServiceUnavailable, "The board UI is not built into this peer: run make web, then build peer again.")
		return
	}
	htmlHeaders(w)
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(page)
}

func (s *Server) static(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/_app/")
	st, err := fs.Stat(s.cfg.SPA, name)
	if !fs.ValidPath(name) || err != nil || st.IsDir() {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeFileFS(w, r, s.cfg.SPA, name)
}

// boardPage opens the SPA only for a key that names a valid board, so a
// bad link fails here and the tab never writes to it.
func (s *Server) boardPage(w http.ResponseWriter, r *http.Request) {
	if _, err := board.DecodeKey(r.PathValue("key")); err != nil {
		htmlError(w, http.StatusNotFound, "No board at this link: "+board.AsError(err).Error())
		return
	}
	s.index(w, r)
}

func (s *Server) asset(w http.ResponseWriter, r *http.Request) {
	dir, err := board.DecodeKey(r.PathValue("key"))
	file := r.PathValue("file")
	if err != nil || !assetName.MatchString(file) {
		http.NotFound(w, r)
		return
	}
	f, err := openAsset(dir, file)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}

	h := w.Header()
	h.Set("Content-Type", assetTypes[filepath.Ext(file)])
	h.Set("Cross-Origin-Resource-Policy", "same-origin")
	h.Set("X-Content-Type-Options", "nosniff")
	// The name is the content hash, so the file never changes.
	h.Set("Cache-Control", "private, max-age=31536000, immutable")
	http.ServeContent(w, r, file, st.ModTime(), f)
}

// openAsset opens assets/name of the canonical board dir without
// following a symlink at the assets directory or at the file.
func openAsset(dir, name string) (*os.File, error) {
	assets := filepath.Join(dir, "assets")
	if st, err := os.Lstat(assets); err != nil || !st.IsDir() {
		return nil, os.ErrNotExist
	}

	return os.OpenFile(filepath.Join(assets, name), os.O_RDONLY|syscall.O_NOFOLLOW, 0)
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, Health{OK: true, PeerHome: s.cfg.PeerHome, Port: s.cfg.Port, Version: s.cfg.Version})
}

func (s *Server) catalog(w http.ResponseWriter, _ *http.Request) {
	c, err := s.Catalog()
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, c)
}

func (s *Server) addWorkspace(w http.ResponseWriter, r *http.Request) {
	data, err := readBody(r, maxSmallBody)
	if err != nil {
		writeError(w, err)
		return
	}
	var req struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(data, &req); err != nil || !filepath.IsAbs(req.Path) {
		writeError(w, invalid("path", "give the absolute path of a directory"))
		return
	}
	path, err := board.Canonical(req.Path)
	if err != nil {
		writeError(w, invalid("path", "no directory at %s", req.Path))
		return
	}
	if st, err := os.Stat(path); err != nil || !st.IsDir() {
		writeError(w, invalid("path", "%s is not a directory", path))
		return
	}

	s.Register(path)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) rescan(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	workspaces := slices.Clone(s.workspaces)
	s.mu.Unlock()
	for _, ws := range workspaces {
		s.scan(ws)
	}
	s.catalog(w, r)
}

func (s *Server) getBoard(w http.ResponseWriter, r *http.Request) {
	dir, err := board.DecodeKey(r.PathValue("key"))
	if err != nil {
		writeError(w, err)
		return
	}
	_, data, err := board.Read(dir)
	if err != nil {
		writeError(w, err)
		return
	}

	snapshot := struct {
		Board json.RawMessage `json:"board"`
		Hash  string          `json:"hash"`
		Ref   board.Ref       `json:"ref"`
	}{data, board.Hash(data), board.NewRef(dir)}
	writeJSON(w, http.StatusOK, snapshot)
}

func (s *Server) apply(w http.ResponseWriter, r *http.Request) {
	dir, err := board.DecodeKey(r.PathValue("key"))
	if err != nil {
		writeError(w, err)
		return
	}
	data, err := readBody(r, maxPackBody)
	if err != nil {
		writeError(w, err)
		return
	}
	pack, err := board.ParsePack(data)
	if err != nil {
		writeError(w, err)
		return
	}
	applied, err := board.ApplyPack(dir, pack, board.Author{Role: "user"}, board.Browser)
	if err != nil {
		writeError(w, err)
		return
	}

	result := struct {
		board.ApplyResult
		Board json.RawMessage `json:"board"`
		Hash  string          `json:"hash"`
	}{applied.Result, applied.Data, board.Hash(applied.Data)}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) upload(w http.ResponseWriter, r *http.Request) {
	media, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if !slices.Contains(uploadTypes, media) {
		writeError(w, &board.Error{Kind: board.KindUnsupportedMedia, Message: "upload a png, jpeg, gif or webp image with its Content-Type"})
		return
	}
	dir, err := board.DecodeKey(r.PathValue("key"))
	if err != nil {
		writeError(w, err)
		return
	}
	data, err := readLimited(r.Body, board.MaxAssetSize)
	if err != nil {
		writeError(w, err)
		return
	}
	asset, err := board.ImportAsset(dir, data)
	if err != nil {
		writeError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, asset)
}

// readBody reads a JSON body of at most limit bytes.
func readBody(r *http.Request, limit int64) ([]byte, error) {
	if media, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); media != "application/json" {
		return nil, &board.Error{Kind: board.KindUnsupportedMedia, Message: "the body must be application/json"}
	}

	return readLimited(r.Body, limit)
}

func readLimited(body io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, &board.Error{Kind: board.KindTooLarge, Limit: limit}
	}

	return data, nil
}

func invalid(path, format string, args ...any) *board.Error {
	return &board.Error{Kind: board.KindInvalid, Problems: []board.Problem{{Path: path, Message: fmt.Sprintf(format, args...)}}}
}

// writeError answers a BoardError with its contract status; any other
// error is the server's own failure.
func writeError(w http.ResponseWriter, err error) {
	var be *board.Error
	if !errors.As(err, &be) {
		writeJSON(w, http.StatusInternalServerError, board.AsError(err))
		return
	}
	writeJSON(w, errorStatus[be.Kind], be)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	h := w.Header()
	h.Set("Content-Type", "application/json")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

func htmlHeaders(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Content-Security-Policy", htmlCSP)
	h.Set("X-Frame-Options", "DENY")
	h.Set("X-Content-Type-Options", "nosniff")
}

func htmlError(w http.ResponseWriter, status int, msg string) {
	htmlHeaders(w)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, "<!doctype html>\n<meta charset=\"utf-8\">\n<title>peer</title>\n<p>%s</p>\n", html.EscapeString(msg))
}
