// Package tunnel runs a Cloudflare static named tunnel or free quick tunnel
// (cloudflared) in front of the daemon's loopback listener. The tunnel process
// is started in its own process group so it survives daemon restarts; on start
// the daemon re-adopts it when it matches the desired mode and is healthy.
package tunnel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Manager supervises one Cloudflare tunnel (static named tunnel or free quick tunnel).
type Manager struct {
	Bin         string       // cloudflared binary
	Target      string       // e.g. http://127.0.0.1:9473
	MetricsAddr string       // e.g. 127.0.0.1:9474
	StateFile   string       // ~/.local/state/agentc/tunnel.json
	TokenFile   string       // ~/.local/state/agentc/cloudflared.token (0600, static mode)
	LogFile     string       // ~/.local/state/agentc/cloudflared.log
	Mode        string       // "auto" / "" (static if configured, else quick), "static", or "quick"
	Static      StaticConfig // optional static Cloudflare auth configuration
	// OnURL is called whenever the public URL changes (including the first time).
	OnURL func(url string)

	mu          sync.Mutex
	url         string
	pid         int
	status      string
	activeMode  string // "cloudflare-static" | "cloudflare-quick"
	tunnelID    string
	tunnelName  string
	fallbackErr string
	reloadCh    chan struct{}
}

type state struct {
	PID        int    `json:"pid"`
	Mode       string `json:"mode,omitempty"` // "cloudflare-static" | "cloudflare-quick"
	Target     string `json:"target"`
	Metrics    string `json:"metrics"`
	URL        string `json:"url"`
	TunnelID   string `json:"tunnelId,omitempty"`
	TunnelName string `json:"tunnelName,omitempty"`
	TokenFile  string `json:"tokenFile,omitempty"`
}

func (m *Manager) initReloadCh() chan struct{} {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.reloadCh == nil {
		m.reloadCh = make(chan struct{}, 1)
	}
	return m.reloadCh
}

// Reconfigure updates the desired tunnel mode and static configuration and wakes Run()
// to switch tunnels cleanly without restarting the daemon.
func (m *Manager) Reconfigure(mode string, static StaticConfig) {
	m.mu.Lock()
	m.Mode = mode
	m.Static = static
	if m.reloadCh == nil {
		m.reloadCh = make(chan struct{}, 1)
	}
	ch := m.reloadCh
	m.mu.Unlock()
	select {
	case ch <- struct{}{}:
	default:
	}
}

func (m *Manager) desiredConfig() (wantStatic bool, sc StaticConfig, noFallback bool) {
	m.mu.Lock()
	mode := strings.ToLower(strings.TrimSpace(m.Mode))
	sc = m.Static
	m.mu.Unlock()
	if sc.Target == "" {
		sc.Target = m.Target
	}
	if sc.CacheFile == "" && m.StateFile != "" {
		sc.CacheFile = filepath.Join(filepath.Dir(m.StateFile), "cloudflare-tunnel.json")
	}
	switch mode {
	case "quick":
		return false, sc, false
	case "static":
		return true, sc, sc.NoFallback
	default: // "", "auto"
		if sc.Configured() {
			return true, sc, sc.NoFallback
		}
		return false, sc, false
	}
}

func (m *Manager) tokenFilePath() string {
	if m.TokenFile != "" {
		return m.TokenFile
	}
	if m.StateFile != "" {
		return filepath.Join(filepath.Dir(m.StateFile), "cloudflared.token")
	}
	return filepath.Join(os.TempDir(), fmt.Sprintf("agentc-cfd-%d.token", os.Getuid()))
}

// URL returns the current public URL ("" while starting).
func (m *Manager) URL() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.url
}

// Status describes the tunnel for `agentc status` and `daemon.info`.
func (m *Manager) Status() map[string]any {
	wantStatic, sc, _ := m.desiredConfig()
	resolved, _ := sc.Resolve()
	m.mu.Lock()
	defer m.mu.Unlock()
	mode := m.activeMode
	if mode == "" {
		if wantStatic {
			mode = "cloudflare-static"
		} else {
			mode = "cloudflare-quick"
		}
	}
	configured := "quick"
	if wantStatic {
		configured = "static"
	}
	out := map[string]any{
		"url":            m.url,
		"pid":            m.pid,
		"status":         m.status,
		"mode":           mode,
		"configuredMode": configured,
	}
	if resolved.Hostname != "" {
		out["hostname"] = resolved.Hostname
	}
	if m.tunnelID != "" {
		out["tunnelId"] = m.tunnelID
	}
	if m.tunnelName != "" {
		out["tunnelName"] = m.tunnelName
	}
	if m.fallbackErr != "" {
		out["fallbackError"] = m.fallbackErr
	}
	return out
}

func (m *Manager) set(url string, pid int, status, activeMode, tunnelID, tunnelName, fallbackErr string) {
	m.mu.Lock()
	changed := url != "" && url != m.url
	m.url = url
	m.pid = pid
	m.status = status
	if activeMode != "" {
		m.activeMode = activeMode
	}
	m.tunnelID = tunnelID
	m.tunnelName = tunnelName
	m.fallbackErr = fallbackErr
	tokFile := m.tokenFilePath()
	m.mu.Unlock()
	if url != "" && pid > 0 && m.StateFile != "" {
		_ = os.MkdirAll(filepath.Dir(m.StateFile), 0o700)
		b, _ := json.Marshal(state{
			PID: pid, Mode: activeMode, Target: m.Target, Metrics: m.MetricsAddr,
			URL: url, TunnelID: tunnelID, TunnelName: tunnelName, TokenFile: tokFile,
		})
		_ = os.WriteFile(m.StateFile, b, 0o600)
	}
	if changed && m.OnURL != nil {
		m.OnURL(url)
	}
}

// Run keeps a tunnel up until ctx is done (the tunnel process itself is left
// running on daemon shutdown so the next daemon start can adopt it seamlessly).
func (m *Manager) Run(ctx context.Context) {
	if m.Bin == "" {
		m.Bin = "cloudflared"
	}
	reloadCh := m.initReloadCh()
	backoff := 5 * time.Second
	forceRestart := false
	for ctx.Err() == nil {
		var pid int
		var url, activeMode, tunID, tunName, fallbackErr string
		if forceRestart {
			_ = m.Stop()
			forceRestart = false
		} else {
			pid, url, activeMode, tunID, tunName = m.adopt()
		}
		if pid == 0 {
			var err error
			pid, url, activeMode, tunID, tunName, fallbackErr, err = m.launchDesired(ctx)
			if err != nil {
				m.set("", 0, "error: "+err.Error(), "", "", "", fallbackErr)
				log.Printf("tunnel: %v (retrying in %s)", err, backoff)
				select {
				case <-ctx.Done():
					return
				case <-reloadCh:
					backoff = time.Second
					forceRestart = true
					continue
				case <-time.After(backoff):
				}
				backoff = min(backoff*2, 5*time.Minute)
				continue
			}
			log.Printf("tunnel: started %s (pid %d): %s", activeMode, pid, url)
		} else {
			log.Printf("tunnel: adopted running %s (pid %d): %s", activeMode, pid, url)
		}
		backoff = 5 * time.Second
		m.set(url, pid, "up", activeMode, tunID, tunName, fallbackErr)

		// Supervise while the process lives or until Reconfigure() wakes us.
		for ctx.Err() == nil {
			select {
			case <-ctx.Done():
				return
			case <-reloadCh:
				log.Printf("tunnel: reloading configuration")
				forceRestart = true
			case <-time.After(15 * time.Second):
			}
			if forceRestart {
				break
			}
			if !alive(pid) {
				log.Printf("tunnel: cloudflared (pid %d) exited; starting a new tunnel", pid)
				m.set("", 0, "restarting", activeMode, tunID, tunName, fallbackErr)
				break
			}
			if activeMode == "cloudflare-quick" {
				if h, err := m.hostname(2 * time.Second); err == nil && h != "" && "https://"+h != url {
					url = "https://" + h
					m.set(url, pid, "up", activeMode, tunID, tunName, fallbackErr)
				}
			}
		}
	}
}

func (m *Manager) launchDesired(ctx context.Context) (pid int, url, activeMode, tunID, tunName, fallbackErr string, err error) {
	wantStatic, sc, noFallback := m.desiredConfig()
	if wantStatic {
		pt, provErr := ProvisionStaticTunnel(ctx, sc)
		if provErr == nil {
			p, u, lerr := m.launchStatic(ctx, pt)
			if lerr == nil {
				return p, u, "cloudflare-static", pt.TunnelID, pt.TunnelName, "", nil
			}
			provErr = lerr
		}
		if noFallback {
			return 0, "", "cloudflare-static", "", "", provErr.Error(), provErr
		}
		fallbackErr = provErr.Error()
		log.Printf("tunnel: static Cloudflare tunnel failed (%v); falling back to free quick tunnel", provErr)
	}
	p, u, qerr := m.launchQuick(ctx)
	if qerr != nil {
		return 0, "", "cloudflare-quick", "", "", fallbackErr, qerr
	}
	return p, u, "cloudflare-quick", "", "", fallbackErr, nil
}

// Stop kills ONLY the tunnel process this daemon started (for `agentc tunnel stop`).
func (m *Manager) Stop() error {
	var st state
	b, err := os.ReadFile(m.StateFile)
	if err != nil {
		return err
	}
	if json.Unmarshal(b, &st) != nil || st.PID == 0 || !m.ours(st.PID, st.TokenFile) {
		return errors.New("no tunnel started by agentc is running")
	}
	_ = syscall.Kill(-st.PID, syscall.SIGTERM)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && alive(st.PID) {
		time.Sleep(100 * time.Millisecond)
	}
	if alive(st.PID) {
		_ = syscall.Kill(-st.PID, syscall.SIGKILL)
	}
	_ = os.Remove(m.StateFile)
	return nil
}

func (m *Manager) adopt() (int, string, string, string, string) {
	b, err := os.ReadFile(m.StateFile)
	if err != nil {
		return 0, "", "", "", ""
	}
	var st state
	if json.Unmarshal(b, &st) != nil || st.PID == 0 {
		return 0, "", "", "", ""
	}
	stMode := st.Mode
	if stMode == "" {
		stMode = "cloudflare-quick"
	}
	wantStatic, sc, _ := m.desiredConfig()
	resolved, _ := sc.Resolve()

	modeMatches := (!wantStatic && stMode == "cloudflare-quick") ||
		(wantStatic && stMode == "cloudflare-static" && (resolved.Hostname == "" || strings.EqualFold(st.URL, "https://"+resolved.Hostname)))

	if !modeMatches || st.Target != m.Target || st.Metrics != m.MetricsAddr || !alive(st.PID) || !m.ours(st.PID, st.TokenFile) {
		if st.PID != 0 && alive(st.PID) && m.ours(st.PID, st.TokenFile) {
			_ = syscall.Kill(-st.PID, syscall.SIGTERM) // config or mode changed: replace our child
			deadline := time.Now().Add(3 * time.Second)
			for time.Now().Before(deadline) && alive(st.PID) {
				time.Sleep(100 * time.Millisecond)
			}
		}
		return 0, "", "", "", ""
	}
	if stMode == "cloudflare-static" {
		if !m.staticReady(3 * time.Second) {
			return 0, "", "", "", ""
		}
		return st.PID, st.URL, stMode, st.TunnelID, st.TunnelName
	}
	h, err := m.hostname(3 * time.Second)
	if err != nil || h == "" || "https://"+h != st.URL {
		return 0, "", "", "", ""
	}
	return st.PID, st.URL, stMode, "", ""
}

// ours checks that pid is an agentc-owned cloudflared process (either our quick
// tunnel or our token-file named tunnel), so we NEVER touch unrelated system
// cloudflared processes on the machine.
func (m *Manager) ours(pid int, recordedTokenFile string) bool {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
	if err != nil {
		return false
	}
	args := strings.Split(strings.TrimRight(string(b), "\x00"), "\x00")
	if len(args) == 0 || !strings.Contains(filepath.Base(args[0]), "cloudflared") {
		return false
	}
	has := map[string]bool{}
	for _, a := range args {
		has[a] = true
	}
	if !has[m.MetricsAddr] {
		return false
	}
	if has["--url"] && has[m.Target] {
		return true
	}
	tf := firstNonEmptyStr(recordedTokenFile, m.tokenFilePath())
	if has["run"] && has["--token-file"] && tf != "" && has[tf] {
		return true
	}
	return false
}

func (m *Manager) checkBinAndMetrics() (string, error) {
	bin, err := exec.LookPath(m.Bin)
	if err != nil {
		return "", fmt.Errorf("cloudflared not found (%s): install it or set \"cloudflared\" in the config", m.Bin)
	}
	if m.metricsOccupied(time.Second) {
		return "", fmt.Errorf("something else is already serving cloudflared metrics on %s; set a different \"tunnelMetrics\" address", m.MetricsAddr)
	}
	return bin, nil
}

func (m *Manager) launchStatic(ctx context.Context, pt *ProvisionedTunnel) (int, string, error) {
	bin, err := m.checkBinAndMetrics()
	if err != nil {
		return 0, "", err
	}
	tf := m.tokenFilePath()
	if err := os.MkdirAll(filepath.Dir(tf), 0o700); err != nil {
		return 0, "", err
	}
	if err := os.WriteFile(tf, []byte(strings.TrimSpace(pt.Token)+"\n"), 0o600); err != nil {
		return 0, "", err
	}
	_ = os.MkdirAll(filepath.Dir(m.LogFile), 0o700)
	lf, err := os.OpenFile(m.LogFile, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return 0, "", err
	}
	defer lf.Close()

	cmd := exec.Command(bin, "tunnel", "--no-autoupdate", "--metrics", m.MetricsAddr, "run", "--token-file", tf)
	cmd.Stdout, cmd.Stderr = lf, lf
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return 0, "", err
	}
	pid := cmd.Process.Pid
	go func() { _ = cmd.Wait() }()

	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return 0, "", ctx.Err()
		case <-time.After(750 * time.Millisecond):
		}
		if !alive(pid) {
			return 0, "", fmt.Errorf("cloudflared static tunnel exited early; see %s", m.LogFile)
		}
		if m.staticReady(2 * time.Second) {
			return pid, "https://" + pt.Hostname, nil
		}
	}
	_ = syscall.Kill(-pid, syscall.SIGTERM)
	return 0, "", fmt.Errorf("timed out waiting for static Cloudflare tunnel %s; see %s", pt.Hostname, m.LogFile)
}

func (m *Manager) launchQuick(ctx context.Context) (int, string, error) {
	bin, err := m.checkBinAndMetrics()
	if err != nil {
		return 0, "", err
	}
	_ = os.MkdirAll(filepath.Dir(m.LogFile), 0o700)
	lf, err := os.OpenFile(m.LogFile, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return 0, "", err
	}
	defer lf.Close()
	cmd := exec.Command(bin, "tunnel", "--no-autoupdate", "--url", m.Target, "--metrics", m.MetricsAddr)
	cmd.Stdout, cmd.Stderr = lf, lf
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return 0, "", err
	}
	pid := cmd.Process.Pid
	go func() { _ = cmd.Wait() }() // reap if it exits while we run
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return 0, "", ctx.Err()
		case <-time.After(time.Second):
		}
		if !alive(pid) {
			return 0, "", fmt.Errorf("cloudflared exited early; see %s", m.LogFile)
		}
		if h, err := m.hostname(2 * time.Second); err == nil && h != "" {
			return pid, "https://" + h, nil
		}
	}
	_ = syscall.Kill(-pid, syscall.SIGTERM)
	return 0, "", fmt.Errorf("timed out waiting for the quick tunnel; see %s", m.LogFile)
}

func (m *Manager) metricsOccupied(timeout time.Duration) bool {
	c := http.Client{Timeout: timeout}
	res, err := c.Get("http://" + m.MetricsAddr + "/ready")
	if err == nil {
		res.Body.Close()
		return true
	}
	if h, err := m.hostname(timeout); err == nil && h != "" {
		return true
	}
	return false
}

func (m *Manager) staticReady(timeout time.Duration) bool {
	c := http.Client{Timeout: timeout}
	if res, err := c.Get("http://" + m.MetricsAddr + "/ready"); err == nil {
		defer res.Body.Close()
		if res.StatusCode == http.StatusOK {
			var r struct {
				ReadyConnections int `json:"readyConnections"`
			}
			if json.NewDecoder(res.Body).Decode(&r) != nil || r.ReadyConnections > 0 {
				return true
			}
		}
	}
	if res, err := c.Get("http://" + m.MetricsAddr + "/metrics"); err == nil {
		defer res.Body.Close()
		b, _ := io.ReadAll(io.LimitReader(res.Body, 256*1024))
		for _, line := range strings.Split(string(b), "\n") {
			if strings.HasPrefix(line, "cloudflared_tunnel_ha_connections ") {
				fields := strings.Fields(line)
				if len(fields) >= 2 {
					if v, err := strconv.ParseFloat(fields[1], 64); err == nil && v > 0 {
						return true
					}
				}
			}
		}
	}
	return false
}

func (m *Manager) hostname(timeout time.Duration) (string, error) {
	c := http.Client{Timeout: timeout}
	res, err := c.Get("http://" + m.MetricsAddr + "/quicktunnel")
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	var r struct {
		Hostname string `json:"hostname"`
	}
	if err := json.NewDecoder(res.Body).Decode(&r); err != nil {
		return "", err
	}
	return r.Hostname, nil
}

func alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	if err := syscall.Kill(pid, 0); err != nil {
		return false
	}
	// A zombie still "exists"; check its state.
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return true
	}
	f := strings.Fields(string(b))
	return len(f) < 3 || f[2] != "Z"
}
