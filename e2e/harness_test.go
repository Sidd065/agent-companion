package e2e_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

const (
	repoRootOnDevBox        = "/home/siddharth/agy-comp-app"
	liveOpenRouterKeyPath   = "/home/siddharth/agy-comp-app/.open_router_api_key"
	hermeticSharedSecret    = "sk-or-v1-e2e-shared-secret-9988776655"
	defaultProcRoot         = "/proc"
	expectedCloudflaredComm = "cloudflared"
)

var (
	realHostHome  string
	suiteRoot     string
	ownSuiteRoot  bool
	suiteBinDir   string
	agentcBin     string
	fakeacpBin    string
	fakecodexBin  string
	suiteBuildErr error
	suiteMockOR   *MockOpenRouterServer

	dirSeq atomic.Int64
	ipSeq  atomic.Int64

	sharedDaemonOnce sync.Once
	sharedDaemonInst *TestDaemon
	sharedDaemonErr  error

	protectedCloudflaredSnap []cloudflaredProcSnapshot
	protectedCloudflaredErr  error
)

func TestMain(m *testing.M) {
	realHostHome = os.Getenv("HOME")
	if realHostHome == "" {
		realHostHome = "/home/siddharth"
	}

	suiteRoot = os.Getenv("AGENTC_E2E_ROOT")
	if suiteRoot == "" {
		b := make([]byte, 4)
		_, _ = rand.Read(b)
		suiteRoot = filepath.Join(realHostHome, ".cache", "agce2e", hex.EncodeToString(b))
		ownSuiteRoot = true
	}
	_ = os.MkdirAll(filepath.Join(suiteRoot, "tmp"), 0o700)
	_ = os.MkdirAll(filepath.Join(suiteRoot, "bin"), 0o755)
	_ = os.Setenv("TMPDIR", filepath.Join(suiteRoot, "tmp"))

	suiteBinDir = filepath.Join(suiteRoot, "bin")
	agentcBin = filepath.Join(suiteBinDir, "agentc")
	fakeacpBin = filepath.Join(suiteBinDir, "fakeacp")
	fakecodexBin = filepath.Join(suiteBinDir, "fakecodex")

	protectedCloudflaredSnap, protectedCloudflaredErr = snapshotProtectedCloudflared(defaultProcRoot)

	var mockErr error
	suiteMockOR, mockErr = NewMockOpenRouterServer(filepath.Join(suiteRoot, "mock-ca.pem"))
	suiteBuildErr = buildSuiteBinaries()
	if suiteBuildErr == nil && mockErr != nil {
		suiteBuildErr = fmt.Errorf("start MockOpenRouterServer: %w", mockErr)
	}

	code := m.Run()

	if sharedDaemonInst != nil {
		sharedDaemonInst.Stop()
	}
	if suiteMockOR != nil {
		suiteMockOR.Close()
	}
	killAllTmuxUnder(suiteRoot)
	if protectedCloudflaredErr != nil {
		fmt.Fprintf(os.Stderr, "FATAL: pre-suite protected cloudflared snapshot failed: %v\n", protectedCloudflaredErr)
		code = 1
	} else if err := verifyProtectedCloudflaredSnapshot(defaultProcRoot, protectedCloudflaredSnap, true); err != nil {
		fmt.Fprintf(os.Stderr, "FATAL: post-suite protected cloudflared verification failed: %v\n", err)
		code = 1
	}
	if ownSuiteRoot && suiteRoot != "" {
		_ = os.RemoveAll(suiteRoot)
		_ = os.Remove(filepath.Join(realHostHome, ".cache", "agce2e"))
	}
	os.Exit(code)
}

func buildSuiteBinaries() error {
	daemonDir := filepath.Join(repoRootOnDevBox, "daemon")
	e2eDir := filepath.Join(repoRootOnDevBox, "e2e")
	raceFlag := os.Getenv("AGENTC_E2E_RACE") == "1"

	buildArgs := func(outBin, pkg string) []string {
		args := []string{"build"}
		if raceFlag {
			args = append(args, "-race")
		}
		return append(args, "-o", outBin, pkg)
	}

	if out, err := runBuildCmd(daemonDir, "go", buildArgs(agentcBin, ".")...); err != nil {
		return fmt.Errorf("build agentc: %w\n%s", err, out)
	}
	if out, err := runBuildCmd(daemonDir, "go", buildArgs(fakeacpBin, "./cmd/fakeacp")...); err != nil {
		return fmt.Errorf("build fakeacp: %w\n%s", err, out)
	}
	if out, err := runBuildCmd(e2eDir, "go", buildArgs(fakecodexBin, "./cmd/fakecodex")...); err != nil {
		return fmt.Errorf("build fakecodex: %w\n%s", err, out)
	}
	return nil
}

func runBuildCmd(dir string, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func requireSuiteReady(t *testing.T) {
	t.Helper()
	if suiteBuildErr != nil {
		t.Fatalf("suite binary/mock bootstrap failed: %v", suiteBuildErr)
	}
}

func killAllTmuxUnder(root string) {
	if root == "" {
		return
	}
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && d.Name() == "tmux.sock" {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			_ = exec.CommandContext(ctx, "tmux", "-S", p, "kill-server").Run()
			cancel()
		}
		return nil
	})
}

func nextDistinctTestIP() string {
	n := ipSeq.Add(1)
	b3 := (n / 250) % 250
	b4 := (n % 250) + 1
	return fmt.Sprintf("198.51.%d.%d", b3, b4)
}

type DaemonOpts struct {
	ExtraArgs              []string
	Env                    map[string]string
	ConfigOverrides        map[string]any
	DefaultSkipPermissions *bool
	AllowRealHostAgents    bool
	UseRealCodex           bool
	OmitCodexFromPath      bool
	OmitAgentcCodexEnv     bool
	DisableMockOpenRouter  bool
	MockOR                 *MockOpenRouterServer
	EnableFakeACP          bool
}

type TestDaemon struct {
	RootDir    string
	HomeDir    string
	ConfigHome string
	StateHome  string
	DataHome   string
	CacheHome  string
	RuntimeDir string
	CodexBin   string
	FakeACPDir string

	SockPath   string
	TmuxSock   string
	ListenAddr string
	HTTPBase   string
	WSBase     string
	Unix       *UnixLocalClient

	mu      sync.Mutex
	cmd     *exec.Cmd
	doneCh  chan struct{}
	waitErr error
	logBuf  bytes.Buffer
	stopped bool
	lastEnv []string
}

func allocateDaemonDirs() (*TestDaemon, error) {
	seq := dirSeq.Add(1)
	root := filepath.Join(suiteRoot, fmt.Sprintf("d%d", seq))
	td := &TestDaemon{
		RootDir:    root,
		HomeDir:    filepath.Join(root, "h"),
		ConfigHome: filepath.Join(root, "c"),
		StateHome:  filepath.Join(root, "s"),
		DataHome:   filepath.Join(root, "d"),
		CacheHome:  filepath.Join(root, "k"),
		RuntimeDir: filepath.Join(root, "r"),
		CodexBin:   filepath.Join(root, "cx"),
		FakeACPDir: filepath.Join(root, "fa"),
	}
	td.SockPath = filepath.Join(td.RuntimeDir, "agentc", "agentc.sock")
	td.TmuxSock = filepath.Join(td.RuntimeDir, "agentc", "tmux.sock")

	if len(td.SockPath) >= 104 {
		return nil, fmt.Errorf("socket path %q (%d chars) exceeds 104-byte safety budget", td.SockPath, len(td.SockPath))
	}
	for _, dir := range []string{
		td.HomeDir,
		filepath.Join(td.ConfigHome, "agentc"),
		filepath.Join(td.StateHome, "agentc"),
		filepath.Join(td.DataHome, "agentc"),
		td.CacheHome,
		td.RuntimeDir,
		td.CodexBin,
		td.FakeACPDir,
	} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, err
		}
	}
	gitCfg := "[user]\n\tname = AgentC E2E\n\temail = e2e@example.com\n"
	if err := os.WriteFile(filepath.Join(td.HomeDir, ".gitconfig"), []byte(gitCfg), 0o600); err != nil {
		return nil, err
	}
	td.Unix = NewUnixLocalClient(td.SockPath)
	return td, nil
}

func TryStartDaemon(opts DaemonOpts) (*TestDaemon, string, error) {
	td, err := allocateDaemonDirs()
	if err != nil {
		return nil, "", err
	}

	skipPerms := true
	if opts.DefaultSkipPermissions != nil {
		skipPerms = *opts.DefaultSkipPermissions
	}
	cfgMap := map[string]any{
		"tunnel":                 "none",
		"listen":                 "127.0.0.1:0",
		"defaultSkipPermissions": skipPerms,
		"maxLive":                12,
	}
	if !opts.AllowRealHostAgents {
		cfgMap["agyPath"] = "nonexistent-agy-e2e"
	}
	for k, v := range opts.ConfigOverrides {
		cfgMap[k] = v
	}
	cfgBytes, _ := json.MarshalIndent(cfgMap, "", "  ")
	cfgFile := filepath.Join(td.ConfigHome, "agentc", "config.json")
	if err := os.WriteFile(cfgFile, cfgBytes, 0o600); err != nil {
		return td, "", err
	}

	if !opts.UseRealCodex && !opts.OmitCodexFromPath {
		_ = os.Symlink(fakecodexBin, filepath.Join(td.CodexBin, "codex"))
	}

	out, err := td.startProcess(opts)
	if err != nil {
		td.Stop()
		return td, out, err
	}
	return td, out, nil
}

func (td *TestDaemon) buildEnv(opts DaemonOpts) []string {
	var pathParts []string
	if !opts.UseRealCodex && !opts.OmitCodexFromPath {
		pathParts = append(pathParts, td.CodexBin)
	}
	pathParts = append(pathParts,
		filepath.Join(realHostHome, ".local", "bin"),
		filepath.Join(realHostHome, ".local", "go", "bin"),
		"/usr/local/bin",
		"/usr/bin",
		"/bin",
	)

	hostUser := os.Getenv("USER")
	if hostUser == "" {
		hostUser = "siddharth"
	}
	envMap := map[string]string{
		"HOME":              td.HomeDir,
		"XDG_CONFIG_HOME":   td.ConfigHome,
		"XDG_STATE_HOME":    td.StateHome,
		"XDG_DATA_HOME":     td.DataHome,
		"XDG_CACHE_HOME":    td.CacheHome,
		"XDG_RUNTIME_DIR":   td.RuntimeDir,
		"TMPDIR":            filepath.Join(suiteRoot, "tmp"),
		"PATH":              strings.Join(pathParts, ":"),
		"FAKEACP_STATE_DIR": td.FakeACPDir,
		"TERM":              "xterm-256color",
		"LANG":              "C.UTF-8",
		"LC_CTYPE":          "C.UTF-8",
		"SHELL":             "/bin/bash",
		"USER":              hostUser,
	}
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "GORACE=") {
			envMap["GORACE"] = strings.TrimPrefix(kv, "GORACE=")
		}
	}
	if opts.EnableFakeACP {
		envMap["AGENTC_FAKE_ACP"] = fakeacpBin
	}
	if !opts.UseRealCodex && !opts.OmitAgentcCodexEnv && !opts.OmitCodexFromPath {
		envMap["AGENTC_CODEX_PATH"] = fakecodexBin
	}
	if !opts.DisableMockOpenRouter {
		mor := opts.MockOR
		if mor == nil {
			mor = suiteMockOR
		}
		if mor != nil {
			envMap["HTTPS_PROXY"] = mor.ProxyURL()
			envMap["NO_PROXY"] = "127.0.0.1,localhost"
			envMap["SSL_CERT_FILE"] = mor.CACertPath()
			envMap["OPENROUTER_BASE_URL"] = mor.BaseAPIURL()
			envMap["AGENTC_OPENROUTER_BASE_URL"] = mor.BaseAPIURL()
		}
	}
	for k, v := range opts.Env {
		if v == "" {
			delete(envMap, k)
		} else {
			envMap[k] = v
		}
	}

	out := make([]string, 0, len(envMap))
	for k, v := range envMap {
		out = append(out, k+"="+v)
	}
	return out
}

func (td *TestDaemon) startProcess(opts DaemonOpts) (string, error) {
	args := []string{"serve", "-listen", "127.0.0.1:0"}
	args = append(args, opts.ExtraArgs...)

	env := td.buildEnv(opts)
	cmd := exec.Command(agentcBin, args...)
	cmd.Dir = td.HomeDir
	cmd.Env = env

	td.mu.Lock()
	td.logBuf.Reset()
	cmd.Stdout = &syncBufferWriter{mu: &td.mu, buf: &td.logBuf}
	cmd.Stderr = &syncBufferWriter{mu: &td.mu, buf: &td.logBuf}
	td.cmd = cmd
	td.stopped = false
	td.lastEnv = env
	doneCh := make(chan struct{})
	td.doneCh = doneCh
	td.waitErr = nil
	td.mu.Unlock()

	if err := cmd.Start(); err != nil {
		return "", err
	}
	go func() {
		wErr := cmd.Wait()
		td.mu.Lock()
		td.waitErr = wErr
		td.mu.Unlock()
		close(doneCh)
	}()

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-doneCh:
			td.mu.Lock()
			wErr := td.waitErr
			td.mu.Unlock()
			logs := td.Logs()
			return logs, fmt.Errorf("agentc serve exited early (%v): %s", wErr, strings.TrimSpace(logs))
		default:
		}

		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		info, _, err := td.Unix.StatusRaw(ctx)
		cancel()
		if err == nil {
			if rawListen, ok := info["listen"].([]any); ok && len(rawListen) > 0 {
				if addr, ok := rawListen[0].(string); ok && addr != "" {
					td.ListenAddr = addr
					td.HTTPBase = "http://" + addr
					td.WSBase = "ws://" + addr
					return td.Logs(), nil
				}
			}
		}
		time.Sleep(35 * time.Millisecond)
	}
	logs := td.Logs()
	return logs, fmt.Errorf("timed out waiting for agentc serve readiness on %s; logs:\n%s", td.SockPath, logs)
}

type syncBufferWriter struct {
	mu  *sync.Mutex
	buf *bytes.Buffer
}

func (w *syncBufferWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Write(p)
}

func (td *TestDaemon) Logs() string {
	td.mu.Lock()
	s := td.logBuf.String()
	td.mu.Unlock()
	logFile := filepath.Join(td.StateHome, "agentc", "agentc.log")
	if b, err := os.ReadFile(logFile); err == nil && len(b) > 0 {
		return s + "\n" + string(b)
	}
	return s
}

func (td *TestDaemon) StopProcessOnly() {
	td.mu.Lock()
	cmd := td.cmd
	doneCh := td.doneCh
	already := td.stopped
	td.stopped = true
	td.mu.Unlock()

	if already || cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-doneCh:
	case <-time.After(4 * time.Second):
		_ = cmd.Process.Kill()
		select {
		case <-doneCh:
		case <-time.After(1 * time.Second):
		}
	}
	_ = os.Remove(td.SockPath)
}

func (td *TestDaemon) Stop() {
	td.StopProcessOnly()
	if _, err := os.Stat(td.TmuxSock); err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_ = exec.CommandContext(ctx, "tmux", "-S", td.TmuxSock, "kill-server").Run()
		cancel()
	}
	_ = os.RemoveAll(td.RootDir)
}

func (td *TestDaemon) Restart(t *testing.T, opts DaemonOpts) {
	t.Helper()
	td.StopProcessOnly()
	if out, err := td.startProcess(opts); err != nil {
		t.Fatalf("failed to restart daemon: %v\n%s", err, out)
	}
}

func StartIsolatedDaemon(t *testing.T, opts DaemonOpts) *TestDaemon {
	t.Helper()
	requireSuiteReady(t)
	td, logs, err := TryStartDaemon(opts)
	if err != nil {
		t.Fatalf("StartIsolatedDaemon failed: %v\nlogs:\n%s", err, logs)
	}
	t.Cleanup(func() {
		td.Stop()
	})
	return td
}

func sharedDaemon(t *testing.T) *TestDaemon {
	t.Helper()
	requireSuiteReady(t)
	sharedDaemonOnce.Do(func() {
		td, _, err := TryStartDaemon(DaemonOpts{
			EnableFakeACP: true,
			Env: map[string]string{
				"OPENROUTER_API_KEY": hermeticSharedSecret,
			},
		})
		if err != nil {
			sharedDaemonErr = err
			return
		}
		sharedDaemonInst = td
	})
	if sharedDaemonErr != nil {
		t.Fatalf("sharedDaemon startup failed: %v", sharedDaemonErr)
	}
	return sharedDaemonInst
}

func (td *TestDaemon) PairAndConnect(t *testing.T, deviceName, platform string) *NoiseRPCClient {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, link, err := td.Unix.Pair(ctx, td.HTTPBase, 300)
	if err != nil {
		t.Fatalf("Unix.Pair failed: %v", err)
	}
	devKey, err := GenerateDeviceKeypair()
	if err != nil {
		t.Fatalf("GenerateDeviceKeypair: %v", err)
	}
	if _, err := PairDeviceOverNoiseWS(ctx, link, devKey, deviceName, platform, nil); err != nil {
		t.Fatalf("PairDeviceOverNoiseWS failed: %v", err)
	}
	cli, err := ConnectNoiseRPCClient(ctx, td.WSBase, link.DaemonPub, devKey, deviceName, platform, nil)
	if err != nil {
		t.Fatalf("ConnectNoiseRPCClient failed: %v", err)
	}
	t.Cleanup(func() {
		_ = cli.Close()
	})
	return cli
}

func (td *TestDaemon) RunCLI(args ...string) (string, string, int) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, agentcBin, args...)
	cmd.Dir = td.HomeDir
	cmd.Env = td.lastEnv
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	code := 0
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitCode()
		} else {
			code = 1
		}
	}
	return stdout.String(), stderr.String(), code
}

func newTempGitRepo(t *testing.T, seedFiles map[string]string) string {
	t.Helper()
	requireSuiteReady(t)
	dir := filepath.Join(suiteRoot, "ws", fmt.Sprintf("r%d", dirSeq.Add(1)))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir git repo: %v", err)
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(dir)
	})

	runGit := func(args ...string) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %v\n%s", args, err, string(out))
		}
	}

	runGit("init", "-b", "main")
	runGit("config", "user.name", "E2E Tester")
	runGit("config", "user.email", "e2e@example.com")

	if len(seedFiles) == 0 {
		seedFiles = map[string]string{"README.md": "# E2E Repo\n"}
	}
	for rel, content := range seedFiles {
		fp := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(fp), 0o755); err != nil {
			t.Fatalf("mkdir seed parent: %v", err)
		}
		if err := os.WriteFile(fp, []byte(content), 0o644); err != nil {
			t.Fatalf("write seed file %s: %v", rel, err)
		}
	}
	runGit("add", "-A")
	runGit("commit", "-m", "initial commit")
	return dir
}

func newTempPlainDir(t *testing.T) string {
	t.Helper()
	requireSuiteReady(t)
	dir := filepath.Join(suiteRoot, "plain", fmt.Sprintf("p%d", dirSeq.Add(1)))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir plain dir: %v", err)
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(dir)
	})
	return dir
}

func collectAgentText(items []TranscriptItemWire) string {
	var parts []string
	for _, it := range items {
		if it.Kind == "agent" && it.Text != "" {
			parts = append(parts, it.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func collectThoughtText(items []TranscriptItemWire) string {
	var parts []string
	for _, it := range items {
		if it.Kind == "thought" && it.Text != "" {
			parts = append(parts, it.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func extractInspectJSON(items []TranscriptItemWire) (map[string]any, bool) {
	txt := collectAgentText(items)
	idx := strings.LastIndex(txt, "INSPECT_JSON:")
	if idx < 0 {
		return nil, false
	}
	sub := txt[idx+len("INSPECT_JSON:"):]
	if nl := strings.IndexByte(sub, '\n'); nl >= 0 {
		sub = sub[:nl]
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(sub)), &out); err != nil {
		return nil, false
	}
	return out, true
}

func assertZeroSecretSubstring(t *testing.T, secret string, surfaceName string, v any) {
	t.Helper()
	if strings.TrimSpace(secret) == "" {
		return
	}
	var text string
	switch tv := v.(type) {
	case string:
		text = tv
	case []byte:
		text = string(tv)
	default:
		b, _ := json.Marshal(v)
		text = string(b)
	}
	if strings.Contains(text, secret) {
		// Never print raw secret in failure output!
		t.Fatalf("SECURITY VIOLATION: raw secret value (len=%d) was leaked in %s", len(secret), surfaceName)
	}
}

func readLiveOpenRouterKeyForRedactionCheckOnly(t *testing.T) string {
	t.Helper()
	st, err := os.Stat(liveOpenRouterKeyPath)
	if err != nil || st.Size() == 0 {
		t.Fatalf("required OpenRouter API key file %s missing or empty: %v", liveOpenRouterKeyPath, err)
	}
	b, err := os.ReadFile(liveOpenRouterKeyPath)
	if err != nil {
		t.Fatalf("failed to stat/read key file %s: %v", liveOpenRouterKeyPath, err)
	}
	trimmed := strings.TrimSpace(string(b))
	if len(trimmed) < 8 {
		t.Fatalf("OpenRouter API key file %s appears malformed (len=%d)", liveOpenRouterKeyPath, len(trimmed))
	}
	return trimmed
}

func requireRealCodexBinary(t *testing.T) string {
	t.Helper()
	candidates := []string{
		filepath.Join(realHostHome, ".local", "bin", "codex"),
	}
	if p, err := exec.LookPath("codex"); err == nil && p != "" {
		candidates = append(candidates, p)
	}
	for _, c := range candidates {
		if st, err := os.Stat(c); err == nil && !st.IsDir() && st.Mode()&0o111 != 0 {
			return c
		}
	}
	t.Fatalf("real codex binary not found at %s/.local/bin/codex or on PATH", realHostHome)
	return ""
}

type AsideItemWire struct {
	ID       string `json:"id"`
	Question string `json:"question"`
	Answer   string `json:"answer"`
	Error    string `json:"error"`
	State    string `json:"state"`
}

func callAndWaitAsideDone(t *testing.T, cli *NoiseRPCClient, sid, question string, timeout time.Duration) AsideItemWire {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	var initial AsideItemWire
	if err := cli.Call(ctx, "session.aside", map[string]any{
		"id":       sid,
		"question": question,
	}, &initial); err != nil {
		t.Fatalf("session.aside RPC failed: %v", err)
	}
	if initial.State == "done" {
		if strings.TrimSpace(initial.Answer) == "" {
			t.Fatalf("session.aside returned empty answer: %+v", initial)
		}
		return initial
	}
	if initial.State == "failed" {
		t.Fatalf("session.aside failed: %s", initial.Error)
	}

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var list []AsideItemWire
		if err := cli.Call(ctx, "session.asides", map[string]any{"id": sid}, &list); err == nil {
			for _, a := range list {
				if a.ID == initial.ID && a.State != "pending" {
					if a.State != "done" || a.Error != "" || strings.TrimSpace(a.Answer) == "" {
						t.Fatalf("session.aside completed with error/empty state: %+v", a)
					}
					return a
				}
			}
		}
		time.Sleep(60 * time.Millisecond)
	}
	t.Fatalf("timed out after %v waiting for session.aside %s to finish", timeout, initial.ID)
	return AsideItemWire{}
}

type cloudflaredProcSnapshot struct {
	PID            int
	Comm           string
	StartTimeTicks string
}

func parsePgrepPIDs(raw string) ([]int, error) {
	var pids []int
	for _, line := range strings.Split(raw, "\n") {
		s := strings.TrimSpace(line)
		if s == "" {
			continue
		}
		pid, err := strconv.Atoi(s)
		if err != nil || pid <= 0 {
			return nil, fmt.Errorf("invalid cloudflared PID %q from pgrep (err=%v)", s, err)
		}
		pids = append(pids, pid)
	}
	slices.Sort(pids)
	pids = slices.Compact(pids)
	if len(pids) == 0 {
		return nil, errors.New("pgrep -x cloudflared returned 0 PIDs")
	}
	return pids, nil
}

func discoverCloudflaredPIDs(ctx context.Context) ([]int, error) {
	cmd := exec.CommandContext(ctx, "pgrep", "-x", expectedCloudflaredComm)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("pgrep -x %s failed (%w): stderr=%q", expectedCloudflaredComm, err, strings.TrimSpace(stderr.String()))
	}
	return parsePgrepPIDs(stdout.String())
}

func parseProcStatStartTime(statContent string) (string, error) {
	rparen := strings.LastIndexByte(statContent, ')')
	if rparen < 0 || rparen+1 >= len(statContent) {
		return "", fmt.Errorf("malformed /proc stat (missing ')'): %q", strings.TrimSpace(statContent))
	}
	fields := strings.Fields(statContent[rparen+1:])
	// proc(5): field 3 (state) is fields[0]; field 22 (starttime) is fields[19].
	if len(fields) <= 19 {
		return "", fmt.Errorf("short /proc stat (%d fields after comm)", len(fields))
	}
	startTicks := fields[19]
	if _, err := strconv.ParseUint(startTicks, 10, 64); err != nil {
		return "", fmt.Errorf("invalid /proc stat starttime %q: %w", startTicks, err)
	}
	return startTicks, nil
}

func readCloudflaredProcInfo(procRoot string, pid int, checkLive bool) (cloudflaredProcSnapshot, error) {
	if pid <= 0 {
		return cloudflaredProcSnapshot{}, fmt.Errorf("non-positive cloudflared PID %d", pid)
	}
	if checkLive {
		if err := syscall.Kill(pid, 0); err != nil && !errors.Is(err, syscall.EPERM) {
			return cloudflaredProcSnapshot{}, fmt.Errorf("protected cloudflared pid %d is not running: %w", pid, err)
		}
	}
	pidDir := filepath.Join(procRoot, strconv.Itoa(pid))
	commBytes, err := os.ReadFile(filepath.Join(pidDir, "comm"))
	if err != nil {
		return cloudflaredProcSnapshot{}, fmt.Errorf("read %s/comm: %w", pidDir, err)
	}
	comm := strings.TrimSpace(string(commBytes))
	if comm != expectedCloudflaredComm {
		return cloudflaredProcSnapshot{}, fmt.Errorf("expected pid %d comm %q, got %q", pid, expectedCloudflaredComm, comm)
	}
	statBytes, err := os.ReadFile(filepath.Join(pidDir, "stat"))
	if err != nil {
		return cloudflaredProcSnapshot{}, fmt.Errorf("read %s/stat: %w", pidDir, err)
	}
	startTicks, err := parseProcStatStartTime(string(statBytes))
	if err != nil {
		return cloudflaredProcSnapshot{}, fmt.Errorf("pid %d stat: %w", pid, err)
	}
	return cloudflaredProcSnapshot{
		PID:            pid,
		Comm:           comm,
		StartTimeTicks: startTicks,
	}, nil
}

func snapshotCloudflaredPIDs(procRoot string, pids []int, checkLive bool) ([]cloudflaredProcSnapshot, error) {
	if len(pids) == 0 {
		return nil, errors.New("no cloudflared PIDs to snapshot")
	}
	snaps := make([]cloudflaredProcSnapshot, 0, len(pids))
	for _, pid := range pids {
		s, err := readCloudflaredProcInfo(procRoot, pid, checkLive)
		if err != nil {
			return nil, err
		}
		snaps = append(snaps, s)
	}
	return snaps, nil
}

func snapshotProtectedCloudflared(procRoot string) ([]cloudflaredProcSnapshot, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	pids, err := discoverCloudflaredPIDs(ctx)
	if err != nil {
		return nil, err
	}
	return snapshotCloudflaredPIDs(procRoot, pids, procRoot == defaultProcRoot)
}

func verifyProtectedCloudflaredSnapshot(procRoot string, baseline []cloudflaredProcSnapshot, checkLive bool) error {
	if len(baseline) == 0 {
		return errors.New("protected cloudflared baseline snapshot is empty")
	}
	var livePIDs []int
	if checkLive {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var err error
		livePIDs, err = discoverCloudflaredPIDs(ctx)
		if err != nil {
			return fmt.Errorf("live cloudflared discovery failed during verification: %w", err)
		}
	}
	for _, exp := range baseline {
		if exp.Comm != expectedCloudflaredComm {
			return fmt.Errorf("invalid baseline comm %q for pid %d (expected %q)", exp.Comm, exp.PID, expectedCloudflaredComm)
		}
		if strings.TrimSpace(exp.StartTimeTicks) == "" {
			return fmt.Errorf("empty baseline starttime ticks for pid %d", exp.PID)
		}
		cur, err := readCloudflaredProcInfo(procRoot, exp.PID, checkLive)
		if err != nil {
			return err
		}
		if cur.StartTimeTicks != exp.StartTimeTicks {
			return fmt.Errorf("protected cloudflared pid %d restarted or was reused (starttime ticks %s -> %s)", exp.PID, exp.StartTimeTicks, cur.StartTimeTicks)
		}
		if checkLive && !slices.Contains(livePIDs, exp.PID) {
			return fmt.Errorf("protected cloudflared pid %d missing from live pgrep -x %s output %v", exp.PID, expectedCloudflaredComm, livePIDs)
		}
	}
	return nil
}

func assertProtectedCloudflaredUntouched(t *testing.T) {
	t.Helper()
	if protectedCloudflaredErr != nil {
		t.Fatalf("protected cloudflared baseline snapshot failed: %v", protectedCloudflaredErr)
	}
	if len(protectedCloudflaredSnap) == 0 {
		t.Fatalf("protected cloudflared baseline snapshot is empty")
	}
	if err := verifyProtectedCloudflaredSnapshot(defaultProcRoot, protectedCloudflaredSnap, true); err != nil {
		t.Fatalf("protected cloudflared verification failed: %v", err)
	}
}
