// agentc is the host-side daemon of the Agent Companion app. It exposes agent
// sessions (agy today; claude and codex next), files and terminals to paired
// phones over an end-to-end encrypted channel, reachable through a Cloudflare
// quick tunnel.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/creack/pty"

	"agentc/internal/agycli"
	"agentc/internal/core"
	"agentc/internal/push"
	"agentc/internal/qrterm"
	"agentc/internal/secure"
	"agentc/internal/server"
	"agentc/internal/term"
	"agentc/internal/tunnel"
)

var version = "dev"

const defaultPort = 9473

// Config is ~/.config/agentc/config.json.
type Config struct {
	Port                   int   `json:"port"`
	IdleHibernateMinutes   int   `json:"idleHibernateMinutes"`
	MaxLive                int   `json:"maxLive"`
	DefaultSkipPermissions *bool `json:"defaultSkipPermissions,omitempty"`
	// Listen is the local address the daemon serves on (cloudflared connects
	// to it). Default 127.0.0.1:<port>.
	Listen string `json:"listen,omitempty"`
	// Tunnel: "quick" (default) runs a Cloudflare quick tunnel; "none" relies
	// on PublicURL (your own tunnel, reverse proxy or LAN address).
	Tunnel        string `json:"tunnel,omitempty"`
	PublicURL     string `json:"publicUrl,omitempty"`
	Cloudflared   string `json:"cloudflared,omitempty"`
	TunnelMetrics string `json:"tunnelMetrics,omitempty"`
	AgyPath       string `json:"agyPath,omitempty"`
	// ClaudePath is the Claude Code executable (default ~/.local/bin/claude or PATH).
	ClaudePath string `json:"claudePath,omitempty"`
	// ClaudrArgs are the default claude flags `claudr` adds.
	ClaudrArgs *[]string `json:"claudrArgs,omitempty"`
	// AgyrArgs are the default agy flags `agyr` adds (configurable; --safe drops
	// --dangerously-skip-permissions for one run).
	AgyrArgs *[]string `json:"agyrArgs,omitempty"`
	// PushContent: "full" sends a title + short excerpt through FCM; "minimal"
	// sends no conversation content (the app shows a generic message).
	PushContent string `json:"pushContent,omitempty"`
	// FirebaseServiceAccount: path to the Firebase service account key JSON.
	FirebaseServiceAccount string `json:"firebaseServiceAccount,omitempty"`
}

type paths struct {
	home, config, state, runtime, share string
}

func getPaths() paths {
	home, _ := os.UserHomeDir()
	p := paths{home: home}
	p.config = envOr("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	p.config = filepath.Join(p.config, "agentc")
	p.state = filepath.Join(envOr("XDG_STATE_HOME", filepath.Join(home, ".local", "state")), "agentc")
	rt := os.Getenv("XDG_RUNTIME_DIR")
	if rt == "" {
		rt = filepath.Join(os.TempDir(), fmt.Sprintf("agentc-%d", os.Getuid()))
	}
	p.runtime = filepath.Join(rt, "agentc")
	p.share = filepath.Join(envOr("XDG_DATA_HOME", filepath.Join(home, ".local", "share")), "agentc")
	return p
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func (p paths) configFile() string  { return filepath.Join(p.config, "config.json") }
func (p paths) tmuxSock() string    { return filepath.Join(p.runtime, "tmux.sock") }
func (p paths) tmuxConf() string    { return filepath.Join(p.config, "tmux.conf") }
func (p paths) localSock() string   { return filepath.Join(p.runtime, "agentc.sock") }
func (p paths) apk() string         { return filepath.Join(p.share, "agent-companion.apk") }
func (p paths) identity() string    { return filepath.Join(p.state, "identity.json") }
func (p paths) devices() string     { return filepath.Join(p.state, "devices.json") }
func (p paths) tunnelState() string { return filepath.Join(p.state, "tunnel.json") }
func (p paths) tunnelLog() string   { return filepath.Join(p.state, "cloudflared.log") }

func (c Config) listenAddr() string {
	if c.Listen != "" {
		return c.Listen
	}
	return fmt.Sprintf("127.0.0.1:%d", c.Port)
}

func (c Config) metricsAddr() string {
	if c.TunnelMetrics != "" {
		return c.TunnelMetrics
	}
	return fmt.Sprintf("127.0.0.1:%d", c.Port+1)
}

func loadConfig(p paths) Config {
	c := Config{Port: defaultPort, IdleHibernateMinutes: 30, MaxLive: 8}
	if b, err := os.ReadFile(p.configFile()); err == nil {
		_ = json.Unmarshal(b, &c)
	}
	if c.Port == 0 {
		c.Port = defaultPort
	}
	if c.AgyPath == "" {
		c.AgyPath = findAgy(p.home)
	}
	return c
}

func saveConfig(p paths, c Config) error {
	if err := os.MkdirAll(p.config, 0o700); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(c, "", "  ")
	return os.WriteFile(p.configFile(), append(b, '\n'), 0o600)
}

func findAgy(home string) string {
	if p, err := exec.LookPath("agy"); err == nil {
		return p
	}
	cand := filepath.Join(home, ".local", "bin", "agy")
	if _, err := os.Stat(cand); err == nil {
		return cand
	}
	return "agy"
}

func findClaude(home, configured string) string {
	if configured != "" {
		return configured
	}
	cand := filepath.Join(home, ".local", "bin", "claude")
	if _, err := os.Stat(cand); err == nil {
		return cand
	}
	if p, err := exec.LookPath("claude"); err == nil {
		return p
	}
	return "claude"
}

func selfExe() string {
	exe, err := os.Executable()
	if err != nil {
		return "agentc"
	}
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		return r
	}
	return exe
}

func termManager(p paths, c Config, env func() []string) *term.Manager {
	skip := true
	if c.DefaultSkipPermissions != nil {
		skip = *c.DefaultSkipPermissions
	}
	return &term.Manager{Sock: p.tmuxSock(), Conf: p.tmuxConf(), SelfExe: selfExe(), LocalSock: p.localSock(), AgyPath: c.AgyPath,
		ClaudePath: findClaude(p.home, c.ClaudePath), DefaultSkip: skip, Env: env}
}

func main() {
	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	name := filepath.Base(os.Args[0])
	args := os.Args[1:]
	if name == "agyr" {
		os.Exit(runAgyr(args))
	}
	if name == "claudr" {
		os.Exit(runClaudr(args))
	}
	if len(args) == 0 {
		usage()
		os.Exit(2)
	}
	switch args[0] {
	case "serve":
		os.Exit(serve(args[1:]))
	case "run", "agyr":
		if len(args) > 1 && args[1] == "claude" {
			os.Exit(runClaudr(args[2:]))
		}
		os.Exit(runAgyr(args[1:]))
	case "claudr":
		os.Exit(runClaudr(args[1:]))
	case "claude-hook":
		os.Exit(claudeHook(args[1:]))
	case "term", "terms":
		os.Exit(termCmd(args[1:]))
	case "status":
		os.Exit(status(true))
	case "pair", "qr":
		os.Exit(pairCmd(args[1:]))
	case "devices", "device":
		os.Exit(devicesCmd(args[1:]))
	case "tunnel":
		os.Exit(tunnelCmd(args[1:]))
	case "install":
		os.Exit(install(args[1:]))
	case "osc-watch":
		os.Exit(oscWatch(args[1:]))
	case "version", "--version", "-v":
		fmt.Println("agentc", version)
	case "help", "-h", "--help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", args[0])
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `agentc — Agent Companion daemon (remote control for coding agents)

usage:
  agentc install [--port N]          install the binary, the agyr link and the systemd user service
  agentc pair                        show a QR code that pairs a phone (one-time, expires in 10 min)
  agentc status                      show the daemon, tunnel and paired devices
  agentc devices [revoke ID]         list paired devices, or unpair one
  agentc tunnel stop                 stop the Cloudflare tunnel process (a new URL is made on restart)
  agentc serve [--listen ADDR]       run the daemon (normally via systemd)
  agentc term ls | attach ID | kill ID   manage shared terminals
  claudr [claude args...]            run Claude Code in a shared terminal the phone can see
                                     (adds "claudrArgs"; --safe drops --dangerously-skip-permissions)
  claudr ls | attach [ID]            list / attach shared Claude Code terminals
  agyr [agy args...]                 run agy in a shared terminal the phone can see
                                     (adds "agyrArgs" from ~/.config/agentc/config.json,
                                     default --dangerously-skip-permissions; --safe omits it)
  agyr ls | attach [ID]              list / attach shared agy terminals
  agentc version
`)
}

// ---- serve ----

type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

func serve(args []string) int {
	p := getPaths()
	cfg := loadConfig(p)
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	listen := fs.String("listen", cfg.listenAddr(), "local listen address (cloudflared connects here)")
	apk := fs.String("apk", p.apk(), "companion APK offered on pairing pages")
	_ = fs.Parse(args)

	for _, d := range []string{p.state, p.runtime, p.config, p.share} {
		_ = os.MkdirAll(d, 0o700)
	}
	lf, err := os.OpenFile(filepath.Join(p.state, "agentc.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err == nil {
		log.SetOutput(io.MultiWriter(os.Stderr, lf))
	}
	log.Printf("agentc %s starting (agy: %s)", version, cfg.AgyPath)

	identity, err := secure.LoadOrCreateIdentity(p.identity())
	if err != nil {
		log.Printf("identity: %v", err)
		return 1
	}
	devices, err := secure.OpenRegistry(p.devices())
	if err != nil {
		log.Printf("devices: %v", err)
		return 1
	}
	log.Printf("host id %s, fingerprint %s, %d paired device(s)", identity.ID(), secure.Fingerprint(identity.Key.Public), len(devices.List()))

	skip := true
	if cfg.DefaultSkipPermissions != nil {
		skip = *cfg.DefaultSkipPermissions
	}
	hub := core.NewHub()
	mgr := core.NewManager(core.Config{
		AgyPath: cfg.AgyPath, ClaudePath: findClaude(p.home, cfg.ClaudePath), StateDir: p.state, HomeDir: p.home,
		AgyDataDir: filepath.Join(p.home, ".gemini", "antigravity-cli"), IdleHibernate: time.Duration(cfg.IdleHibernateMinutes) * time.Minute,
		MaxLive: cfg.MaxLive, DefaultSkip: skip,
	}, hub)
	terms := termManager(p, cfg, mgr.AgyEnv)
	if err := terms.Init(); err != nil {
		log.Printf("terminal init: %v", err)
	}
	cli := &agycli.CLI{Agy: cfg.AgyPath, DataDir: filepath.Join(p.home, ".gemini", "antigravity-cli"), Gemini: filepath.Join(p.home, ".gemini"), Env: mgr.AgyEnv}
	saPath := cfg.FirebaseServiceAccount
	if saPath == "" {
		saPath = filepath.Join(p.config, "firebase-service-account.json")
	}
	pusher := push.New(saPath, filepath.Join(p.state, "push-devices.json"))
	if cfg.PushContent == "minimal" {
		pusher.Content = "minimal"
	}
	mgr.Sink = func(n core.Notification) {
		pusher.Send(push.Message{Kind: n.Kind, Title: n.Title, Body: n.Body, SessionID: n.SessionID, TermID: n.TermID,
			ReqID: n.ReqID, Options: n.Options, Seq: n.Seq, ID: n.ID, Ts: n.Ts})
	}
	if pusher.Configured() {
		log.Printf("push: Firebase Cloud Messaging enabled")
	} else {
		log.Printf("push: disabled until a Firebase service account key is saved to %s", saPath)
	}
	server.Version = version
	srv := &server.Server{Mgr: mgr, Terms: terms, CLI: cli, Push: pusher, Identity: identity, Devices: devices,
		Tokens: secure.NewTokens(), APKPath: *apk, Started: time.Now()}

	// Public URL: a quick tunnel unless the user configured their own.
	var tun *tunnel.Manager
	mode := cfg.Tunnel
	if mode == "" {
		mode = "quick"
		if cfg.PublicURL != "" {
			mode = "none"
		}
	}
	if mode == "quick" {
		tun = &tunnel.Manager{Bin: firstNonEmpty(cfg.Cloudflared, "cloudflared"), Target: "http://" + *listen, MetricsAddr: cfg.metricsAddr(),
			StateFile: p.tunnelState(), LogFile: p.tunnelLog()}
		tun.OnURL = func(url string) {
			log.Printf("public URL: %s", url)
			mgr.Hub().Broadcast("host.url", map[string]any{"url": url})
			// Phones follow the new address through push (quick tunnel URLs change
			// whenever cloudflared restarts).
			pusher.Send(push.Message{Kind: "host_url", URL: url, ID: fmt.Sprintf("url-%d", time.Now().UnixNano()), Ts: time.Now().UnixMilli()})
		}
		srv.PublicURL = tun.URL
		srv.Tunnel = tun.Status
	} else {
		pub := strings.TrimRight(cfg.PublicURL, "/")
		srv.PublicURL = func() string { return pub }
		srv.Tunnel = func() map[string]any { return map[string]any{"mode": "none", "url": pub} }
	}
	var cfgMu sync.Mutex
	srv.ConfigGet = func() map[string]any {
		cfgMu.Lock()
		defer cfgMu.Unlock()
		return map[string]any{"listen": *listen, "tunnel": mode, "publicUrl": srv.PublicURL(), "idleHibernateMinutes": cfg.IdleHibernateMinutes,
			"maxLive": cfg.MaxLive, "defaultSkipPermissions": skip, "agyPath": cfg.AgyPath, "pushContent": pusher.Content, "firebaseServiceAccount": saPath}
	}
	srv.ConfigSet = func(in map[string]any) (map[string]any, error) {
		cfgMu.Lock()
		if v, ok := in["idleHibernateMinutes"].(float64); ok && v >= 1 {
			cfg.IdleHibernateMinutes = int(v)
		}
		if v, ok := in["maxLive"].(float64); ok && v >= 1 {
			cfg.MaxLive = int(v)
		}
		if v, ok := in["defaultSkipPermissions"].(bool); ok {
			cfg.DefaultSkipPermissions = &v
		}
		if v, ok := in["pushContent"].(string); ok && (v == "full" || v == "minimal") {
			cfg.PushContent = v
			pusher.Content = v
		}
		err := saveConfig(p, cfg)
		cfgMu.Unlock()
		return srv.ConfigGet(), err
	}
	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		log.Printf("listen %s: %v", *listen, err)
		return 1
	}
	srv.Listeners = func() []string { return []string{ln.Addr().String()} }
	httpSrv := &http.Server{Handler: srv.Handler(), ReadHeaderTimeout: 15 * time.Second}
	go func() {
		if err := httpSrv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("serve: %v", err)
		}
	}()
	log.Printf("listening on %s", ln.Addr())
	localLn, err := srv.ListenLocal(p.localSock())
	if err != nil {
		log.Printf("local socket: %v", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if tun != nil {
		go tun.Run(ctx)
	}
	go pollTerms(ctx, srv, terms)
	<-ctx.Done()
	log.Printf("shutting down")
	sctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	_ = httpSrv.Shutdown(sctx)
	cancel()
	if localLn != nil {
		_ = localLn.Close()
	}
	devices.Flush()
	mgr.Shutdown()
	log.Printf("bye")
	return 0
}

// pollTerms broadcasts terminal list changes (attach counts, exits, states).
func pollTerms(ctx context.Context, srv *server.Server, tm *term.Manager) {
	last := ""
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		terms, err := tm.List(ctx)
		if err != nil {
			continue
		}
		b, _ := json.Marshal(terms)
		if string(b) != last {
			last = string(b)
			srv.Mgr.Hub().Broadcast("terms.changed", terms)
		}
	}
}

// ---- agyr ----

func runAgyr(args []string) int {
	p := getPaths()
	cfg := loadConfig(p)
	tm := termManager(p, cfg, nil)
	if len(args) > 0 {
		switch args[0] {
		case "ls", "list":
			return termCmd([]string{"ls"})
		case "attach", "a":
			id := ""
			if len(args) > 1 {
				id = args[1]
			} else {
				terms, _ := tm.List(context.Background())
				for _, t := range terms {
					if t.Kind == "agy" {
						id = t.ID
						break
					}
				}
				if id == "" {
					fmt.Fprintln(os.Stderr, "no shared agy terminals")
					return 1
				}
			}
			return attach(tm, id)
		case "help", "-h", "--help":
			usage()
			return 0
		}
	}
	defaults := []string{"--dangerously-skip-permissions"}
	if cfg.AgyrArgs != nil {
		defaults = *cfg.AgyrArgs
	}
	pass, skip := agyrArgs(defaults, args)
	cwd, _ := os.Getwd()
	cols, rows := 120, 40
	if ws, err := pty.GetsizeFull(os.Stdin); err == nil && ws.Cols > 0 {
		cols, rows = int(ws.Cols), int(ws.Rows)
	}
	t, err := tm.Create(context.Background(), term.CreateOpts{Kind: "agy", Cwd: cwd, Args: pass, Cols: cols, Rows: rows, Origin: "agyr", Skip: &skip})
	if err != nil {
		fmt.Fprintln(os.Stderr, "agyr:", err)
		return 1
	}
	fmt.Fprintf(os.Stderr, "agyr: shared terminal %s (detach: Ctrl-b d · reattach: agyr attach %s)\n", t.ID, t.ID)
	return attach(tm, t.ID)
}

// agyrArgs combines the configured default agy flags with the ones typed
// after agyr. Defaults come first (in order) so explicit arguments override
// them; --safe drops --dangerously-skip-permissions for this run.
func agyrArgs(defaults, args []string) (pass []string, skip bool) {
	safe := false
	var user []string
	for _, a := range args {
		if a == "--safe" {
			safe = true
			continue
		}
		user = append(user, a)
	}
	for _, d := range defaults {
		if d == "--dangerously-skip-permissions" {
			skip = !safe
			continue
		}
		pass = append(pass, d)
	}
	return append(pass, user...), skip
}

func attach(tm *term.Manager, id string) int {
	tmux, err := exec.LookPath("tmux")
	if err != nil {
		fmt.Fprintln(os.Stderr, "tmux not found")
		return 1
	}
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "TMUX=") && !strings.HasPrefix(kv, "TMUX_PANE=") {
			env = append(env, kv)
		}
	}
	err = syscall.Exec(tmux, []string{"tmux", "-S", tm.Sock, "-f", tm.Conf, "attach-session", "-t", id}, env)
	fmt.Fprintln(os.Stderr, "exec tmux:", err)
	return 1
}

func termCmd(args []string) int {
	p := getPaths()
	cfg := loadConfig(p)
	tm := termManager(p, cfg, nil)
	if len(args) == 0 {
		args = []string{"ls"}
	}
	ctx := context.Background()
	switch args[0] {
	case "ls", "list":
		terms, err := tm.List(ctx)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		if len(terms) == 0 {
			fmt.Println("no shared terminals")
			return 0
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
		fmt.Fprintln(w, "ID\tKIND\tNAME\tSTATE\tATTACHED\tCWD")
		for _, t := range terms {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%d\t%s\n", t.ID, t.Kind, t.Name, t.State, t.Attached, t.Cwd)
		}
		w.Flush()
	case "attach", "a":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: agentc term attach ID")
			return 2
		}
		return attach(tm, args[1])
	case "kill":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: agentc term kill ID")
			return 2
		}
		if err := tm.Kill(ctx, args[1]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
	default:
		fmt.Fprintf(os.Stderr, "unknown term command %q\n", args[0])
		return 2
	}
	return 0
}

// ---- status ----

func localClient(p paths) *http.Client {
	return &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", p.localSock())
	}}}
}

func localGet(path string, out any) error {
	res, err := localClient(getPaths()).Get("http://agentc" + path)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	return json.NewDecoder(res.Body).Decode(out)
}

func localPost(path string, in, out any) (int, error) {
	b, _ := json.Marshal(in)
	res, err := localClient(getPaths()).Post("http://agentc"+path, "application/json", strings.NewReader(string(b)))
	if err != nil {
		return 0, err
	}
	defer res.Body.Close()
	return res.StatusCode, json.NewDecoder(res.Body).Decode(out)
}

func notRunning(err error) int {
	fmt.Println("agentc daemon: not running (" + err.Error() + ")")
	fmt.Println("start it with: systemctl --user start agentc   (or: agentc install)")
	return 1
}

// status prints the daemon state; pairHint adds how to pair when no phone is.
func status(pairHint bool) int {
	var info map[string]any
	if err := localGet("/local/status", &info); err != nil {
		return notRunning(err)
	}
	fmt.Printf("agentc daemon %v on %v — up %vs\n", info["version"], info["host"], info["uptimeSec"])
	fmt.Printf("  host id:     %v   fingerprint %v\n", info["hostId"], info["fingerprint"])
	if t, ok := info["tunnel"].(map[string]any); ok {
		st := fmt.Sprint(t["status"])
		switch st {
		case "<nil>":
			st = "configured"
		case "":
			st = "starting"
		}
		fmt.Printf("  public URL:  %v  (%v, %v)\n", firstNonEmpty(fmt.Sprint(info["publicUrl"]), "(starting…)"), t["mode"], st)
	}
	if l, ok := info["listen"].([]any); ok {
		for _, a := range l {
			fmt.Printf("  listening:   http://%v (local only)\n", a)
		}
	}
	if st, ok := info["stats"].(map[string]any); ok {
		fmt.Printf("  sessions:    %v known, %v live (%.0f MB RSS)\n", st["sessions"], st["live"], toF(st["rssKiB"])/1024)
	}
	if pu, ok := info["push"].(map[string]any); ok {
		fmt.Printf("  push:        configured=%v content=%v\n", pu["configured"], pu["content"])
	}
	devs, _ := info["deviceList"].([]any)
	fmt.Printf("  devices:     %d paired", len(devs))
	if len(devs) == 0 && pairHint {
		fmt.Print(" — run `agentc pair` and scan the QR code with the app")
	}
	fmt.Println()
	for _, d := range devs {
		m, _ := d.(map[string]any)
		fmt.Printf("    %-18v %-24v last seen %s\n", m["id"], m["name"], ago(toF(m["lastSeen"])))
	}
	return 0
}

func ago(ms float64) string {
	if ms == 0 {
		return "never"
	}
	d := time.Since(time.UnixMilli(int64(ms))).Round(time.Second)
	if d < time.Minute {
		return "just now"
	}
	return d.String() + " ago"
}

func pairCmd(args []string) int {
	fs := flag.NewFlagSet("pair", flag.ExitOnError)
	url := fs.String("url", "", "advertise this base URL instead of the tunnel (e.g. http://192.168.1.5:9473)")
	ttl := fs.Duration("ttl", 10*time.Minute, "how long the code stays valid")
	plain := fs.Bool("no-qr", false, "print only the link")
	_ = fs.Parse(args)
	var out map[string]any
	var code int
	var err error
	for i := 0; i < 60; i++ { // the tunnel may still be starting
		code, err = localPost("/local/pair", map[string]any{"ttlSeconds": int(ttl.Seconds()), "url": *url}, &out)
		if err != nil {
			return notRunning(err)
		}
		if code != 503 {
			break
		}
		if i == 0 {
			fmt.Fprintln(os.Stderr, "waiting for the tunnel…")
		}
		time.Sleep(2 * time.Second)
	}
	if code != 200 {
		fmt.Fprintln(os.Stderr, "pair:", out["error"])
		return 1
	}
	link := fmt.Sprint(out["url"])
	if !*plain {
		if q, err := qrterm.Render(link); err == nil {
			fmt.Println()
			fmt.Print(q)
		}
	}
	exp := time.UnixMilli(int64(toF(out["expires"])))
	fmt.Printf("\nScan with Agent Companion (Hosts → Add host), or open this link on the phone:\n%s\n", link)
	fmt.Printf("\nHost %v · fingerprint %v · single use · expires %s\n", out["host"], out["fingerprint"], exp.Format("15:04:05"))
	fmt.Println("Anyone with this code can pair, so don't share it.")
	return 0
}

func devicesCmd(args []string) int {
	if len(args) >= 2 && (args[0] == "revoke" || args[0] == "rm" || args[0] == "remove") {
		var out map[string]any
		if _, err := localPost("/local/devices/revoke", map[string]any{"id": args[1]}, &out); err != nil {
			return notRunning(err)
		}
		if out["removed"] == true {
			fmt.Println("unpaired", args[1])
			return 0
		}
		fmt.Fprintln(os.Stderr, "no such device:", args[1], out["error"])
		return 1
	}
	var devs []map[string]any
	if err := localGet("/local/devices", &devs); err != nil {
		return notRunning(err)
	}
	if len(devs) == 0 {
		fmt.Println("no paired devices — run `agentc pair`")
		return 0
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tNAME\tAPP\tPAIRED\tLAST SEEN")
	for _, d := range devs {
		fmt.Fprintf(tw, "%v\t%v\t%v\t%s\t%s\n", d["id"], d["name"], d["appVersion"], time.UnixMilli(int64(toF(d["pairedAt"]))).Format("2006-01-02 15:04"), ago(toF(d["lastSeen"])))
	}
	tw.Flush()
	return 0
}

func tunnelCmd(args []string) int {
	p := getPaths()
	cfg := loadConfig(p)
	if len(args) == 0 || args[0] == "status" {
		return status(true)
	}
	if args[0] == "stop" {
		m := &tunnel.Manager{Target: "http://" + cfg.listenAddr(), MetricsAddr: cfg.metricsAddr(), StateFile: p.tunnelState()}
		if err := m.Stop(); err != nil {
			fmt.Fprintln(os.Stderr, "tunnel:", err)
			return 1
		}
		fmt.Println("tunnel stopped; the daemon starts a new one (with a new URL) within ~15s")
		return 0
	}
	fmt.Fprintln(os.Stderr, "usage: agentc tunnel [status|stop]")
	return 2
}

func toF(v any) float64 {
	f, _ := v.(float64)
	return f
}

// ---- claudr: Claude Code in a terminal the phone can share ----

func runClaudr(args []string) int {
	p := getPaths()
	cfg := loadConfig(p)
	tm := termManager(p, cfg, nil)
	if len(args) > 0 {
		switch args[0] {
		case "ls", "list":
			return termCmd([]string{"ls"})
		case "attach", "a":
			id := ""
			if len(args) > 1 {
				id = args[1]
			} else {
				terms, _ := tm.List(context.Background())
				for _, t := range terms {
					if t.Kind == "claude" {
						id = t.ID
						break
					}
				}
				if id == "" {
					fmt.Fprintln(os.Stderr, "no shared Claude Code terminals")
					return 1
				}
			}
			return attach(tm, id)
		case "help", "-h", "--help":
			usage()
			return 0
		}
	}
	defaults := []string{"--dangerously-skip-permissions"}
	if cfg.ClaudrArgs != nil {
		defaults = *cfg.ClaudrArgs
	}
	pass := append(append([]string{}, defaults...), args...)
	safe := false
	var keep []string
	for _, a := range pass {
		if a == "--safe" {
			safe = true
			continue
		}
		keep = append(keep, a)
	}
	if safe {
		var k []string
		for _, a := range keep {
			if a != "--dangerously-skip-permissions" {
				k = append(k, a)
			}
		}
		keep = k
	}
	noSkip := false
	cwd, _ := os.Getwd()
	cols, rows := 120, 40
	if ws, err := pty.GetsizeFull(os.Stdin); err == nil && ws.Cols > 0 {
		cols, rows = int(ws.Cols), int(ws.Rows)
	}
	t, err := tm.Create(context.Background(), term.CreateOpts{Kind: "claude", Cwd: cwd, Args: keep, Cols: cols, Rows: rows, Origin: "claudr", Skip: &noSkip})
	if err != nil {
		fmt.Fprintln(os.Stderr, "claudr:", err)
		return 1
	}
	fmt.Fprintf(os.Stderr, "claudr: shared terminal %s (detach: Ctrl-b d · reattach: claudr attach %s)\n", t.ID, t.ID)
	return attach(tm, t.ID)
}

// ---- claude-hook (internal, run by Claude Code hooks in agentc terminals) ----

func claudeHook(args []string) int {
	fs := flag.NewFlagSet("claude-hook", flag.ContinueOnError)
	sock := fs.String("sock", "", "tmux socket")
	conf := fs.String("conf", "", "tmux config")
	id := fs.String("id", "", "terminal id")
	report := fs.String("report", "", "daemon local socket")
	if fs.Parse(args) != nil || *id == "" {
		return 0 // never block Claude Code
	}
	var in struct {
		Event        string `json:"hook_event_name"`
		SessionID    string `json:"session_id"`
		Message      string `json:"message"`
		Type         string `json:"notification_type"`
		LastMessage  string `json:"last_assistant_message"`
		StopActive   bool   `json:"stop_hook_active"`
		TranscriptFn string `json:"transcript_path"`
	}
	b, _ := io.ReadAll(io.LimitReader(os.Stdin, 4<<20))
	if json.Unmarshal(b, &in) != nil {
		return 0
	}
	state, msg := "", ""
	switch in.Event {
	case "SessionStart":
		if in.SessionID != "" {
			term.SetConversation(*sock, *conf, *id, in.SessionID)
		}
		state = "idle"
	case "UserPromptSubmit", "PostToolUse":
		state = "working"
	case "Notification":
		switch {
		case in.Type == "permission_prompt" || strings.Contains(strings.ToLower(in.Message), "permission"):
			state, msg = "blocked:permission", in.Message
		case in.Type == "idle_prompt":
			state, msg = "idle", in.Message
		case in.Type == "elicitation_dialog":
			state, msg = "blocked:question", in.Message
		default:
			return 0
		}
	case "Stop":
		state, msg = "done", in.LastMessage
	case "SessionEnd":
		state = "idle"
	default:
		return 0
	}
	if len(msg) > 280 {
		msg = msg[:280] + "…"
	}
	term.Publish(*sock, *conf, *id, *report, state, msg)
	return 0
}

// ---- osc-watch (internal, run by tmux pipe-pane) ----

func oscWatch(args []string) int {
	fs := flag.NewFlagSet("osc-watch", flag.ExitOnError)
	sock := fs.String("sock", "", "tmux socket")
	conf := fs.String("conf", "", "tmux config")
	id := fs.String("id", "", "terminal id")
	report := fs.String("report", "", "daemon local socket")
	_ = fs.Parse(args)
	if err := term.WatchOSC(os.Stdin, *sock, *conf, *id, *report); err != nil {
		return 1
	}
	return 0
}

// ---- install ----

const unitTmpl = `[Unit]
Description=Agent Companion daemon (agentc)
After=network-online.target
Wants=network-online.target

[Service]
Type=exec
ExecStart=%s serve
Environment=PATH=%%h/.local/bin:` + unitSystemPath + `
WorkingDirectory=%%h
Restart=always
RestartSec=3
# Only the daemon is signalled on stop/restart: it closes agent sessions
# gracefully itself, while shared terminals (tmux) and the Cloudflare tunnel
# keep running, so the tunnel URL survives daemon restarts and upgrades.
KillMode=process
TimeoutStopSec=60

[Install]
WantedBy=default.target
`

func systemctl(args ...string) error {
	out, err := exec.Command("systemctl", append([]string{"--user"}, args...)...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("systemctl --user %s: %v %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

// migrateFromAgyc moves an older "agyc" install (Tailscale-era name) over.
func migrateFromAgyc(p paths) {
	unitDir := filepath.Join(envOr("XDG_CONFIG_HOME", filepath.Join(p.home, ".config")), "systemd", "user")
	old := filepath.Join(unitDir, "agyc.service")
	if _, err := os.Stat(old); err == nil {
		_ = systemctl("disable", "--now", "agyc.service")
		_ = os.Remove(old)
		fmt.Println("removed the old agyc service")
	}
	if b := filepath.Join(p.home, ".local", "bin", "agyc"); fileExists(b) {
		_ = os.Remove(b)
	}
	oldCfg := filepath.Join(filepath.Dir(p.config), "agyc")
	for _, name := range []string{"firebase-service-account.json", "config.json"} {
		src, dst := filepath.Join(oldCfg, name), filepath.Join(p.config, name)
		if fileExists(src) && !fileExists(dst) {
			_ = os.MkdirAll(p.config, 0o700)
			if err := os.Rename(src, dst); err == nil {
				fmt.Println("moved", src, "->", dst)
			}
		}
	}
	oldState := filepath.Join(filepath.Dir(p.state), "agyc")
	if fileExists(filepath.Join(oldState, "sessions")) && !fileExists(filepath.Join(p.state, "sessions")) {
		_ = os.MkdirAll(p.state, 0o700)
		if err := os.Rename(filepath.Join(oldState, "sessions"), filepath.Join(p.state, "sessions")); err == nil {
			fmt.Println("moved session metadata to", p.state)
		}
	}
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func install(args []string) int {
	p := getPaths()
	fs := flag.NewFlagSet("install", flag.ExitOnError)
	port := fs.Int("port", 0, "local port (default 9473)")
	noService := fs.Bool("no-service", false, "don't install/start the systemd user service")
	_ = fs.Parse(args)
	migrateFromAgyc(p)
	bin := filepath.Join(p.home, ".local", "bin")
	_ = os.MkdirAll(bin, 0o755)
	dst := filepath.Join(bin, "agentc")
	if src := selfExe(); src != dst {
		if err := copyExe(src, dst); err != nil {
			fmt.Fprintln(os.Stderr, "install binary:", err)
			return 1
		}
		fmt.Println("installed", dst)
	}
	for _, l := range []string{"claudr"} {
		lp := filepath.Join(bin, l)
		_ = os.Remove(lp)
		if err := os.Symlink("agentc", lp); err == nil {
			fmt.Println("linked", lp, "-> agentc")
		}
	}
	link := filepath.Join(bin, "agyr")
	_ = os.Remove(link)
	if err := os.Symlink("agentc", link); err != nil {
		fmt.Fprintln(os.Stderr, "link agyr:", err)
	} else {
		fmt.Println("linked", link, "-> agentc")
	}
	cfg := loadConfig(p)
	if *port > 0 {
		cfg.Port = *port
	}
	if !fileExists(p.configFile()) || *port > 0 {
		if err := saveConfig(p, cfg); err != nil {
			fmt.Fprintln(os.Stderr, "config:", err)
		}
	}
	// The service runs with its own PATH (~/.local/bin first), which a
	// non-login shell such as `ssh host cmd` may not have.
	cf := firstNonEmpty(cfg.Cloudflared, "cloudflared")
	_, errHere := exec.LookPath(cf)
	_, errService := lookPathIn(cf, servicePath(p.home))
	if errHere != nil && errService != nil && cfg.PublicURL == "" {
		fmt.Println("warning: cloudflared is not installed; the daemon needs it for its public URL")
		fmt.Println("         see https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/downloads/")
	}
	if *noService {
		return 0
	}
	unitDir := filepath.Join(envOr("XDG_CONFIG_HOME", filepath.Join(p.home, ".config")), "systemd", "user")
	_ = os.MkdirAll(unitDir, 0o755)
	unit := filepath.Join(unitDir, "agentc.service")
	if err := os.WriteFile(unit, []byte(fmt.Sprintf(unitTmpl, dst)), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "write unit:", err)
		return 1
	}
	fmt.Println("wrote", unit)
	for _, c := range [][]string{{"daemon-reload"}, {"enable", "agentc.service"}, {"restart", "agentc.service"}} {
		if err := systemctl(c...); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
	}
	fmt.Println("service enabled and started (systemctl --user status agentc)")
	if out, err := exec.Command("loginctl", "show-user", os.Getenv("USER"), "-p", "Linger").Output(); err == nil && strings.TrimSpace(string(out)) == "Linger=no" {
		fmt.Println("tip: run `loginctl enable-linger` so agentc keeps running when you log out")
	}
	// A fresh quick tunnel takes a few seconds; show its URL rather than
	// "starting" (an upgrade adopts the running tunnel and returns at once).
	var info map[string]any
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(500 * time.Millisecond) {
		info = nil
		if localGet("/local/status", &info) == nil && tunnelSettled(info) {
			break
		}
	}
	if status(false) != 0 { // install says how to pair itself, below
		return 1
	}
	cmd := "agentc"
	if !onPath(bin) {
		cmd = dst
		fmt.Printf("\nnote: %s isn't on this shell's PATH (a new login shell usually has it), so use %s\n", bin, dst)
	}
	// Upgrades keep their paired phones; only a fresh install needs pairing.
	if devs, _ := info["deviceList"].([]any); len(devs) > 0 {
		return 0
	}
	fmt.Printf("\nNext: run `%s pair` and scan the QR code with the Agent Companion app.\n", cmd)
	return 0
}

// tunnelSettled reports whether `agentc status` would show the tunnel's final
// state: a URL, an error, or no tunnel at all.
func tunnelSettled(info map[string]any) bool {
	if info == nil {
		return false
	}
	if u, _ := info["publicUrl"].(string); u != "" {
		return true
	}
	t, _ := info["tunnel"].(map[string]any)
	mode, _ := t["mode"].(string)
	st, _ := t["status"].(string)
	return mode != "cloudflare-quick" || strings.HasPrefix(st, "error")
}

// unitSystemPath follows ~/.local/bin in the PATH the systemd unit gives the
// daemon, and so the agents and cloudflared it starts.
const unitSystemPath = "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin:/snap/bin"

func servicePath(home string) string {
	return filepath.Join(home, ".local", "bin") + string(filepath.ListSeparator) + unitSystemPath
}

// lookPathIn is exec.LookPath against the given PATH instead of $PATH.
func lookPathIn(name, path string) (string, error) {
	if strings.Contains(name, "/") {
		return exec.LookPath(name)
	}
	for _, dir := range filepath.SplitList(path) {
		if dir == "" {
			continue
		}
		f := filepath.Join(dir, name)
		if fi, err := os.Stat(f); err == nil && fi.Mode().IsRegular() && fi.Mode().Perm()&0o111 != 0 {
			return f, nil
		}
	}
	return "", exec.ErrNotFound
}

// onPath reports whether dir is one of the directories in $PATH.
func onPath(dir string) bool {
	for _, d := range filepath.SplitList(os.Getenv("PATH")) {
		if d != "" && filepath.Clean(d) == filepath.Clean(dir) {
			return true
		}
	}
	return false
}

func copyExe(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".new"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		return errors.Join(err, os.Remove(tmp))
	}
	return nil
}

func firstNonEmpty(v ...string) string {
	for _, x := range v {
		if x != "" {
			return x
		}
	}
	return ""
}
