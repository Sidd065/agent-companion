// Package agycli runs agy's management subcommands and edits its config files.
package agycli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"agentc/internal/agyproc"
)

// CLI runs agy subcommands.
type CLI struct {
	Agy     string
	DataDir string // ~/.gemini/antigravity-cli
	Gemini  string // ~/.gemini
	Env     func() []string
	mu      sync.Mutex
	cache   map[string]cached
}

type cached struct {
	v  any
	at time.Time
}

// Result is a finished command.
type Result struct {
	Args     []string `json:"args"`
	Stdout   string   `json:"stdout"`
	Stderr   string   `json:"stderr"`
	ExitCode int      `json:"exitCode"`
	Duration int64    `json:"durationMs"`
}

// Run executes `agy <args>` with a timeout; stdin is empty.
func (c *CLI) Run(ctx context.Context, timeout time.Duration, args ...string) (*Result, error) {
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	start := time.Now()
	cmd := exec.CommandContext(cctx, c.Agy, args...)
	var baseEnv []string
	if c.Env != nil {
		baseEnv = c.Env()
	}
	cmd.Env = append(agyproc.BuildEnv(baseEnv), "NO_COLOR=1", "AGY_CLI_HIDE_LOGO=1", "TERM=dumb")
	home, _ := os.UserHomeDir()
	cmd.Dir = home
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	cmd.Stdin = nil
	err := cmd.Run()
	r := &Result{Args: args, Stdout: clip(stripANSI(out.String())), Stderr: clip(stripANSI(errb.String())), Duration: time.Since(start).Milliseconds()}
	if cmd.ProcessState != nil {
		r.ExitCode = cmd.ProcessState.ExitCode()
	}
	if cctx.Err() == context.DeadlineExceeded {
		return r, fmt.Errorf("agy %s timed out after %s", strings.Join(args, " "), timeout)
	}
	if err != nil && r.ExitCode == 0 {
		return r, err
	}
	return r, nil
}

func clip(s string) string {
	if len(s) > 2<<20 {
		return s[:2<<20] + "\n…[truncated]"
	}
	return s
}

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b\][^\x07]*(\x07|\x1b\\)`)

func stripANSI(s string) string { return ansiRe.ReplaceAllString(s, "") }

func (c *CLI) cachedRun(ctx context.Context, key string, ttl time.Duration, fn func() (any, error)) (any, error) {
	c.mu.Lock()
	if c.cache == nil {
		c.cache = map[string]cached{}
	}
	if e, ok := c.cache[key]; ok && time.Since(e.at) < ttl {
		c.mu.Unlock()
		return e.v, nil
	}
	c.mu.Unlock()
	v, err := fn()
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.cache[key] = cached{v, time.Now()}
	c.mu.Unlock()
	return v, nil
}

// Invalidate drops cached results.
func (c *CLI) Invalidate() {
	c.mu.Lock()
	c.cache = nil
	c.mu.Unlock()
}

// Version returns `agy --version`.
func (c *CLI) Version(ctx context.Context) string {
	v, err := c.cachedRun(ctx, "version", time.Minute, func() (any, error) {
		r, err := c.Run(ctx, 20*time.Second, "--version")
		if err != nil {
			return nil, err
		}
		return strings.TrimSpace(r.Stdout), nil
	})
	if err != nil {
		return ""
	}
	return v.(string)
}

// Model is one `agy models` row. Default marks the model agy's settings.json
// picks when no --model is given.
type Model struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Default bool   `json:"default,omitempty"`
}

// DefaultModel is the "model" in agy's settings.json (a display name or an
// id), "" when unset.
func (c *CLI) DefaultModel() string {
	b, err := os.ReadFile(filepath.Join(c.DataDir, "settings.json"))
	if err != nil {
		return ""
	}
	var s struct {
		Model string `json:"model"`
	}
	_ = json.Unmarshal(b, &s)
	return strings.TrimSpace(s.Model)
}

// Models lists models available to the account.
func (c *CLI) Models(ctx context.Context, force bool) ([]Model, error) {
	if force {
		c.Invalidate()
	}
	v, err := c.cachedRun(ctx, "models", 10*time.Minute, func() (any, error) {
		r, err := c.Run(ctx, time.Minute, "models")
		if err != nil {
			return nil, err
		}
		var out []Model
		for _, line := range strings.Split(r.Stdout, "\n") {
			f := strings.SplitN(strings.TrimSpace(line), "\t", 2)
			if len(f) == 2 && f[0] != "" {
				out = append(out, Model{ID: f[0], Name: f[1]})
			}
		}
		if len(out) == 0 {
			return nil, fmt.Errorf("agy models: %s", strings.TrimSpace(r.Stdout+r.Stderr))
		}
		return out, nil
	})
	if err != nil {
		return nil, err
	}
	// Mark the default on a copy: the setting can change between cached runs.
	def := c.DefaultModel()
	out := append([]Model(nil), v.([]Model)...)
	for i := range out {
		out[i].Default = def != "" && (out[i].Name == def || out[i].ID == def)
	}
	return out, nil
}

// Changelog returns the release notes.
func (c *CLI) Changelog(ctx context.Context) (string, error) {
	v, err := c.cachedRun(ctx, "changelog", 30*time.Minute, func() (any, error) {
		r, err := c.Run(ctx, time.Minute, "changelog")
		if err != nil {
			return nil, err
		}
		return r.Stdout, nil
	})
	if err != nil {
		return "", err
	}
	return v.(string), nil
}

// Plugins returns `agy plugin list --json`.
func (c *CLI) Plugins(ctx context.Context) (json.RawMessage, error) {
	r, err := c.Run(ctx, time.Minute, "plugin", "list", "--json")
	if err != nil {
		return nil, err
	}
	s := strings.TrimSpace(r.Stdout)
	if !json.Valid([]byte(s)) {
		return nil, fmt.Errorf("agy plugin list: %s", strings.TrimSpace(r.Stdout+r.Stderr))
	}
	return json.RawMessage(s), nil
}

// MCPServer is one entry of mcp_config.json.
type MCPServer struct {
	Name     string            `json:"name"`
	Command  string            `json:"command,omitempty"`
	Args     []string          `json:"args,omitempty"`
	Env      map[string]string `json:"env,omitempty"`
	URL      string            `json:"url,omitempty"`
	Headers  map[string]string `json:"headers,omitempty"`
	Type     string            `json:"type,omitempty"`
	Disabled bool              `json:"disabled"`
	Raw      json.RawMessage   `json:"raw"`
}

// MCPConfigPath is the user-level MCP config file.
func (c *CLI) MCPConfigPath() string { return filepath.Join(c.Gemini, "config", "mcp_config.json") }

// MCPServers parses mcp_config.json (comments and trailing commas allowed).
func (c *CLI) MCPServers() ([]MCPServer, error) {
	b, err := os.ReadFile(c.MCPConfigPath())
	if errors.Is(err, os.ErrNotExist) {
		return []MCPServer{}, nil
	}
	if err != nil {
		return nil, err
	}
	clean := Relax(b)
	var cfg struct {
		MCPServers map[string]json.RawMessage `json:"mcpServers"`
	}
	if len(bytes.TrimSpace(clean)) == 0 {
		return []MCPServer{}, nil
	}
	if err := json.Unmarshal(clean, &cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", c.MCPConfigPath(), err)
	}
	out := []MCPServer{}
	for name, raw := range cfg.MCPServers {
		s := MCPServer{Name: name, Raw: raw}
		_ = json.Unmarshal(raw, &s)
		var flags map[string]any
		_ = json.Unmarshal(raw, &flags)
		if d, ok := flags["disabled"].(bool); ok {
			s.Disabled = d
		}
		if e, ok := flags["enabled"].(bool); ok && !e {
			s.Disabled = true
		}
		s.Name = name
		out = append(out, s)
	}
	return out, nil
}

var lineComment = regexp.MustCompile(`(?m)^\s*//.*$`)
var trailingComma = regexp.MustCompile(`,(\s*[}\]])`)

// Relax strips // comments, /* */ comments and trailing commas (JSONC → JSON).
func Relax(b []byte) []byte {
	var out []byte
	inStr, esc := false, false
	for i := 0; i < len(b); i++ {
		ch := b[i]
		if inStr {
			out = append(out, ch)
			if esc {
				esc = false
			} else if ch == '\\' {
				esc = true
			} else if ch == '"' {
				inStr = false
			}
			continue
		}
		if ch == '"' {
			inStr = true
			out = append(out, ch)
			continue
		}
		if ch == '/' && i+1 < len(b) && b[i+1] == '/' {
			for i < len(b) && b[i] != '\n' {
				i++
			}
			out = append(out, '\n')
			continue
		}
		if ch == '/' && i+1 < len(b) && b[i+1] == '*' {
			i += 2
			for i+1 < len(b) && !(b[i] == '*' && b[i+1] == '/') {
				i++
			}
			i++
			continue
		}
		out = append(out, ch)
	}
	out = trailingComma.ReplaceAll(out, []byte("$1"))
	return bytes.TrimPrefix(out, []byte("\xef\xbb\xbf"))
}

// MCPAdd adds or updates a server through the CLI.
func (c *CLI) MCPAdd(ctx context.Context, name, typ, target string, args []string, env, headers map[string]string) (*Result, error) {
	a := []string{"mcp", "add"}
	if typ != "" {
		a = append(a, "--type", typ)
	}
	for k, v := range env {
		a = append(a, "--env", k+"="+v)
	}
	for k, v := range headers {
		a = append(a, "--header", k+": "+v)
	}
	a = append(a, name)
	needSep := strings.HasPrefix(target, "-")
	for _, x := range args {
		if strings.HasPrefix(x, "-") {
			needSep = true
		}
	}
	if needSep {
		a = append(a, "--")
	}
	a = append(a, target)
	a = append(a, args...)
	return c.Run(ctx, time.Minute, a...)
}

// SettingsPath is agy's CLI settings file.
func (c *CLI) SettingsPath() string { return filepath.Join(c.DataDir, "settings.json") }

// Settings returns raw settings.json.
func (c *CLI) Settings() (string, error) {
	b, err := os.ReadFile(c.SettingsPath())
	if errors.Is(err, os.ErrNotExist) {
		return "{}", nil
	}
	return string(b), err
}

// SaveSettings validates and writes settings.json, keeping a backup.
func (c *CLI) SaveSettings(text string) error {
	clean := Relax([]byte(text))
	var v map[string]any
	if err := json.Unmarshal(clean, &v); err != nil {
		return fmt.Errorf("settings.json must be a JSON object: %w", err)
	}
	p := c.SettingsPath()
	if old, err := os.ReadFile(p); err == nil {
		_ = os.WriteFile(p+".agentc-backup", old, 0o600)
	}
	pretty, _ := json.MarshalIndent(v, "", "  ")
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, append(pretty, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// SetSetting updates one top-level key (nil value deletes it).
func (c *CLI) SetSetting(key string, value any) error {
	text, err := c.Settings()
	if err != nil {
		return err
	}
	var v map[string]any
	if err := json.Unmarshal(Relax([]byte(text)), &v); err != nil || v == nil {
		v = map[string]any{}
	}
	if value == nil {
		delete(v, key)
	} else {
		v[key] = value
	}
	b, _ := json.Marshal(v)
	return c.SaveSettings(string(b))
}

// ConfigFile is a well-known agy configuration file.
type ConfigFile struct {
	Name   string `json:"name"`
	Path   string `json:"path"`
	Desc   string `json:"desc"`
	Exists bool   `json:"exists"`
	Size   int64  `json:"size"`
	Dir    bool   `json:"dir,omitempty"`
}

// ConfigFiles lists agy's user-level config files and folders.
func (c *CLI) ConfigFiles() []ConfigFile {
	cfg := filepath.Join(c.Gemini, "config")
	files := []ConfigFile{
		{Name: "settings.json", Path: c.SettingsPath(), Desc: "CLI settings (model, agent mode, permissions, UI)"},
		{Name: "GEMINI.md (global rules)", Path: filepath.Join(c.Gemini, "GEMINI.md"), Desc: "Global instructions for every conversation"},
		{Name: "AGENTS.md (global rules)", Path: filepath.Join(c.Gemini, "AGENTS.md"), Desc: "Global agent instructions"},
		{Name: "mcp_config.json", Path: c.MCPConfigPath(), Desc: "MCP servers"},
		{Name: "config.json", Path: filepath.Join(cfg, "config.json"), Desc: "Shared config: plugins, permission rules"},
		{Name: "hooks.json", Path: filepath.Join(cfg, "hooks.json"), Desc: "Lifecycle hooks"},
		{Name: "keybindings.json", Path: filepath.Join(c.DataDir, "keybindings.json"), Desc: "TUI key bindings"},
		{Name: "skills/", Path: filepath.Join(cfg, "skills"), Desc: "Global skills (each SKILL.md becomes a /command)", Dir: true},
		{Name: "agents/", Path: filepath.Join(cfg, "agents"), Desc: "Custom agents", Dir: true},
		{Name: "plugins/", Path: filepath.Join(cfg, "plugins"), Desc: "Installed plugins", Dir: true},
		{Name: "projects/", Path: filepath.Join(cfg, "projects"), Desc: "Project definitions and permission grants", Dir: true},
		{Name: "built-in skills/", Path: filepath.Join(c.DataDir, "builtin", "skills"), Desc: "Skills shipped with agy (read-only)", Dir: true},
		{Name: "conversations/", Path: filepath.Join(c.DataDir, "conversations"), Desc: "Conversation databases", Dir: true},
		{Name: "brain/", Path: filepath.Join(c.DataDir, "brain"), Desc: "Per-conversation artifacts (plans, tasks, media)", Dir: true},
		{Name: "logs/", Path: filepath.Join(c.DataDir, "log"), Desc: "CLI and ACP logs", Dir: true},
	}
	for i := range files {
		if st, err := os.Stat(files[i].Path); err == nil {
			files[i].Exists = true
			files[i].Size = st.Size()
		}
	}
	return files
}

// Skill is a discovered SKILL.md.
type Skill struct {
	Name   string `json:"name"`
	Desc   string `json:"description"`
	Path   string `json:"path"`
	Source string `json:"source"`
}

var fmName = regexp.MustCompile(`(?m)^name:\s*["']?([^"'\n]+)`)
var fmDesc = regexp.MustCompile(`(?m)^description:\s*["']?([^\n]+?)["']?\s*$`)

// Skills discovers skills globally, built in, and in a workspace's .agents dir.
func (c *CLI) Skills(workspace string) []Skill {
	var out []Skill
	scan := func(dir, source string) {
		matches, _ := filepath.Glob(filepath.Join(dir, "*", "SKILL.md"))
		for _, m := range matches {
			b, err := os.ReadFile(m)
			if err != nil {
				continue
			}
			s := Skill{Name: filepath.Base(filepath.Dir(m)), Path: m, Source: source}
			head := string(b)
			if len(head) > 4000 {
				head = head[:4000]
			}
			if mm := fmName.FindStringSubmatch(head); mm != nil {
				s.Name = strings.TrimSpace(mm[1])
			}
			if mm := fmDesc.FindStringSubmatch(head); mm != nil {
				s.Desc = strings.TrimSpace(mm[1])
			}
			out = append(out, s)
		}
	}
	scan(filepath.Join(c.Gemini, "config", "skills"), "global")
	if workspace != "" {
		scan(filepath.Join(workspace, ".agents", "skills"), "workspace")
	}
	scan(filepath.Join(c.DataDir, "builtin", "skills"), "built-in")
	return out
}
