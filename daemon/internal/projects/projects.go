// Package projects manages first-class workspace projects (local folders,
// newly initialized git repos, cloned URLs / GitHub repos, multi-repo
// workspaces, and per-project settings/scripts).
package projects

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// ScriptDef is a user-defined project script stored in project settings.
type ScriptDef struct {
	ID                  string `json:"id"`
	Name                string `json:"name"`
	Command             string `json:"command"`
	Cwd                 string `json:"cwd,omitempty"`
	Icon                string `json:"icon,omitempty"`
	RunOnWorktreeCreate bool   `json:"runOnWorktreeCreate,omitempty"`
	RunOnSettle         bool   `json:"runOnSettle,omitempty"`
	StopOnSettle        bool   `json:"stopOnSettle,omitempty"`
	Keybinding          string `json:"keybinding,omitempty"`
}

// Project is a registered repository or workspace folder on this host.
type Project struct {
	ID              string            `json:"id"`
	Name            string            `json:"name"`
	Path            string            `json:"path"`
	TrackedFolders  []string          `json:"trackedFolders,omitempty"` // multi-repo support
	Icon            string            `json:"icon,omitempty"`
	Color           string            `json:"color,omitempty"`
	Pinned          bool              `json:"pinned,omitempty"`
	DefaultAgent    string            `json:"defaultAgent,omitempty"`
	DefaultModel    string            `json:"defaultModel,omitempty"`
	DefaultMode     string            `json:"defaultMode,omitempty"`
	DefaultCheckout string            `json:"defaultCheckout,omitempty"` // local | worktree
	DefaultBranch   string            `json:"defaultBranch,omitempty"`
	Env             map[string]string `json:"env,omitempty"`
	Scripts         []ScriptDef       `json:"scripts,omitempty"`
	IsGit           bool              `json:"isGit"`
	CreatedAt       int64             `json:"createdAt"`
	UpdatedAt       int64             `json:"updatedAt"`
}

// Store persists projects atomically to projects.json.
type Store struct {
	path     string
	mu       sync.RWMutex
	projects map[string]*Project
}

// Open loads or initializes the projects store at path.
func Open(path string) (*Store, error) {
	s := &Store{path: path, projects: map[string]*Project{}}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	var list []*Project
	if err := json.Unmarshal(b, &list); err != nil {
		return nil, fmt.Errorf("projects %s: %w", path, err)
	}
	for _, p := range list {
		if p.ID != "" && p.Path != "" {
			p.IsGit = isGitDir(p.Path)
			s.projects[p.ID] = p
		}
	}
	return s, nil
}

func isGitDir(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil
}

func detectChildRepos(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var repos []string
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		sub := filepath.Join(dir, e.Name())
		if isGitDir(sub) {
			repos = append(repos, sub)
		}
	}
	sort.Strings(repos)
	return repos
}

func newID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return "proj-" + hex.EncodeToString(b)
}

// List returns all projects (pinned first, then recently updated).
func (s *Store) List() []Project {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Project, 0, len(s.projects))
	for _, p := range s.projects {
		cp := *p
		cp.IsGit = isGitDir(cp.Path)
		out = append(out, cp)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Pinned != out[j].Pinned {
			return out[i].Pinned
		}
		if out[i].UpdatedAt != out[j].UpdatedAt {
			return out[i].UpdatedAt > out[j].UpdatedAt
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out
}

// Get returns a project by ID.
func (s *Store) Get(id string) (Project, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.projects[id]
	if !ok {
		return Project{}, false
	}
	cp := *p
	cp.IsGit = isGitDir(cp.Path)
	return cp, true
}

// FindByPath returns the project matching dir (exact or longest-prefix ancestor).
func (s *Store) FindByPath(dir string) (Project, bool) {
	clean := filepath.Clean(dir)
	s.mu.RLock()
	defer s.mu.RUnlock()
	var best *Project
	bestLen := -1
	for _, p := range s.projects {
		candidates := append([]string{p.Path}, p.TrackedFolders...)
		for _, cand := range candidates {
			c := filepath.Clean(cand)
			if clean == c || strings.HasPrefix(clean, c+string(filepath.Separator)) {
				if len(c) > bestLen {
					bestLen = len(c)
					best = p
				}
			}
		}
	}
	if best == nil {
		return Project{}, false
	}
	cp := *best
	return cp, true
}

// Add registers an existing local directory as a project (idempotent by path).
func (s *Store) Add(dir, name string) (Project, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return Project{}, err
	}
	abs = filepath.Clean(abs)
	st, err := os.Stat(abs)
	if err != nil || !st.IsDir() {
		return Project{}, fmt.Errorf("directory %q does not exist", abs)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for _, existing := range s.projects {
		if filepath.Clean(existing.Path) == abs {
			if strings.TrimSpace(name) != "" {
				existing.Name = strings.TrimSpace(name)
			}
			existing.UpdatedAt = time.Now().UnixMilli()
			existing.IsGit = isGitDir(abs)
			_ = s.saveLocked()
			return *existing, nil
		}
	}
	if strings.TrimSpace(name) == "" {
		name = filepath.Base(abs)
	}
	now := time.Now().UnixMilli()
	p := &Project{
		ID:              newID(),
		Name:            strings.TrimSpace(name),
		Path:            abs,
		DefaultCheckout: "local",
		IsGit:           isGitDir(abs),
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if !p.IsGit {
		if subs := detectChildRepos(abs); len(subs) > 0 {
			p.TrackedFolders = subs
		}
	}
	s.projects[p.ID] = p
	if err := s.saveLocked(); err != nil {
		return Project{}, err
	}
	return *p, nil
}

// Init creates a new directory, initializes a Git repo (with optional initial commit), and registers it.
func (s *Store) Init(ctx context.Context, parentDir, name string, initialCommit bool) (Project, error) {
	name = strings.TrimSpace(name)
	if name == "" || strings.ContainsAny(name, `/\`) || name == "." || name == ".." {
		return Project{}, errors.New("invalid project name")
	}
	target := filepath.Join(parentDir, name)
	if _, err := os.Stat(target); err == nil {
		return Project{}, fmt.Errorf("directory %q already exists", target)
	}
	if err := os.MkdirAll(target, 0o755); err != nil {
		return Project{}, err
	}
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cctx, "git", "init", "-b", "main")
	cmd.Dir = target
	if out, err := cmd.CombinedOutput(); err != nil {
		// Fallback for older git without -b
		cmd2 := exec.CommandContext(cctx, "git", "init")
		cmd2.Dir = target
		if out2, err2 := cmd2.CombinedOutput(); err2 != nil {
			return Project{}, fmt.Errorf("git init: %s / %s", strings.TrimSpace(string(out)), strings.TrimSpace(string(out2)))
		}
	}
	if initialCommit {
		readme := filepath.Join(target, "README.md")
		_ = os.WriteFile(readme, []byte("# "+name+"\n"), 0o644)
		add := exec.CommandContext(cctx, "git", "add", "README.md")
		add.Dir = target
		_ = add.Run()
		commit := exec.CommandContext(cctx, "git", "-c", "user.name=Agent Companion", "-c", "user.email=agentc@local", "commit", "-m", "Initial commit")
		commit.Dir = target
		_ = commit.Run()
	}
	return s.Add(target, name)
}

// Clone clones a Git URL or GitHub owner/repo into parentDir and registers it.
func (s *Store) Clone(ctx context.Context, repoURL, parentDir, dirName string) (Project, error) {
	repoURL = strings.TrimSpace(repoURL)
	if repoURL == "" {
		return Project{}, errors.New("repository URL or owner/repo is required")
	}
	// Shorthand owner/repo -> https://github.com/owner/repo.git
	isGHShort := !strings.Contains(repoURL, "://") && !strings.HasPrefix(repoURL, "git@") && strings.Count(repoURL, "/") == 1
	if dirName == "" {
		base := strings.TrimSuffix(repoURL, "/")
		base = strings.TrimSuffix(base, ".git")
		if idx := strings.LastIndexAny(base, "/:"); idx >= 0 {
			dirName = base[idx+1:]
		} else {
			dirName = base
		}
	}
	if dirName == "" || strings.ContainsAny(dirName, `/\`) {
		return Project{}, errors.New("could not determine target directory name")
	}
	if err := os.MkdirAll(parentDir, 0o755); err != nil {
		return Project{}, err
	}
	target := filepath.Join(parentDir, dirName)
	if _, err := os.Stat(target); err == nil {
		return Project{}, fmt.Errorf("destination %q already exists", target)
	}
	cctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	var cmd *exec.Cmd
	if isGHShort {
		if gh, err := exec.LookPath("gh"); err == nil {
			cmd = exec.CommandContext(cctx, gh, "repo", "clone", repoURL, target)
		} else {
			cmd = exec.CommandContext(cctx, "git", "clone", "https://github.com/"+repoURL+".git", target)
		}
	} else {
		cmd = exec.CommandContext(cctx, "git", "clone", repoURL, target)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return Project{}, fmt.Errorf("clone failed: %s", strings.TrimSpace(string(out)))
	}
	return s.Add(target, dirName)
}

// UpdatePatch updates editable project metadata.
type UpdatePatch struct {
	Name            *string           `json:"name,omitempty"`
	Icon            *string           `json:"icon,omitempty"`
	Color           *string           `json:"color,omitempty"`
	Pinned          *bool             `json:"pinned,omitempty"`
	DefaultAgent    *string           `json:"defaultAgent,omitempty"`
	DefaultModel    *string           `json:"defaultModel,omitempty"`
	DefaultMode     *string           `json:"defaultMode,omitempty"`
	DefaultCheckout *string           `json:"defaultCheckout,omitempty"`
	DefaultBranch   *string           `json:"defaultBranch,omitempty"`
	TrackedFolders  *[]string         `json:"trackedFolders,omitempty"`
	Env             map[string]string `json:"env,omitempty"`
	Scripts         *[]ScriptDef      `json:"scripts,omitempty"`
}

// Update applies a partial update to a project.
func (s *Store) Update(id string, patch UpdatePatch) (Project, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.projects[id]
	if !ok {
		return Project{}, errors.New("project not found")
	}
	if patch.Name != nil && strings.TrimSpace(*patch.Name) != "" {
		p.Name = strings.TrimSpace(*patch.Name)
	}
	if patch.Icon != nil {
		p.Icon = *patch.Icon
	}
	if patch.Color != nil {
		p.Color = *patch.Color
	}
	if patch.Pinned != nil {
		p.Pinned = *patch.Pinned
	}
	if patch.DefaultAgent != nil {
		p.DefaultAgent = *patch.DefaultAgent
	}
	if patch.DefaultModel != nil {
		p.DefaultModel = *patch.DefaultModel
	}
	if patch.DefaultMode != nil {
		p.DefaultMode = *patch.DefaultMode
	}
	if patch.DefaultCheckout != nil {
		p.DefaultCheckout = *patch.DefaultCheckout
	}
	if patch.DefaultBranch != nil {
		p.DefaultBranch = *patch.DefaultBranch
	}
	if patch.TrackedFolders != nil {
		p.TrackedFolders = *patch.TrackedFolders
	}
	if patch.Env != nil {
		p.Env = patch.Env
	}
	if patch.Scripts != nil {
		p.Scripts = *patch.Scripts
	}
	p.UpdatedAt = time.Now().UnixMilli()
	if err := s.saveLocked(); err != nil {
		return Project{}, err
	}
	return *p, nil
}

// Remove unregisters a project (never deletes files on disk).
func (s *Store) Remove(id string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.projects[id]; !ok {
		return false, nil
	}
	delete(s.projects, id)
	return true, s.saveLocked()
}

func (s *Store) saveLocked() error {
	if s.path == "" {
		return nil
	}
	list := make([]*Project, 0, len(s.projects))
	for _, p := range s.projects {
		list = append(list, p)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].CreatedAt < list[j].CreatedAt })
	b, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(s.path), filepath.Base(s.path)+".tmp*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	if _, err = f.Write(b); err == nil {
		err = f.Close()
	} else {
		_ = f.Close()
	}
	if err == nil {
		_ = os.Chmod(tmp, 0o600)
		err = os.Rename(tmp, s.path)
	}
	if err != nil {
		_ = os.Remove(tmp)
	}
	return err
}
