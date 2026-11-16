// Package server exposes the daemon to paired apps. The only public endpoint
// is an end-to-end encrypted WebSocket (see package secure); it is normally
// reached through a Cloudflare quick tunnel, which only relays ciphertext.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	"agentc/internal/agycli"
	"agentc/internal/core"
	"agentc/internal/fsops"
	"agentc/internal/push"
	"agentc/internal/secure"
	"agentc/internal/term"
)

// Version is the daemon version (set by main).
var Version = "dev"

// Server wires everything together.
type Server struct {
	Mgr       *core.Manager
	Terms     *term.Manager
	CLI       *agycli.CLI
	Push      *push.Pusher
	Identity  *secure.Identity
	Devices   *secure.Registry
	Tokens    *secure.Tokens
	APKPath   string
	PublicURL func() string         // the URL phones use ("" until the tunnel is up)
	Tunnel    func() map[string]any // tunnel status
	Started   time.Time
	Listeners func() []string
	ConfigSet func(map[string]any) (map[string]any, error)
	ConfigGet func() map[string]any

	clientSeq atomic.Int64
	upgrader  websocket.Upgrader
	methods   map[string]method
	termMu    sync.Mutex
	termState map[string]string
	limiter   *limiter
	uploads   *uploads
	pending   chan struct{} // bounds concurrent handshakes
}

// Handler returns the public HTTP handler.
func (s *Server) Handler() http.Handler {
	s.upgrader = websocket.Upgrader{
		ReadBufferSize:  64 * 1024,
		WriteBufferSize: 64 * 1024,
		// Any origin may open the socket; without a paired key (or the pairing
		// secret from the QR code) the handshake cannot succeed.
		CheckOrigin: func(r *http.Request) bool { return true },
	}
	s.limiter = newLimiter()
	s.uploads = newUploads()
	s.pending = make(chan struct{}, 32)
	s.registerMethods()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/ws", s.handleSecure)
	mux.HandleFunc("GET /p/{id}", s.handlePairPage)
	mux.HandleFunc("GET /p/{id}/app.apk", s.handlePairAPK)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) })
	return mux
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) hostName() string {
	h, _ := os.Hostname()
	return h
}

func (s *Server) publicURL() string {
	if s.PublicURL == nil {
		return ""
	}
	return s.PublicURL()
}

func (s *Server) info(ctx context.Context) map[string]any {
	m := map[string]any{
		"name": "agentc", "version": Version, "host": s.hostName(), "home": fsops.Home, "uptimeSec": int(time.Since(s.Started).Seconds()),
		"agents": s.Mgr.Agents(), "stats": s.Mgr.Stats(), "apk": s.apkInfo(),
		"hostId": s.Identity.ID(), "fingerprint": secure.Fingerprint(s.Identity.Key.Public), "publicUrl": s.publicURL(),
		"devices": len(s.Devices.List()),
	}
	if s.Push != nil {
		m["push"] = map[string]any{"configured": s.Push.Configured(), "content": s.Push.Content}
	}
	if s.Tunnel != nil {
		m["tunnel"] = s.Tunnel()
	}
	if s.Listeners != nil {
		m["listen"] = s.Listeners()
	}
	return m
}

func (s *Server) apkInfo() map[string]any {
	if s.APKPath == "" {
		return nil
	}
	st, err := os.Stat(s.APKPath)
	if err != nil {
		return nil
	}
	m := map[string]any{"size": st.Size(), "mtime": st.ModTime().UnixMilli()}
	if b, err := os.ReadFile(s.APKPath + ".json"); err == nil {
		var meta map[string]any
		if json.Unmarshal(b, &meta) == nil {
			for k, v := range meta {
				m[k] = v
			}
		}
	}
	return m
}

// ---- encrypted endpoint ----

// clientIP is the address used for throttling: behind cloudflared the TCP
// peer is loopback and Cloudflare supplies the real client address.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		if cf := strings.TrimSpace(r.Header.Get("Cf-Connecting-Ip")); cf != "" {
			return cf
		}
	}
	return host
}

func (s *Server) handleSecure(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	if !s.limiter.allow(ip) {
		http.Error(w, "too many failed attempts; try again later", http.StatusTooManyRequests)
		return
	}
	select {
	case s.pending <- struct{}{}:
	default:
		http.Error(w, "busy", http.StatusServiceUnavailable)
		return
	}
	ws, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		<-s.pending
		return
	}
	mode, pairID := r.URL.Query().Get("m"), r.URL.Query().Get("p")
	conn, err := secure.Accept(ws, mode, pairID, secure.ServerConfig{
		Identity: s.Identity, Devices: s.Devices, Tokens: s.Tokens,
		Reply: func(d *secure.Device, h secure.Hello) map[string]any {
			return map[string]any{"v": 1, "host": s.hostName(), "hostId": s.Identity.ID(), "version": Version,
				"deviceId": d.ID, "deviceName": d.Name, "publicUrl": s.publicURL()}
		},
		OnPaired: func(d secure.Device) {
			log.Printf("paired new device %q (%s) from %s", d.Name, d.ID, ip)
			s.Mgr.Hub().Broadcast("devices.changed", s.Devices.List())
		},
	})
	<-s.pending
	if err != nil {
		s.limiter.fail(ip)
		reason := "handshake-failed"
		switch {
		case errors.Is(err, secure.ErrUnknownDevice):
			reason = "unpaired"
		case errors.Is(err, secure.ErrPairing):
			reason = "pairing-invalid"
		}
		log.Printf("rejected connection from %s (%s): %v", ip, mode, err)
		_ = ws.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.ClosePolicyViolation, reason), time.Now().Add(2*time.Second))
		_ = ws.Close()
		return
	}
	switch conn.Hello.Purpose {
	case "term":
		s.serveTerm(conn)
	default:
		s.serveRPC(conn)
	}
}

// ---- RPC ----

type rpcReq struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

func (s *Server) serveRPC(conn *secure.Conn) {
	c := core.NewClient(fmt.Sprintf("c%d", s.clientSeq.Add(1)))
	c.Device, c.Peer = conn.Device.Name, conn.Device.ID
	hub := s.Mgr.Hub()
	hub.Add(c)
	log.Printf("client %s connected: %s (device %s)", c.ID, c.Device, c.Peer)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	c.OnClose = func() { _ = conn.Close() }
	conn.KeepAlive(25 * time.Second)
	go func() {
		defer close(done)
		for {
			select {
			case b, ok := <-c.Outbox():
				if !ok {
					_ = conn.Close()
					return
				}
				if err := conn.WriteMessage(secure.TextMessage, b); err != nil {
					_ = conn.Close()
					return
				}
			case <-conn.Done():
				return
			}
		}
	}()
	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			break
		}
		conn.ExtendRead(75 * time.Second)
		var req rpcReq
		if err := json.Unmarshal(data, &req); err != nil || req.Method == "" {
			continue
		}
		go s.dispatch(ctx, c, req)
	}
	cancel()
	hub.Remove(c)
	c.Close()
	_ = conn.Close()
	<-done
	s.Devices.Flush()
	log.Printf("client %s disconnected", c.ID)
}

func (s *Server) dispatch(ctx context.Context, c *core.Client, req rpcReq) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("panic in %s: %v", req.Method, r)
			reply(c, req.ID, nil, fmt.Errorf("internal error: %v", r))
		}
	}()
	m, ok := s.methods[req.Method]
	if !ok {
		reply(c, req.ID, nil, fmt.Errorf("unknown method %q", req.Method))
		return
	}
	res, err := m(ctx, c, req.Params)
	reply(c, req.ID, res, err)
}

func reply(c *core.Client, id json.RawMessage, res any, err error) {
	if len(id) == 0 {
		return
	}
	var b []byte
	if err != nil {
		b, _ = json.Marshal(map[string]any{"id": id, "error": map[string]any{"message": err.Error()}})
	} else {
		var merr error
		b, merr = json.Marshal(map[string]any{"id": id, "result": res})
		if merr != nil {
			b, _ = json.Marshal(map[string]any{"id": id, "error": map[string]any{"message": "encode: " + merr.Error()}})
		}
	}
	c.SendBytes(b)
}

// ---- terminals ----

type termCtl struct {
	Type string `json:"type"`
	Cols int    `json:"cols"`
	Rows int    `json:"rows"`
	Data string `json:"data"`
}

func (s *Server) serveTerm(conn *secure.Conn) {
	defer conn.Close()
	h := conn.Hello
	fail := func(msg string) {
		b, _ := json.Marshal(map[string]any{"type": "error", "message": msg})
		_ = conn.WriteMessage(secure.TextMessage, b)
	}
	if _, err := s.Terms.Get(context.Background(), h.Term); err != nil {
		fail(err.Error())
		return
	}
	// Wait for the client's first message (proves it holds the session keys)
	// before attaching.
	conn.KeepAlive(25 * time.Second)
	mt, data, err := conn.ReadMessage()
	if err != nil {
		return
	}
	att, err := s.Terms.Attach(h.Term, h.Cols, h.Rows)
	if err != nil {
		fail(err.Error())
		return
	}
	defer att.Close()
	handle := func(mt int, data []byte) bool {
		switch mt {
		case secure.BinaryMessage:
			if _, err := att.PTY.Write(data); err != nil {
				return false
			}
		case secure.TextMessage:
			var ctl termCtl
			if json.Unmarshal(data, &ctl) != nil {
				return true
			}
			switch ctl.Type {
			case "resize":
				_ = att.Resize(ctl.Cols, ctl.Rows)
			case "input":
				_, _ = att.PTY.Write([]byte(ctl.Data))
			}
		}
		return true
	}
	handle(mt, data)
	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 32*1024)
		for {
			n, err := att.PTY.Read(buf)
			if n > 0 {
				if werr := conn.WriteMessage(secure.BinaryMessage, buf[:n]); werr != nil {
					break
				}
			}
			if err != nil {
				break
			}
		}
		_ = conn.WriteMessage(secure.TextMessage, []byte(`{"type":"exit"}`))
		_ = conn.Close()
	}()
	for {
		mt, data, err := conn.ReadMessage()
		if err != nil {
			break
		}
		conn.ExtendRead(75 * time.Second)
		if !handle(mt, data) {
			break
		}
	}
	att.Close()
	<-done
}

// ---- pairing landing page (exists only while a pairing code is open) ----

var pairTmpl = template.Must(template.New("p").Parse(`<!doctype html><html><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1"><meta name="robots" content="noindex">
<title>Pair with {{.Host}}</title>
<style>body{font-family:system-ui,sans-serif;margin:0;padding:28px 18px;background:#101114;color:#e9e9ec;max-width:560px}
a.btn{display:block;text-align:center;margin:14px 0;padding:14px 18px;border-radius:12px;background:#8ab4ff;color:#0b1020;text-decoration:none;font-weight:600}
a.sec{background:#26282e;color:#e9e9ec}.muted{color:#9a9ca5;font-size:14px;line-height:1.45}code{background:#24262c;padding:2px 6px;border-radius:6px}</style></head><body>
<h2>Pair with {{.Host}}</h2>
<p class="muted">This link pairs the Agent Companion app with the agentc daemon on <b>{{.Host}}</b>. It works once and expires in a few minutes.
Fingerprint: <code>{{.Fingerprint}}</code></p>
<a class="btn" id="open" href="#">Open in Agent Companion</a>
{{if .APK}}<a class="btn sec" href="/p/{{.ID}}/app.apk">Download the Android app ({{.APKSize}})</a>{{end}}
<p class="muted">If the app doesn't open, install it first, then scan the QR code from inside the app.</p>
<script>
var h=location.hash.replace(/^#/,'');var q=new URLSearchParams(h);
q.set('u',location.origin);q.set('p',{{.ID}});
document.getElementById('open').href='intent://pair?'+q.toString()+'#Intent;scheme=agentc;package=io.github.sidd065.agentcompanion;end';
</script></body></html>`))

func (s *Server) handlePairPage(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, ok := s.Tokens.Get(id); !ok {
		http.NotFound(w, r)
		return
	}
	data := map[string]any{"Host": s.hostName(), "ID": id, "Fingerprint": secure.Fingerprint(s.Identity.Key.Public)}
	if a := s.apkInfo(); a != nil {
		data["APK"] = true
		data["APKSize"] = fmt.Sprintf("%.1f MB", float64(a["size"].(int64))/1e6)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Robots-Tag", "noindex")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'")
	_ = pairTmpl.Execute(w, data)
}

func (s *Server) handlePairAPK(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.Tokens.Get(r.PathValue("id")); !ok || s.APKPath == "" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/vnd.android.package-archive")
	w.Header().Set("Content-Disposition", `attachment; filename="agent-companion.apk"`)
	w.Header().Set("Cache-Control", "no-store")
	http.ServeFile(w, r, s.APKPath)
}

// ---- local (unix socket) API for the CLI, agyr and osc-watch ----

// LocalHandler serves the unix-socket API (filesystem permissions are the auth).
func (s *Server) LocalHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /local/term-state", func(w http.ResponseWriter, r *http.Request) {
		var st struct {
			ID    string `json:"id"`
			State string `json:"state"`
			Msg   string `json:"msg"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 64*1024)).Decode(&st); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		s.onTermState(st.ID, st.State, st.Msg)
		w.WriteHeader(204)
	})
	mux.HandleFunc("GET /local/status", func(w http.ResponseWriter, r *http.Request) {
		info := s.info(r.Context())
		info["deviceList"] = s.Devices.List()
		writeJSON(w, 200, info)
	})
	mux.HandleFunc("POST /local/pair", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			TTLSeconds int    `json:"ttlSeconds"`
			URL        string `json:"url"` // override the public URL (e.g. a LAN address)
		}
		_ = json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&in)
		base := strings.TrimRight(in.URL, "/")
		if base == "" {
			base = s.publicURL()
		}
		if base == "" {
			writeJSON(w, 503, map[string]any{"error": "the tunnel isn't up yet; check `agentc status` and try again in a few seconds"})
			return
		}
		ttl := time.Duration(in.TTLSeconds) * time.Second
		if ttl <= 0 || ttl > time.Hour {
			ttl = 10 * time.Minute
		}
		tok, err := s.Tokens.Create(ttl)
		if err != nil {
			writeJSON(w, 429, map[string]any{"error": err.Error()})
			return
		}
		frag := "k=" + s.Identity.PublicB64() + "&s=" + b64url(tok.PSK) + "&n=" + urlQueryEscape(s.hostName())
		writeJSON(w, 200, map[string]any{
			"url": base + "/p/" + tok.ID + "#" + frag, "base": base, "expires": tok.Expires.UnixMilli(),
			"host": s.hostName(), "fingerprint": secure.Fingerprint(s.Identity.Key.Public),
		})
	})
	mux.HandleFunc("GET /local/devices", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, s.Devices.List())
	})
	mux.HandleFunc("POST /local/devices/revoke", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			ID string `json:"id"`
		}
		_ = json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&in)
		ok, err := s.revokeDevice(in.ID)
		if err != nil {
			writeJSON(w, 500, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, 200, map[string]any{"removed": ok})
	})
	return mux
}

// revokeDevice unpairs a device and drops its connections and push tokens.
func (s *Server) revokeDevice(id string) (bool, error) {
	ok, err := s.Devices.Remove(id)
	if err != nil || !ok {
		return ok, err
	}
	if s.Push != nil {
		s.Push.UnregisterDevice(id)
	}
	for _, c := range s.Mgr.Hub().Clients() {
		if c.Peer == id {
			c.Close()
		}
	}
	s.Mgr.Hub().Broadcast("devices.changed", s.Devices.List())
	log.Printf("unpaired device %s", id)
	return true, nil
}

func (s *Server) onTermState(id, state, msg string) {
	s.termMu.Lock()
	if s.termState == nil {
		s.termState = map[string]string{}
	}
	prev := s.termState[id]
	s.termState[id] = state
	s.termMu.Unlock()
	terms, _ := s.Terms.List(context.Background())
	s.Mgr.Hub().Broadcast("terms.changed", terms)
	if prev == state {
		return
	}
	name := id
	for _, t := range terms {
		if t.ID == id {
			name = t.Name
		}
	}
	kind := ""
	switch {
	case state == "done":
		kind = "term_done"
	case strings.HasPrefix(state, "blocked"):
		kind = "term_blocked"
	case state == "failed":
		kind = "term_failed"
	}
	if kind == "" {
		return
	}
	title := name
	body := msg
	if body == "" {
		switch kind {
		case "term_done":
			body = "agy finished"
		case "term_blocked":
			body = "agy is waiting for you"
		default:
			body = "agy failed"
		}
	}
	s.Mgr.Notify(core.Notification{TermID: id, Kind: kind, Title: title, Body: body})
}

// ListenLocal serves the local API on a unix socket.
func (s *Server) ListenLocal(path string) (net.Listener, error) {
	_ = os.Remove(path)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	_ = os.Chmod(path, 0o600)
	go func() {
		srv := &http.Server{Handler: s.LocalHandler(), ReadHeaderTimeout: 10 * time.Second}
		if err := srv.Serve(ln); err != nil && !errors.Is(err, net.ErrClosed) {
			log.Printf("local api: %v", err)
		}
	}()
	return ln, nil
}
