// Package fsops implements the file manager operations exposed to the app.
package fsops

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// Entry is one directory entry.
type Entry struct {
	Name       string `json:"name"`
	Path       string `json:"path"`
	Type       string `json:"type"` // dir|file|symlink|other
	Size       int64  `json:"size"`
	Mtime      int64  `json:"mtime"`
	Mode       string `json:"mode"`
	Hidden     bool   `json:"hidden,omitempty"`
	Target     string `json:"target,omitempty"`
	TargetType string `json:"targetType,omitempty"`
	Mime       string `json:"mime,omitempty"`
	Children   int    `json:"children,omitempty"`
}

var startupHome = detectHome()

// Home is the user's home directory at startup (with passwd fallback if $HOME was unset).
var Home = startupHome

func detectHome() string {
	if h, err := os.UserHomeDir(); err == nil && strings.TrimSpace(h) != "" {
		return h
	}
	if u, err := user.Current(); err == nil && strings.TrimSpace(u.HomeDir) != "" {
		return u.HomeDir
	}
	return "/"
}

func defaultHome() string {
	if Home != "" && Home != startupHome {
		return Home
	}
	return detectHome()
}

// FS scopes file manager operations to a specific host Home directory.
type FS struct {
	Home string
}

// New creates a host-scoped FS instance, falling back to current/configured Home when empty.
func New(home string) FS {
	h := strings.TrimSpace(home)
	if h == "" {
		h = defaultHome()
	}
	return FS{Home: filepath.Clean(h)}
}

// HomeDir returns the effective home directory for this FS instance.
func (f FS) HomeDir() string {
	if strings.TrimSpace(f.Home) != "" {
		return filepath.Clean(f.Home)
	}
	return defaultHome()
}

// Resolve expands ~ and makes a path absolute (relative to f.HomeDir()).
func (f FS) Resolve(p string) string {
	home := f.HomeDir()
	p = strings.TrimSpace(p)
	if p == "" || p == "~" {
		return home
	}
	if strings.HasPrefix(p, "~/") {
		p = filepath.Join(home, p[2:])
	}
	if strings.HasPrefix(p, "file://") {
		p = strings.TrimPrefix(p, "file://")
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(home, p)
	}
	return filepath.Clean(p)
}

// Resolve expands ~ and makes a path absolute (relative to current HOME).
func Resolve(p string) string {
	return New("").Resolve(p)
}

func entryFor(path string, info fs.FileInfo) Entry {
	e := Entry{Name: info.Name(), Path: path, Size: info.Size(), Mtime: info.ModTime().UnixMilli(), Mode: info.Mode().String(),
		Hidden: strings.HasPrefix(info.Name(), ".")}
	switch {
	case info.Mode()&os.ModeSymlink != 0:
		e.Type = "symlink"
		if t, err := os.Readlink(path); err == nil {
			e.Target = t
		}
		if st, err := os.Stat(path); err == nil {
			if st.IsDir() {
				e.TargetType = "dir"
			} else {
				e.TargetType = "file"
				e.Size = st.Size()
			}
		} else {
			e.TargetType = "broken"
		}
	case info.IsDir():
		e.Type = "dir"
	case info.Mode().IsRegular():
		e.Type = "file"
	default:
		e.Type = "other"
	}
	if e.Type == "file" || e.TargetType == "file" {
		e.Mime = mime.TypeByExtension(strings.ToLower(filepath.Ext(info.Name())))
	}
	return e
}

// List returns directory entries (dirs first).
func (f FS) List(p string, hidden bool) (map[string]any, error) {
	p = f.Resolve(p)
	des, err := os.ReadDir(p)
	if err != nil {
		return nil, err
	}
	out := make([]Entry, 0, len(des))
	for _, de := range des {
		if !hidden && strings.HasPrefix(de.Name(), ".") {
			continue
		}
		info, err := de.Info()
		if err != nil {
			continue
		}
		out = append(out, entryFor(filepath.Join(p, de.Name()), info))
	}
	sort.Slice(out, func(i, j int) bool {
		di := out[i].Type == "dir" || out[i].TargetType == "dir"
		dj := out[j].Type == "dir" || out[j].TargetType == "dir"
		if di != dj {
			return di
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	parent := filepath.Dir(p)
	if parent == p {
		parent = ""
	}
	git := ""
	gctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	b, gerr := exec.CommandContext(gctx, "git", "-C", p, "rev-parse", "--abbrev-ref", "HEAD").Output()
	cancel()
	if gerr == nil {
		git = strings.TrimSpace(string(b))
	}
	return map[string]any{"path": p, "parent": parent, "entries": out, "gitBranch": git}, nil
}

// List returns directory entries (dirs first).
func List(p string, hidden bool) (map[string]any, error) {
	return New("").List(p, hidden)
}

// Stat describes a single path.
func (f FS) Stat(p string) (Entry, error) {
	p = f.Resolve(p)
	info, err := os.Lstat(p)
	if err != nil {
		return Entry{}, err
	}
	e := entryFor(p, info)
	if e.Type == "dir" {
		if des, err := os.ReadDir(p); err == nil {
			e.Children = len(des)
		}
	}
	return e, nil
}

// Stat describes a single path.
func Stat(p string) (Entry, error) {
	return New("").Stat(p)
}

// ReadResult is the content of a file.
type ReadResult struct {
	Path      string  `json:"path"`
	Size      int64   `json:"size"`
	Mtime     int64   `json:"mtime"`
	Text      *string `json:"text,omitempty"`
	Base64    string  `json:"base64,omitempty"`
	Binary    bool    `json:"binary"`
	Truncated bool    `json:"truncated"`
	Mime      string  `json:"mime"`
}

// Read returns up to max bytes of a file; text when it looks like UTF-8.
func (fsys FS) Read(p string, max int64) (*ReadResult, error) {
	p = fsys.Resolve(p)
	if max <= 0 {
		max = 2 << 20
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if st.IsDir() {
		return nil, errors.New("is a directory")
	}
	b, err := io.ReadAll(io.LimitReader(f, max))
	if err != nil {
		return nil, err
	}
	r := &ReadResult{Path: p, Size: st.Size(), Mtime: st.ModTime().UnixMilli(), Truncated: st.Size() > int64(len(b))}
	r.Mime = mime.TypeByExtension(strings.ToLower(filepath.Ext(p)))
	if r.Mime == "" {
		r.Mime = http.DetectContentType(b)
	}
	if IsText(b) {
		txt := string(b)
		if r.Truncated {
			// Don't split a UTF-8 sequence.
			for len(txt) > 0 && !utf8.ValidString(txt) {
				txt = txt[:len(txt)-1]
			}
		}
		r.Text = &txt
	} else {
		r.Binary = true
		r.Base64 = base64.StdEncoding.EncodeToString(b)
	}
	return r, nil
}

// Read returns up to max bytes of a file; text when it looks like UTF-8.
func Read(p string, max int64) (*ReadResult, error) {
	return New("").Read(p, max)
}

// IsText guesses whether bytes are text.
func IsText(b []byte) bool {
	n := len(b)
	if n > 8192 {
		n = 8192
	}
	sample := b[:n]
	if bytes.IndexByte(sample, 0) >= 0 {
		return false
	}
	// Allow a trailing partial rune.
	for i := 0; i < 3 && len(sample) > 0 && !utf8.Valid(sample); i++ {
		sample = sample[:len(sample)-1]
	}
	return utf8.Valid(sample)
}

// WriteParams writes a file.
type WriteParams struct {
	Path        string `json:"path"`
	Text        string `json:"text"`
	Base64      string `json:"base64"`
	CreateDirs  bool   `json:"createDirs"`
	ExpectMtime int64  `json:"expectMtime"`
	NoOverwrite bool   `json:"noOverwrite"`
}

// ErrConflict means the file changed on disk since it was read.
var ErrConflict = errors.New("file changed on disk since it was opened")

// Write stores content atomically, preserving the file mode.
func (f FS) Write(w WriteParams) (Entry, error) {
	p := f.Resolve(w.Path)
	data := []byte(w.Text)
	if w.Base64 != "" {
		b, err := base64.StdEncoding.DecodeString(w.Base64)
		if err != nil {
			return Entry{}, err
		}
		data = b
	}
	mode := os.FileMode(0o644)
	if st, err := os.Stat(p); err == nil {
		if st.IsDir() {
			return Entry{}, errors.New("is a directory")
		}
		if w.NoOverwrite {
			return Entry{}, fs.ErrExist
		}
		if w.ExpectMtime > 0 && st.ModTime().UnixMilli() != w.ExpectMtime {
			return Entry{}, ErrConflict
		}
		mode = st.Mode().Perm()
	}
	if w.CreateDirs {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return Entry{}, err
		}
	}
	tmp := filepath.Join(filepath.Dir(p), fmt.Sprintf(".%s.agentc-%d", filepath.Base(p), time.Now().UnixNano()))
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return Entry{}, err
	}
	if err := os.Rename(tmp, p); err != nil {
		os.Remove(tmp)
		return Entry{}, err
	}
	return f.Stat(p)
}

// Write stores content atomically, preserving the file mode.
func Write(w WriteParams) (Entry, error) {
	return New("").Write(w)
}

// Mkdir creates a directory.
func (f FS) Mkdir(p string, parents bool) (Entry, error) {
	p = f.Resolve(p)
	var err error
	if parents {
		err = os.MkdirAll(p, 0o755)
	} else {
		err = os.Mkdir(p, 0o755)
	}
	if err != nil {
		return Entry{}, err
	}
	return f.Stat(p)
}

// Mkdir creates a directory.
func Mkdir(p string, parents bool) (Entry, error) {
	return New("").Mkdir(p, parents)
}

// Create makes an empty file (fails if it exists).
func (f FS) Create(p string) (Entry, error) {
	p = f.Resolve(p)
	fh, err := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return Entry{}, err
	}
	fh.Close()
	return f.Stat(p)
}

// Create makes an empty file (fails if it exists).
func Create(p string) (Entry, error) {
	return New("").Create(p)
}

// Rename moves a path (falls back to copy+delete across devices).
func (f FS) Rename(from, to string, overwrite bool) (Entry, error) {
	from, to = f.Resolve(from), f.Resolve(to)
	if st, err := os.Stat(to); err == nil && st.IsDir() && to != from {
		to = filepath.Join(to, filepath.Base(from)) // mv semantics: move into an existing directory
	}
	if strings.HasPrefix(to+"/", from+"/") {
		return Entry{}, errors.New("cannot move a directory into itself")
	}
	if _, err := os.Lstat(to); err == nil && !overwrite {
		return Entry{}, fmt.Errorf("%s already exists", to)
	}
	if err := os.Rename(from, to); err != nil {
		var le *os.LinkError
		if errors.As(err, &le) && strings.Contains(le.Err.Error(), "cross-device") {
			if err := copyAll(from, to); err != nil {
				return Entry{}, err
			}
			if err := os.RemoveAll(from); err != nil {
				return Entry{}, err
			}
		} else {
			return Entry{}, err
		}
	}
	return f.Stat(to)
}

// Rename moves a path (falls back to copy+delete across devices).
func Rename(from, to string, overwrite bool) (Entry, error) {
	return New("").Rename(from, to, overwrite)
}

// Copy copies files or directories recursively.
func (f FS) Copy(from, to string, overwrite bool) (Entry, error) {
	from, to = f.Resolve(from), f.Resolve(to)
	if st, err := os.Stat(to); err == nil && st.IsDir() {
		to = filepath.Join(to, filepath.Base(from))
	}
	if _, err := os.Lstat(to); err == nil && !overwrite {
		// Pick a free "copy" name.
		ext := filepath.Ext(to)
		base := strings.TrimSuffix(to, ext)
		for i := 1; ; i++ {
			cand := fmt.Sprintf("%s copy%s", base, ext)
			if i > 1 {
				cand = fmt.Sprintf("%s copy %d%s", base, i, ext)
			}
			if _, err := os.Lstat(cand); os.IsNotExist(err) {
				to = cand
				break
			}
		}
	}
	if strings.HasPrefix(to+"/", from+"/") {
		return Entry{}, errors.New("cannot copy a directory into itself")
	}
	if err := copyAll(from, to); err != nil {
		return Entry{}, err
	}
	return f.Stat(to)
}

// Copy copies files or directories recursively.
func Copy(from, to string, overwrite bool) (Entry, error) {
	return New("").Copy(from, to, overwrite)
}

func copyAll(from, to string) error {
	return filepath.WalkDir(from, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(from, p)
		dst := filepath.Join(to, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			t, err := os.Readlink(p)
			if err != nil {
				return err
			}
			return os.Symlink(t, dst)
		case d.IsDir():
			return os.MkdirAll(dst, info.Mode().Perm()|0o700)
		case info.Mode().IsRegular():
			return copyFile(p, dst, info.Mode().Perm())
		}
		return nil
	})
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// Delete removes paths: to the desktop trash by default, permanently on request.
func (f FS) Delete(ctx context.Context, paths []string, permanent bool) error {
	home := f.HomeDir()
	var errs []string
	for _, p := range paths {
		p = f.Resolve(p)
		if p == "/" || p == home {
			errs = append(errs, "refusing to delete "+p)
			continue
		}
		if !permanent {
			if _, stErr := os.Lstat(p); stErr != nil {
				errs = append(errs, stErr.Error())
				continue
			}
			cctx, cancel := context.WithTimeout(ctx, 60*time.Second)
			out, err := exec.CommandContext(cctx, "gio", "trash", "--", p).CombinedOutput()
			cancel()
			if err == nil {
				continue
			}
			msg := strings.TrimSpace(string(out))
			combined := strings.ToLower(msg + " " + err.Error())
			tRashUnsupported := errors.Is(err, exec.ErrNotFound)
			for _, sub := range []string{
				"not supported",
				"unable to find or create trash",
				"trash portal",
				"dbus",
				"message bus",
				"without x11",
				"connection is closed",
				"executable file not found",
			} {
				if strings.Contains(combined, sub) {
					tRashUnsupported = true
					break
				}
			}
			if tRashUnsupported {
				errs = append(errs, "TRASH_UNSUPPORTED: the trash isn't available for "+p+"; delete it permanently instead")
			} else {
				reason := msg
				if reason == "" {
					reason = err.Error()
				}
				errs = append(errs, fmt.Sprintf("trash %s: %s", p, reason))
			}
			continue
		}
		if err := os.RemoveAll(p); err != nil {
			errs = append(errs, err.Error())
		}
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

// Delete removes paths: to the desktop trash by default, permanently on request.
func Delete(ctx context.Context, paths []string, permanent bool) error {
	return New("").Delete(ctx, paths, permanent)
}

var skipDirs = map[string]bool{".git": true, "node_modules": true, ".cache": true, "__pycache__": true, ".gradle": true,
	"build": false, ".venv": true, "venv": true, ".idea": true, "target": false, ".npm": true, ".m2": true, ".cargo": true,
	".rustup": true, ".local": false}

// Search finds paths whose name contains query (case-insensitive).
func (f FS) Search(ctx context.Context, root, query string, max int, hidden bool) ([]Entry, error) {
	root = f.Resolve(root)
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return nil, errors.New("empty query")
	}
	if max <= 0 {
		max = 200
	}
	deadline := time.Now().Add(8 * time.Second)
	var out []Entry
	errStop := errors.New("stop")
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if len(out) >= max || time.Now().After(deadline) {
			return errStop
		}
		if ctx.Err() != nil {
			return errStop
		}
		name := d.Name()
		if p != root && d.IsDir() && (skipDirs[name] || (!hidden && strings.HasPrefix(name, ".") && !strings.HasPrefix(q, "."))) {
			return filepath.SkipDir
		}
		if p != root && strings.Contains(strings.ToLower(name), q) {
			if info, err := d.Info(); err == nil {
				out = append(out, entryFor(p, info))
			}
		}
		return nil
	})
	if err != nil && err != errStop {
		return out, err
	}
	return out, nil
}

// Search finds paths whose name contains query (case-insensitive).
func Search(ctx context.Context, root, query string, max int, hidden bool) ([]Entry, error) {
	return New("").Search(ctx, root, query, max, hidden)
}

// Complete returns workspace-relative paths fuzzily matching a query (for @mentions).
func (f FS) Complete(ctx context.Context, cwd, query string, max int) ([]map[string]any, error) {
	cwd = f.Resolve(cwd)
	if max <= 0 {
		max = 40
	}
	q := strings.ToLower(strings.TrimSpace(query))
	type cand struct {
		rel   string
		dir   bool
		score int
	}
	var cands []cand
	deadline := time.Now().Add(3 * time.Second)
	visited := 0
	_ = filepath.WalkDir(cwd, func(p string, d fs.DirEntry, err error) error {
		if err != nil || p == cwd {
			return nil
		}
		visited++
		if visited > 60000 || time.Now().After(deadline) || ctx.Err() != nil {
			return filepath.SkipAll
		}
		name := d.Name()
		if d.IsDir() && (skipDirs[name] || strings.HasPrefix(name, ".")) {
			return filepath.SkipDir
		}
		rel, _ := filepath.Rel(cwd, p)
		lr, ln := strings.ToLower(rel), strings.ToLower(name)
		score, matched := 0, true
		switch {
		case q == "":
			score = 1000 - strings.Count(rel, "/")*100 - len(rel)
		case strings.HasPrefix(ln, q):
			score = 4000 - len(rel)
		case strings.Contains(ln, q):
			score = 3000 - len(rel)
		case strings.Contains(lr, q):
			score = 2000 - len(rel)
		case subseq(lr, q):
			score = 1000 - len(rel)
		default:
			matched = false
		}
		if matched {
			cands = append(cands, cand{rel, d.IsDir(), score})
		}
		return nil
	})
	sort.Slice(cands, func(i, j int) bool { return cands[i].score > cands[j].score })
	var out []map[string]any
	for i := 0; i < len(cands) && i < max; i++ {
		out = append(out, map[string]any{"path": cands[i].rel, "dir": cands[i].dir, "abs": filepath.Join(cwd, cands[i].rel)})
	}
	return out, nil
}

// Complete returns workspace-relative paths fuzzily matching a query (for @mentions).
func Complete(ctx context.Context, cwd, query string, max int) ([]map[string]any, error) {
	return New("").Complete(ctx, cwd, query, max)
}

func subseq(s, q string) bool {
	i := 0
	for j := 0; j < len(s) && i < len(q); j++ {
		if s[j] == q[i] {
			i++
		}
	}
	return i == len(q)
}

// HomeInfo describes the host for the file browser.
func (f FS) HomeInfo() map[string]any {
	home := f.HomeDir()
	u, _ := user.Current()
	name := ""
	if u != nil {
		name = u.Username
	}
	host, _ := os.Hostname()
	roots := []map[string]string{{"name": "Home", "path": home}, {"name": "Root", "path": "/"}, {"name": "Temp", "path": os.TempDir()}}
	for _, d := range []string{"Desktop", "Documents", "Downloads", "Projects", "code", "src"} {
		p := filepath.Join(home, d)
		if st, err := os.Stat(p); err == nil && st.IsDir() {
			roots = append(roots, map[string]string{"name": d, "path": p})
		}
	}
	return map[string]any{"home": home, "user": name, "host": host, "roots": roots, "sep": "/"}
}

// HomeInfo describes the host for the file browser.
func HomeInfo() map[string]any {
	return New("").HomeInfo()
}
