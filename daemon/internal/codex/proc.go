package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"agentc/internal/acp"
)

const MinCodexVersion = "0.172.0"

var semverRe = regexp.MustCompile(`(\d+)\.(\d+)\.(\d+)`)

// ResolveBinary locates the codex CLI executable in precedence order:
// configured -> AGENTC_CODEX_PATH -> ~/.local/bin/codex -> PATH -> "codex".
func ResolveBinary(configured, home string) string {
	if c := expandTilde(configured, home); c != "" {
		return c
	}
	if envBin := expandTilde(os.Getenv("AGENTC_CODEX_PATH"), home); envBin != "" {
		return envBin
	}
	if home != "" {
		local := filepath.Join(home, ".local", "bin", "codex")
		if st, err := os.Stat(local); err == nil && !st.IsDir() && st.Mode()&0o111 != 0 {
			return local
		}
	}
	if p, err := exec.LookPath("codex"); err == nil {
		return p
	}
	return "codex"
}

func expandTilde(p, home string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	if p == "~" && home != "" {
		return home
	}
	if strings.HasPrefix(p, "~/") && home != "" {
		return filepath.Join(home, p[2:])
	}
	return p
}

// CheckBinaryVersion runs `<bin> --version` and verifies >= 0.172.0 (or non-semver custom mock scripts in tests).
func CheckBinaryVersion(ctx context.Context, bin string, env []string) (resolvedPath, version string, versionOK bool, err error) {
	if strings.TrimSpace(bin) == "" {
		return "", "", false, errors.New("no codex binary configured")
	}
	resolved := bin
	if !strings.Contains(bin, "/") {
		p, lerr := exec.LookPath(bin)
		if lerr != nil {
			return "", "", false, lerr
		}
		resolved = p
	} else if st, serr := os.Stat(bin); serr != nil || st.IsDir() {
		if serr != nil {
			return "", "", false, serr
		}
		return "", "", false, fmt.Errorf("%s is a directory", bin)
	}

	cctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cctx, resolved, "--version")
	if len(env) > 0 {
		cmd.Env = env
	}
	out, cerr := cmd.CombinedOutput()
	if cerr != nil {
		return resolved, "", false, cerr
	}
	ver := strings.TrimSpace(string(out))
	if i := strings.IndexByte(ver, '\n'); i >= 0 {
		ver = strings.TrimSpace(ver[:i])
	}
	return resolved, ver, IsVersionSupported(ver), nil
}

// IsVersionSupported returns true if version string has semver >= 0.172.0,
// or if version has no semver digits (e.g., hermetic mock test binaries).
func IsVersionSupported(ver string) bool {
	m := semverRe.FindStringSubmatch(ver)
	if len(m) != 4 {
		return strings.TrimSpace(ver) != ""
	}
	maj, _ := strconv.Atoi(m[1])
	min, _ := strconv.Atoi(m[2])
	pat, _ := strconv.Atoi(m[3])
	if maj > 0 {
		return true
	}
	if min > 172 {
		return true
	}
	if min == 172 && pat >= 0 {
		return true
	}
	return false
}

// EnsureCodexHome prepares <codexHome> outside /tmp (0700) and symlinks/copies
// ~/.codex/auth.json if present so native OpenAI OAuth also works seamlessly.
func EnsureCodexHome(codexHome, userHome string) error {
	if strings.TrimSpace(codexHome) == "" {
		return nil
	}
	if err := os.MkdirAll(codexHome, 0o700); err != nil {
		return err
	}
	if userHome == "" {
		return nil
	}
	userCodex := filepath.Join(userHome, ".codex")
	if filepath.Clean(codexHome) == filepath.Clean(userCodex) {
		return nil
	}
	if realHome, e1 := filepath.EvalSymlinks(codexHome); e1 == nil {
		if realUserCodex, e2 := filepath.EvalSymlinks(userCodex); e2 == nil && realHome == realUserCodex {
			return nil
		}
	}
	dstAuth := filepath.Join(codexHome, "auth.json")
	srcAuth := filepath.Join(userCodex, "auth.json")
	if filepath.Clean(dstAuth) == filepath.Clean(srcAuth) {
		return nil
	}
	if realSrc, e1 := filepath.EvalSymlinks(srcAuth); e1 == nil {
		if realDst, e2 := filepath.EvalSymlinks(dstAuth); e2 == nil && realSrc == realDst {
			return nil
		}
	}
	if _, err := os.Lstat(dstAuth); err == nil {
		return nil
	}
	st, err := os.Stat(srcAuth)
	if err != nil || st.IsDir() {
		return nil
	}
	if err := os.Symlink(srcAuth, dstAuth); err == nil {
		return nil
	}
	if b, rerr := os.ReadFile(srcAuth); rerr == nil {
		_ = os.WriteFile(dstAuth, b, 0o600)
	}
	return nil
}

// OpenRouterConfigOverrides returns the `-c` arguments configuring Codex to use
// agentc's loopback OpenRouter Responses bridge and generated model_catalog_json.
func OpenRouterConfigOverrides(bridgeBaseURL, catalogPath string) []string {
	var args []string
	args = append(args,
		"-c", `model_provider="openrouter"`,
		"-c", fmt.Sprintf(`model_providers.openrouter={name="OpenRouter",base_url=%q,wire_api="responses",env_key="AGENTC_BRIDGE_TOKEN"}`, bridgeBaseURL),
	)
	if strings.TrimSpace(catalogPath) != "" {
		args = append(args, "-c", fmt.Sprintf(`model_catalog_json=%q`, catalogPath))
	}
	args = append(args,
		"-c", "features.multi_agent=false",
		"-c", "features.goals=false",
		"-c", `web_search="disabled"`,
	)
	return args
}

// BuildAppServerArgs constructs the full argument vector for `codex app-server`.
func BuildAppServerArgs(useOpenRouter bool, bridgeBaseURL, catalogPath string, extraArgs []string) []string {
	args := []string{"-c", "thread_unload_delay_secs=0"}
	if useOpenRouter && bridgeBaseURL != "" {
		args = append(args, OpenRouterConfigOverrides(bridgeBaseURL, catalogPath)...)
	}
	for _, a := range extraArgs {
		t := strings.TrimSpace(a)
		if t == "" || t == "acp" || t == "app-server" {
			continue
		}
		args = append(args, a)
	}
	args = append(args, "app-server")
	return args
}

// BuildChildEnv strips any raw OPENROUTER_API_KEY from child process environment
// when using the loopback bridge (passing only AGENTC_BRIDGE_TOKEN and CODEX_HOME).
func BuildChildEnv(baseEnv []string, codexHome string, useOpenRouter bool, bridgeToken string) []string {
	var out []string
	for _, kv := range baseEnv {
		k := kv
		if i := strings.IndexByte(kv, '='); i >= 0 {
			k = kv[:i]
		}
		if k == "CODEX_HOME" && codexHome != "" {
			continue
		}
		if k == "AGENTC_BRIDGE_TOKEN" {
			continue
		}
		if useOpenRouter && (k == "OPENROUTER_API_KEY" || k == "OPENROUTER_API_KEY_FILE") {
			continue
		}
		out = append(out, kv)
	}
	if codexHome != "" {
		out = append(out, "CODEX_HOME="+codexHome)
	}
	if useOpenRouter && bridgeToken != "" {
		out = append(out, "AGENTC_BRIDGE_TOKEN="+bridgeToken)
	}
	return out
}

// ProcConfig configures one `codex app-server` subprocess.
type ProcConfig struct {
	Bin           string
	Cwd           string
	CodexHome     string
	UserHome      string
	Env           []string
	UseOpenRouter bool
	BridgeBaseURL string
	BridgeToken   string
	CatalogPath   string
	ExtraArgs     []string
	Handler       acp.Handler
	Trace         func(dir string, line []byte)
}

// AppServerProc manages a persistent `codex app-server` child process and its stdio JSON-RPC connection.
type AppServerProc struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	conn   *acp.Conn
	done   chan struct{}
	mu     sync.Mutex
	err    error
	stderr boundedBuffer
}

type boundedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
	max int
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.max <= 0 {
		b.max = 64 * 1024
	}
	if b.buf.Len()+len(p) > b.max {
		drop := (b.buf.Len() + len(p)) - b.max
		if drop >= b.buf.Len() {
			b.buf.Reset()
		} else {
			rem := append([]byte(nil), b.buf.Bytes()[drop:]...)
			b.buf.Reset()
			b.buf.Write(rem)
		}
	}
	return b.buf.Write(p)
}

func (b *boundedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// StartAppServer launches `codex app-server`, runs `initialize` (experimentalApi: true) + `initialized`,
// and returns the live AppServerProc.
func StartAppServer(ctx context.Context, cfg ProcConfig) (*AppServerProc, error) {
	if err := EnsureCodexHome(cfg.CodexHome, cfg.UserHome); err != nil {
		return nil, fmt.Errorf("codex home: %w", err)
	}
	bin := ResolveBinary(cfg.Bin, cfg.UserHome)
	args := BuildAppServerArgs(cfg.UseOpenRouter, cfg.BridgeBaseURL, cfg.CatalogPath, cfg.ExtraArgs)

	cmd := exec.Command(bin, args...)
	if cfg.Cwd != "" {
		cmd.Dir = cfg.Cwd
	}
	baseEnv := cfg.Env
	if len(baseEnv) == 0 {
		baseEnv = os.Environ()
	}
	cmd.Env = BuildChildEnv(baseEnv, cfg.CodexHome, cfg.UseOpenRouter, cfg.BridgeToken)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, err
	}
	p := &AppServerProc{
		cmd:    cmd,
		stdin:  stdin,
		done:   make(chan struct{}),
		stderr: boundedBuffer{max: 64 * 1024},
	}
	cmd.Stderr = &p.stderr

	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("start codex app-server (%s): %w", bin, err)
	}

	p.conn = acp.NewConn(stdout, stdin, cfg.Handler)
	p.conn.Trace = cfg.Trace

	go func() {
		werr := cmd.Wait()
		p.mu.Lock()
		p.err = werr
		p.mu.Unlock()
		close(p.done)
	}()

	initCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	initParams := map[string]any{
		"clientInfo": map[string]any{
			"name":    "agentc",
			"title":   "Agent Companion",
			"version": "0.6.0",
		},
		"capabilities": map[string]any{
			"experimentalApi": true,
		},
	}
	var initRes map[string]any
	if err := p.conn.Call(initCtx, "initialize", initParams, &initRes); err != nil {
		stderrMsg := strings.TrimSpace(p.stderr.String())
		p.Close(2 * time.Second)
		if stderrMsg != "" {
			return nil, fmt.Errorf("codex initialize failed: %w (%s)", err, stderrMsg)
		}
		return nil, fmt.Errorf("codex initialize failed: %w", err)
	}
	if err := p.conn.Notify("initialized", map[string]any{}); err != nil {
		p.Close(2 * time.Second)
		return nil, fmt.Errorf("codex initialized notify: %w", err)
	}
	return p, nil
}

func (p *AppServerProc) Conn() *acp.Conn { return p.conn }

func (p *AppServerProc) Pid() int {
	if p == nil || p.cmd == nil || p.cmd.Process == nil {
		return 0
	}
	return p.cmd.Process.Pid
}

func (p *AppServerProc) Alive() bool {
	if p == nil {
		return false
	}
	select {
	case <-p.done:
		return false
	default:
		return true
	}
}

func (p *AppServerProc) Done() <-chan struct{} { return p.done }

func (p *AppServerProc) ExitErr() error {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.err != nil {
		if s := strings.TrimSpace(p.stderr.String()); s != "" {
			return fmt.Errorf("%w: %s", p.err, lastLine(s))
		}
	}
	return p.err
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) == 0 {
		return s
	}
	return strings.TrimSpace(lines[len(lines)-1])
}

func (p *AppServerProc) Stderr() string {
	if p == nil {
		return ""
	}
	return p.stderr.String()
}

func (p *AppServerProc) RSSKiB() int64 {
	pid := p.Pid()
	if pid <= 0 {
		return 0
	}
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, "VmRSS:") {
			f := strings.Fields(line)
			if len(f) >= 2 {
				v, _ := strconv.ParseInt(f[1], 10, 64)
				return v
			}
		}
	}
	return 0
}

func (p *AppServerProc) Close(grace time.Duration) {
	if p == nil {
		return
	}
	_ = p.stdin.Close()
	pid := p.Pid()
	if pid > 0 {
		_ = syscall.Kill(-pid, syscall.SIGTERM)
	}
	if grace <= 0 {
		grace = 2 * time.Second
	}
	select {
	case <-p.done:
		return
	case <-time.After(grace):
	}
	if pid > 0 {
		_ = syscall.Kill(-pid, syscall.SIGKILL)
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
	select {
	case <-p.done:
	case <-time.After(time.Second):
	}
}

// RunControlCall executes a short-lived control RPC against `codex app-server`
// for sleeping threads (e.g. thread/fork, thread/revert, thread/name/set, thread/archive, thread/delete, model/list).
func RunControlCall(ctx context.Context, cfg ProcConfig, method string, params any, out any) error {
	p, err := StartAppServer(ctx, cfg)
	if err != nil {
		return err
	}
	defer p.Close(2 * time.Second)
	cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	return p.Conn().Call(cctx, method, params, out)
}

type noopHandler struct{}

func (noopHandler) HandleNotification(string, json.RawMessage) {}
func (noopHandler) HandleRequest(c *acp.Conn, id json.RawMessage, _ string, _ json.RawMessage) {
	_ = c.ReplyError(id, -32601, "not supported in control mode")
}
