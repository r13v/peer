package board

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// Applied is a written pack: its result and the board as written, with
// the exact bytes, so the server can hash what is on disk.
type Applied struct {
	Result ApplyResult
	Board  *Board
	Data   []byte
}

// Create makes an empty board in dir, which must end with .board and must
// not exist yet.
func Create(dir, title, roomID string) (*Board, error) {
	if filepath.Ext(dir) != ".board" {
		return nil, invalid("path", "a board directory name ends with .board")
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return nil, err
	}
	if err := os.Mkdir(dir, 0o755); err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil, invalid("path", "%s already exists", dir)
		}
		return nil, err
	}
	if err := os.Mkdir(filepath.Join(dir, "assets"), 0o755); err != nil {
		return nil, err
	}
	// The lock file is per machine; keep it out of a repository the board
	// may live in.
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(".lock\n"), 0o644); err != nil {
		return nil, err
	}
	var raw [9]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return nil, err
	}
	b := &Board{FormatVersion: FormatVersion, ID: "b_" + base64.RawURLEncoding.EncodeToString(raw[:]), Title: title, RoomID: roomID, NextID: 1}
	normalize(b)
	err := flock(dir, func() error {
		_, err := write(dir, b)
		return err
	})
	return b, err
}

// Read reads and checks the board in dir without the lock: writes are
// atomic renames, so a reader always sees a whole file.
func Read(dir string) (*Board, []byte, error) {
	if filepath.Ext(dir) != ".board" {
		return nil, nil, notFound("%s is not a *.board directory", dir)
	}
	data, err := os.ReadFile(filepath.Join(dir, "board.json"))
	if errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) {
		return nil, nil, notFound("no board at %s", dir)
	}
	if err != nil {
		return nil, nil, err
	}
	b, err := Parse(data, dir)
	return b, data, err
}

// Hash is the hex SHA-256 of board.json bytes.
func Hash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// ApplyPack applies pack to the board in dir under the board lock.
func ApplyPack(dir string, pack Pack, author Author, mode Mode) (Applied, error) {
	var out Applied
	err := lock(dir, func() error {
		var err error
		b, _, err := Read(dir)
		if err != nil {
			return err
		}
		out, err = applyLocked(dir, b, pack, author, mode)
		return err
	})
	return out, err
}

// applyLocked applies pack to b, just read from dir; the caller holds the
// lock.
func applyLocked(dir string, b *Board, pack Pack, author Author, mode Mode) (Applied, error) {
	nb, result, err := Apply(b, dir, pack, author, mode)
	if err != nil {
		return Applied{}, err
	}
	data, err := write(dir, nb)
	return Applied{Result: result, Board: nb, Data: data}, err
}

// ImportAsset stores an image in the board's assets under the lock.
func ImportAsset(dir string, data []byte) (Asset, error) {
	var a Asset
	err := lock(dir, func() error {
		// A board that breaks the schema gets no writes at all.
		if _, _, err := Read(dir); err != nil {
			return err
		}
		var err error
		a, err = importAsset(dir, data)
		return err
	})
	return a, err
}

// ImportImage stores an image and adds an image node for it under one
// lock, so it needs no base.
func ImportImage(dir string, data []byte, parentID, caption string, author Author, mode Mode) (Applied, error) {
	var out Applied
	err := lock(dir, func() error {
		b, _, err := Read(dir)
		if err != nil {
			return err
		}
		a, err := importAsset(dir, data)
		if err != nil {
			return err
		}
		node := &Node{Type: TypeImage, Asset: &a.Asset, Caption: caption, ParentID: parentID}
		out, err = applyLocked(dir, b, Pack{Base: &b.Revision, Ops: []Op{{Op: "add", Node: node}}}, author, mode)
		return err
	})
	return out, err
}

// write checks b and replaces board.json atomically; the caller holds the
// lock.
func write(dir string, b *Board) ([]byte, error) {
	normalize(b)
	if problems := validate(b, dir); len(problems) > 0 {
		return nil, invalidAll(problems)
	}
	data, err := Marshal(b)
	if err != nil {
		return nil, err
	}
	return data, writeAtomic(filepath.Join(dir, "board.json"), data)
}

// lock runs fn under the board's lock, once dir is known to be a board.
func lock(dir string, fn func() error) error {
	if filepath.Ext(dir) != ".board" {
		return notFound("%s is not a *.board directory", dir)
	}
	if st, err := os.Stat(filepath.Join(dir, "board.json")); err != nil || !st.Mode().IsRegular() {
		return notFound("no board at %s", dir)
	}
	return flock(dir, fn)
}

// flock holds an exclusive flock on .lock, never on board.json: the rename
// replaces board.json, and a lock on the old file would guard nothing.
func flock(dir string, fn func() error) error {
	f, err := os.OpenFile(filepath.Join(dir, ".lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("lock board: %w", err)
	}
	defer func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }()
	return fn()
}

// writeAtomic writes a temp file next to path and renames it over path,
// so readers never see a half-written file.
func writeAtomic(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	// Board files are meant to be shared through git, not kept private.
	if err := f.Chmod(0o644); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
