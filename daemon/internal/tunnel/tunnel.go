// Package tunnel runs a Cloudflare quick tunnel (cloudflared) in front of the
// daemon's loopback listener. The tunnel process is started in its own process
// group so it survives daemon restarts (the public URL then stays the same);
// on start the daemon re-adopts it when it is still healthy.
package tunnel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

// Manager supervises one quick tunnel.
type Manager struct {
	Bin         string // cloudflared binary
	Target      string // e.g. http://127.0.0.1:9473
	MetricsAddr string // e.g. 127.0.0.1:9474 (cloudflared's /quicktunnel endpoint)
	StateFile   string
	LogFile     string
	// OnURL is called whenever the public URL changes (including the first time).
	OnURL func(url string)

	mu     sync.Mutex
	url    string
	pid    int
	status string
}

type state struct {
	PID     int    `json:"pid"`
	Target  string `json:"target"`
	Metrics string `json:"metrics"`
	URL     string `json:"url"`
}

// URL returns the current public URL ("" while starting).
func (m *Manager) URL() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.url
}

// Status describes the tunnel for `agentc status`.
func (m *Manager) Status() map[string]any {
	m.mu.Lock()
	defer m.mu.Unlock()
	return map[string]any{"url": m.url, "pid": m.pid, "status": m.status, "mode": "cloudflare-quick"}
}

func (m *Manager) set(url string, pid int, status string) {
	m.mu.Lock()
	changed := url != "" && url != m.url
	m.url, m.pid, m.status = url, pid, status
	m.mu.Unlock()
	if changed {
		b, _ := json.Marshal(state{PID: pid, Target: m.Target, Metrics: m.MetricsAddr, URL: url})
		_ = os.WriteFile(m.StateFile, b, 0o600)
		if m.OnURL != nil {
			m.OnURL(url)
		}
	}
}

// Run keeps a tunnel up until ctx is done (the tunnel process itself is left
// running so the next daemon start can adopt it).
func (m *Manager) Run(ctx context.Context) {
	if m.Bin == "" {
		m.Bin = "cloudflared"
	}
	backoff := 5 * time.Second
	for ctx.Err() == nil {
		pid, url := m.adopt()
		if pid == 0 {
			var err error
			pid, url, err = m.launch(ctx)
			if err != nil {
				m.set("", 0, "error: "+err.Error())
				log.Printf("tunnel: %v (retrying in %s)", err, backoff)
				select {
				case <-ctx.Done():
					return
				case <-time.After(backoff):
				}
				backoff = min(backoff*2, 5*time.Minute)
				continue
			}
			log.Printf("tunnel: started cloudflared (pid %d): %s", pid, url)
		} else {
			log.Printf("tunnel: adopted running cloudflared (pid %d): %s", pid, url)
		}
		backoff = 5 * time.Second
		m.set(url, pid, "up")
		// Supervise: the URL is only valid while that process lives.
		for ctx.Err() == nil {
			select {
			case <-ctx.Done():
				return
			case <-time.After(15 * time.Second):
			}
			if !alive(pid) {
				log.Printf("tunnel: cloudflared (pid %d) exited; starting a new tunnel", pid)
				m.set("", 0, "restarting")
				break
			}
			if h, err := m.hostname(2 * time.Second); err == nil && h != "" && "https://"+h != url {
				url = "https://" + h
				m.set(url, pid, "up")
			}
		}
	}
}

// Stop kills the tunnel process this daemon started (for `agentc tunnel stop`).
func (m *Manager) Stop() error {
	var st state
	b, err := os.ReadFile(m.StateFile)
	if err != nil {
		return err
	}
	if json.Unmarshal(b, &st) != nil || st.PID == 0 || !m.ours(st.PID) {
		return errors.New("no tunnel started by agentc is running")
	}
	_ = syscall.Kill(-st.PID, syscall.SIGTERM)
	_ = os.Remove(m.StateFile)
	return nil
}

func (m *Manager) adopt() (int, string) {
	b, err := os.ReadFile(m.StateFile)
	if err != nil {
		return 0, ""
	}
	var st state
	if json.Unmarshal(b, &st) != nil || st.PID == 0 {
		return 0, ""
	}
	if st.Target != m.Target || st.Metrics != m.MetricsAddr || !alive(st.PID) || !m.ours(st.PID) {
		if st.PID != 0 && alive(st.PID) && m.ours(st.PID) {
			_ = syscall.Kill(-st.PID, syscall.SIGTERM) // config changed: replace it
		}
		return 0, ""
	}
	h, err := m.hostname(3 * time.Second)
	if err != nil || h == "" || "https://"+h != st.URL {
		return 0, ""
	}
	return st.PID, st.URL
}

// ours checks that pid is a cloudflared quick tunnel pointed at our target,
// so we never touch other cloudflared processes on the machine.
func (m *Manager) ours(pid int) bool {
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
	return has["--url"] && has[m.Target] && has[m.MetricsAddr]
}

func (m *Manager) launch(ctx context.Context) (int, string, error) {
	bin, err := exec.LookPath(m.Bin)
	if err != nil {
		return 0, "", fmt.Errorf("cloudflared not found (%s): install it or set \"cloudflared\" in the config", m.Bin)
	}
	if h, err := m.hostname(time.Second); err == nil && h != "" {
		return 0, "", fmt.Errorf("something else is already serving cloudflared metrics on %s; set a different \"tunnelMetrics\" address", m.MetricsAddr)
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
