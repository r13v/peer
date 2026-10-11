package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/r13v/peer/internal/board"
)

const boardUsage = "usage: peer board create NAME [--title T] [--path DIR] --as ROLE [--room ID], peer board list, peer board inspect BOARD [--overview|--frame ID|--ids ID,...] [--as ROLE --room ID], peer board apply BOARD --as ROLE [--room ID] < pack.json, peer board import-image BOARD FILE [--frame ID] [--caption T] --as ROLE [--room ID], or peer board schema"

// boardName is a board in the checkout's store, boards/<NAME>.board.
var boardName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// maxPack limits a pack read from stdin; an image goes through
// import-image, so a pack is text only.
const maxPack = 32 << 20

// runBoard runs peer board ...; every error it returns is a board.Error,
// which main prints as contract JSON.
func runBoard(args []string, in io.Reader, out io.Writer, cwd string) error {
	if err := boardCommand(args, in, out, cwd); err != nil {
		return board.AsError(err)
	}
	return nil
}

func usageError(format string, args ...any) error {
	return &board.Error{Kind: board.KindInvalid, Problems: []board.Problem{{Path: "args", Message: fmt.Sprintf(format, args...)}}}
}

func roomError(format string, args ...any) error {
	return &board.Error{Kind: board.KindRoom, Message: fmt.Sprintf(format, args...)}
}

type boardFlags struct {
	title, path, as, room, frame, ids, caption string
	overview                                   bool
}

func boardCommand(args []string, in io.Reader, out io.Writer, cwd string) error {
	if len(args) == 0 {
		return usageError("%s", boardUsage)
	}
	cmd := args[0]
	// Positional arguments come first, as in the other peer commands.
	rest, pos := args[1:], []string(nil)
	for len(rest) > 0 && !strings.HasPrefix(rest[0], "-") {
		pos, rest = append(pos, rest[0]), rest[1:]
	}
	var f boardFlags
	fs := flag.NewFlagSet("board "+cmd, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&f.title, "title", "", "board title")
	fs.StringVar(&f.path, "path", "", "directory to create the board in")
	fs.StringVar(&f.as, "as", "", "author role")
	fs.StringVar(&f.room, "room", "", "room the author works in")
	fs.StringVar(&f.frame, "frame", "", "frame id")
	fs.StringVar(&f.ids, "ids", "", "comma-separated ids")
	fs.StringVar(&f.caption, "caption", "", "image caption")
	fs.BoolVar(&f.overview, "overview", false, "short overview")
	if err := fs.Parse(rest); err != nil {
		return usageError("%v; %s", err, boardUsage)
	}
	if fs.NArg() != 0 {
		return usageError("unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}
	wantPos := map[string]int{"create": 1, "list": 0, "inspect": 1, "apply": 1, "import-image": 2, "schema": 0}
	n, ok := wantPos[cmd]
	if !ok || len(pos) != n {
		return usageError("%s", boardUsage)
	}
	switch cmd {
	case "schema":
		s, err := board.LoadSchemas()
		if err != nil {
			return err
		}
		return printJSON(out, s)
	case "list":
		return boardList(out, cwd)
	case "create":
		return boardCreate(out, cwd, pos[0], f)
	case "inspect":
		return boardInspect(out, cwd, pos[0], f)
	case "apply":
		return boardApply(in, out, cwd, pos[0], f)
	default:
		return boardImport(out, cwd, pos[0], pos[1], f)
	}
}

func printJSON(out io.Writer, v any) error {
	enc := json.NewEncoder(out)
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

// boardActor checks --as and --room before the board is touched. Without
// a room only the user may write; with one, --as must be an active,
// non-kicked member. The store is returned for linking the board.
func boardActor(cwd string, f boardFlags, required bool) (board.Author, *store, error) {
	switch {
	case f.as == "" && f.room != "":
		return board.Author{}, nil, roomError("--room needs --as ROLE")
	case f.as == "" && required:
		return board.Author{}, nil, usageError("--as ROLE is required: your role in --room ID, or user for a human")
	case f.as == "":
		return board.Author{}, nil, nil
	case f.room == "":
		if f.as != human {
			return board.Author{}, nil, roomError("without --room only --as user is allowed; add --room ID of your room")
		}
		return board.Author{Role: human}, nil, nil
	}
	if err := checkName(f.as); err != nil {
		return board.Author{}, nil, roomError("%v", err)
	}
	s, err := openStore(cwd)
	if err != nil {
		return board.Author{}, nil, roomError("%v", err)
	}
	v, err := s.refresh(f.room)
	if err != nil {
		return board.Author{}, nil, roomError("%v", err)
	}
	if v.EndedAt != "" {
		return board.Author{}, nil, roomError("room %s: %v", f.room, endedError(v))
	}
	if err := v.check(f.as); err != nil {
		return board.Author{}, nil, roomError("%v", err)
	}
	return board.Author{Role: f.as, RoomID: f.room}, s, nil
}

// link records the board in the room after a successful operation; the
// board lock is released by then. A failure only warns: the next command
// with this board and room adds the path.
func link(s *store, room, path string) []board.Warning {
	if s == nil {
		return []board.Warning{}
	}
	if err := s.linkBoard(room, path); err != nil {
		return []board.Warning{{Kind: "roomLink", Message: fmt.Sprintf("the board was not linked to room %s: %v", room, err)}}
	}
	return []board.Warning{}
}

// boardDir resolves BOARD: a path to a *.board directory, or a name in
// this checkout's store.
func boardDir(cwd, arg string) (string, error) {
	if !strings.ContainsRune(arg, filepath.Separator) && !strings.HasSuffix(arg, ".board") {
		if !boardName.MatchString(arg) {
			return "", usageError("BOARD is a name of 1-64 letters, digits, ., _ or -, or a path to a *.board directory")
		}
		s, err := openStore(cwd)
		if err != nil {
			return "", err
		}
		arg = filepath.Join(s.dir, "boards", arg+".board")
	} else if !filepath.IsAbs(arg) {
		arg = filepath.Join(cwd, arg)
	}
	dir, err := board.Canonical(arg)
	if err != nil {
		return "", &board.Error{Kind: board.KindNotFound, Message: "no board at " + arg}
	}
	return dir, nil
}

func boardCreate(out io.Writer, cwd, name string, f boardFlags) error {
	if !boardName.MatchString(name) || strings.HasSuffix(name, ".board") {
		return usageError("NAME is 1-64 letters, digits, ., _ or -, without .board")
	}
	author, s, err := boardActor(cwd, f, true)
	if err != nil {
		return err
	}
	var dir string
	switch {
	case strings.HasSuffix(f.path, ".board"):
		dir = f.path
	case f.path != "":
		dir = filepath.Join(f.path, name+".board")
	default:
		st, err := openStore(cwd)
		if err != nil {
			return err
		}
		dir = filepath.Join(st.dir, "boards", name+".board")
	}
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(cwd, dir)
	}
	title := f.title
	if title == "" {
		title = name
	}
	if _, err := board.Create(dir, title, author.RoomID); err != nil {
		return err
	}
	if dir, err = board.Canonical(dir); err != nil {
		return err
	}
	ref := board.NewRef(dir)
	warnings := link(s, f.room, dir)
	if s != nil && len(warnings) == 0 {
		ref.Rooms = []string{f.room}
	}
	return printJSON(out, struct {
		board.Ref
		Warnings []board.Warning `json:"warnings,omitempty"`
	}{ref, warnings})
}

func boardInspect(out io.Writer, cwd, arg string, f boardFlags) error {
	given := 0
	for _, set := range []bool{f.overview, f.frame != "", f.ids != ""} {
		if set {
			given++
		}
	}
	if given > 1 {
		return usageError("give at most one of --overview, --frame and --ids")
	}
	_, s, err := boardActor(cwd, f, false)
	if err != nil {
		return err
	}
	dir, err := boardDir(cwd, arg)
	if err != nil {
		return err
	}
	b, _, err := board.Read(dir)
	if err != nil {
		return err
	}
	var v any
	switch {
	case f.overview:
		v = board.NewOverview(b, dir)
	case f.frame != "":
		if v, err = board.InspectFrame(b, dir, f.frame); err != nil {
			return err
		}
	case f.ids != "":
		v = board.InspectIDs(b, dir, strings.Split(f.ids, ","))
	default:
		v = board.Inspect(b, dir)
	}
	warnings := link(s, f.room, dir)
	if len(warnings) == 0 {
		return printJSON(out, v)
	}
	// The projections have no warnings field; add one only when needed.
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(data, &obj); err != nil {
		return err
	}
	if obj["warnings"], err = json.Marshal(warnings); err != nil {
		return err
	}
	return printJSON(out, obj)
}

func boardApply(in io.Reader, out io.Writer, cwd, arg string, f boardFlags) error {
	author, s, err := boardActor(cwd, f, true)
	if err != nil {
		return err
	}
	dir, err := boardDir(cwd, arg)
	if err != nil {
		return err
	}
	data, err := io.ReadAll(io.LimitReader(in, maxPack+1))
	if err != nil {
		return err
	}
	if len(data) > maxPack {
		return &board.Error{Kind: board.KindTooLarge, Limit: maxPack}
	}
	pack, err := board.ParsePack(data)
	if err != nil {
		return err
	}
	applied, err := board.ApplyPack(dir, pack, author, board.CLI)
	if err != nil {
		return err
	}
	applied.Result.Warnings = link(s, f.room, dir)
	return printJSON(out, applied.Result)
}

func boardImport(out io.Writer, cwd, arg, file string, f boardFlags) error {
	author, s, err := boardActor(cwd, f, true)
	if err != nil {
		return err
	}
	dir, err := boardDir(cwd, arg)
	if err != nil {
		return err
	}
	if !filepath.IsAbs(file) {
		file = filepath.Join(cwd, file)
	}
	st, err := os.Stat(file)
	if err != nil {
		return &board.Error{Kind: board.KindNotFound, Message: fmt.Sprintf("no image file %s", file)}
	}
	if st.Size() > board.MaxAssetSize {
		return &board.Error{Kind: board.KindTooLarge, Limit: board.MaxAssetSize}
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	applied, err := board.ImportImage(dir, data, f.frame, f.caption, author, board.CLI)
	if err != nil {
		return err
	}
	applied.Result.Warnings = link(s, f.room, dir)
	return printJSON(out, applied.Result)
}

// boardList lists this checkout's boards: the store's, those linked by its
// rooms and those found under cwd, once each.
func boardList(out io.Writer, cwd string) error {
	var order []string
	rooms := map[string][]string{}
	add := func(path, room string) {
		if _, ok := rooms[path]; !ok {
			order = append(order, path)
			rooms[path] = []string{}
		}
		if room != "" && !slices.Contains(rooms[path], room) {
			rooms[path] = append(rooms[path], room)
		}
	}
	// Outside a checkout there is no store; the scan still works.
	if s, err := openStore(cwd); err == nil {
		dirs, _ := filepath.Glob(filepath.Join(s.dir, "boards", "*.board"))
		sort.SliceStable(dirs, func(i, j int) bool { return mtime(dirs[i]) > mtime(dirs[j]) })
		for _, d := range dirs {
			if c, err := board.Canonical(d); err == nil {
				add(c, "")
			}
		}
		sessions, err := s.sessions()
		if err != nil {
			return err
		}
		for _, v := range sessions {
			for _, p := range v.Boards {
				add(p, v.ID)
			}
		}
	}
	found, err := board.Scan(cwd)
	if err != nil {
		return err
	}
	for _, p := range found {
		add(p, "")
	}
	refs := []board.Ref{}
	for _, p := range order {
		r := board.NewRef(p)
		if len(rooms[p]) > 0 {
			r.Rooms = rooms[p]
		}
		refs = append(refs, r)
	}
	return printJSON(out, map[string][]board.Ref{"boards": refs})
}

func mtime(dir string) int64 {
	st, err := os.Stat(filepath.Join(dir, "board.json"))
	if err != nil {
		return 0
	}
	return st.ModTime().UnixNano()
}
