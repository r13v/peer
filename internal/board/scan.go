package board

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

// maxScanDepth bounds the walk of a workspace outside git.
const maxScanDepth = 4

// Scan finds *.board directories under workspace and returns their
// canonical paths. Inside a git work tree it lists tracked and
// non-ignored files of the workspace only; elsewhere it walks up to four
// levels, skipping hidden directories, node_modules, vendor and symlinks.
// It never looks inside a board.
func Scan(workspace string) ([]string, error) {
	ws, err := Canonical(workspace)
	if err != nil {
		return nil, err
	}
	if filepath.Ext(ws) == ".board" {
		if hasBoardFile(ws) {
			return []string{ws}, nil
		}
		return []string{}, nil
	}
	var rels []string
	if inGit(ws) {
		rels, err = gitBoards(ws)
	} else {
		rels, err = walkBoards(ws)
	}
	if err != nil {
		return nil, err
	}
	out := []string{}
	for _, rel := range rels {
		dir := filepath.Join(ws, filepath.FromSlash(rel))
		if !hasBoardFile(dir) {
			continue
		}
		if c, err := Canonical(dir); err == nil && !slices.Contains(out, c) {
			out = append(out, c)
		}
	}
	slices.Sort(out)
	return out, nil
}

func inGit(dir string) bool {
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--is-inside-work-tree").Output()
	return err == nil && strings.TrimSpace(string(out)) == "true"
}

func gitBoards(ws string) ([]string, error) {
	out, err := exec.Command("git", "-C", ws, "ls-files", "-z", "--cached", "--others", "--exclude-standard", "--", ".").Output()
	if err != nil {
		return nil, err
	}
	var rels []string
	for _, p := range strings.Split(string(out), "\x00") {
		parts := strings.Split(p, "/")
		// The first *.board component is the board; a board.json deeper
		// inside it belongs to that board, not to a nested one.
		for i, part := range parts {
			if strings.HasSuffix(part, ".board") {
				if i == len(parts)-2 && parts[i+1] == "board.json" {
					rels = append(rels, strings.Join(parts[:i+1], "/"))
				}
				break
			}
		}
	}
	return rels, nil
}

func walkBoards(ws string) ([]string, error) {
	var rels []string
	err := filepath.WalkDir(ws, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if p == ws {
				return err
			}
			return nil // an unreadable directory hides only itself
		}
		if p == ws || !d.IsDir() {
			return nil // symlinks are not dirs here, so they are skipped
		}
		name := d.Name()
		if strings.HasPrefix(name, ".") || name == "node_modules" || name == "vendor" {
			return filepath.SkipDir
		}
		rel, err := filepath.Rel(ws, p)
		if err != nil {
			return err
		}
		if strings.HasSuffix(name, ".board") {
			rels = append(rels, filepath.ToSlash(rel))
			return filepath.SkipDir
		}
		if strings.Count(rel, string(filepath.Separator))+1 >= maxScanDepth {
			return filepath.SkipDir
		}
		return nil
	})
	return rels, err
}

func hasBoardFile(dir string) bool {
	st, err := os.Lstat(filepath.Join(dir, "board.json"))

	return err == nil && st.Mode().IsRegular()
}
