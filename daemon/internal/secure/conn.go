package secure

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/flynn/noise"
	"github.com/gorilla/websocket"
)

// Message types (mirroring gorilla/websocket).
const (
	TextMessage   = websocket.TextMessage
	BinaryMessage = websocket.BinaryMessage
)

const (
	maxNoise    = 65535
	maxFragment = maxNoise - 16 - 1 // tag + flags byte
	// MaxMessage bounds one reassembled logical message.
	MaxMessage = 64 << 20

	flagFinal  = 1
	flagBinary = 2
)

// Errors returned by Accept.
var (
	ErrUnknownDevice = errors.New("unknown device")
	ErrPairing       = errors.New("pairing code is invalid or expired")
	ErrHandshake     = errors.New("handshake failed")
)

// Hello is the first handshake payload a client sends.
type Hello struct {
	V        int    `json:"v"`
	Purpose  string `json:"purpose"` // rpc | term
	Term     string `json:"term,omitempty"`
	Cols     int    `json:"cols,omitempty"`
	Rows     int    `json:"rows,omitempty"`
	Name     string `json:"name,omitempty"`
	Platform string `json:"platform,omitempty"`
	App      string `json:"app,omitempty"`
}

// Conn is an encrypted message connection.
type Conn struct {
	ws     *websocket.Conn
	enc    *noise.CipherState
	dec    *noise.CipherState
	wmu    sync.Mutex
	rmu    sync.Mutex
	Device *Device // the authenticated peer (server side)
	Hello  Hello   // the client's hello (server side)
	Reply  map[string]any
	closed chan struct{}
	once   sync.Once
}

func newConn(ws *websocket.Conn, enc, dec *noise.CipherState) *Conn {
	return &Conn{ws: ws, enc: enc, dec: dec, closed: make(chan struct{})}
}

// WriteMessage encrypts and sends one logical message (fragmenting it).
func (c *Conn) WriteMessage(mt int, data []byte) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	pt := make([]byte, 0, min(len(data), maxFragment)+1)
	for first := true; first || len(data) > 0; first = false {
		n := min(len(data), maxFragment)
		var flags byte
		if n == len(data) {
			flags |= flagFinal
		}
		if mt == BinaryMessage {
			flags |= flagBinary
		}
		pt = append(append(pt[:0], flags), data[:n]...)
		ct, err := c.enc.Encrypt(nil, nil, pt)
		if err != nil {
			return err
		}
		_ = c.ws.SetWriteDeadline(time.Now().Add(30 * time.Second))
		if err := c.ws.WriteMessage(websocket.BinaryMessage, ct); err != nil {
			return err
		}
		data = data[n:]
	}
	return nil
}

// ReadMessage returns the next decrypted logical message.
func (c *Conn) ReadMessage() (int, []byte, error) {
	c.rmu.Lock()
	defer c.rmu.Unlock()
	var buf []byte
	for {
		t, ct, err := c.ws.ReadMessage()
		if err != nil {
			return 0, nil, err
		}
		if t != websocket.BinaryMessage {
			return 0, nil, errors.New("secure: unexpected plaintext frame")
		}
		pt, err := c.dec.Decrypt(nil, nil, ct)
		if err != nil {
			return 0, nil, fmt.Errorf("secure: %w", err)
		}
		if len(pt) == 0 {
			return 0, nil, errors.New("secure: empty frame")
		}
		buf = append(buf, pt[1:]...)
		if len(buf) > MaxMessage {
			return 0, nil, errors.New("secure: message too large")
		}
		if pt[0]&flagFinal != 0 {
			mt := TextMessage
			if pt[0]&flagBinary != 0 {
				mt = BinaryMessage
			}
			if buf == nil {
				buf = []byte{}
			}
			return mt, buf, nil
		}
	}
}

// Ping sends a WebSocket ping (keeps relays from timing out idle connections).
func (c *Conn) Ping() error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	return c.ws.WriteControl(websocket.PingMessage, nil, time.Now().Add(10*time.Second))
}

// SetReadDeadline bounds the next read; pongs extend it via KeepAlive.
func (c *Conn) SetReadDeadline(t time.Time) error { return c.ws.SetReadDeadline(t) }

// KeepAlive pings every interval and expects traffic or pongs within 3 intervals.
func (c *Conn) KeepAlive(interval time.Duration) {
	_ = c.ws.SetReadDeadline(time.Now().Add(3 * interval))
	c.ws.SetPongHandler(func(string) error { return c.ws.SetReadDeadline(time.Now().Add(3 * interval)) })
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-c.closed:
				return
			case <-t.C:
				if err := c.Ping(); err != nil {
					_ = c.Close()
					return
				}
			}
		}
	}()
}

// ExtendRead pushes the read deadline after application traffic.
func (c *Conn) ExtendRead(d time.Duration) { _ = c.ws.SetReadDeadline(time.Now().Add(d)) }

// Close closes the underlying WebSocket.
func (c *Conn) Close() error {
	var err error
	c.once.Do(func() {
		close(c.closed)
		c.wmu.Lock()
		_ = c.ws.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(2*time.Second))
		c.wmu.Unlock()
		err = c.ws.Close()
	})
	return err
}

// Done is closed when the connection is closed locally.
func (c *Conn) Done() <-chan struct{} { return c.closed }

// ServerConfig is what the daemon needs to accept connections.
type ServerConfig struct {
	Identity *Identity
	Devices  *Registry
	Tokens   *Tokens
	// Reply builds the handshake response payload for an authenticated peer.
	Reply func(d *Device, h Hello) map[string]any
	// OnPaired is called after a new device pairs.
	OnPaired func(d Device)
}

// Accept performs the responder side of the handshake on a fresh WebSocket.
// mode and pairID come from the request URL (they are not secret).
func Accept(ws *websocket.Conn, mode, pairID string, cfg ServerConfig) (*Conn, error) {
	_ = ws.SetReadDeadline(time.Now().Add(20 * time.Second))
	t, msg1, err := ws.ReadMessage()
	if err != nil {
		return nil, err
	}
	if t != websocket.BinaryMessage {
		return nil, ErrHandshake
	}
	hc := noise.Config{CipherSuite: Suite, Pattern: noise.HandshakeIK, Initiator: false,
		StaticKeypair: cfg.Identity.Key, Prologue: Prologue(mode, pairID)}
	var tok *PairToken
	switch mode {
	case ModePair:
		var ok bool
		if tok, ok = cfg.Tokens.Get(pairID); !ok {
			return nil, ErrPairing
		}
		hc.PresharedKey, hc.PresharedKeyPlacement = tok.PSK, 1
	case ModeConnect:
	default:
		return nil, ErrHandshake
	}
	hs, err := noise.NewHandshakeState(hc)
	if err != nil {
		return nil, err
	}
	payload, _, _, err := hs.ReadMessage(nil, msg1)
	if err != nil {
		if mode == ModePair {
			return nil, ErrPairing
		}
		return nil, ErrHandshake
	}
	var hello Hello
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &hello); err != nil {
			return nil, ErrHandshake
		}
	}
	peer := hs.PeerStatic()
	var dev *Device
	if mode == ModePair {
		// Single use: only the first successful handshake may claim the code.
		if !cfg.Tokens.Take(pairID) {
			return nil, ErrPairing
		}
		name := strings.TrimSpace(hello.Name)
		if name == "" {
			name = "Phone"
		}
		d, err := cfg.Devices.Add(peer, name, hello.Platform, hello.App)
		if err != nil {
			return nil, err
		}
		dev = &d
		if cfg.OnPaired != nil {
			cfg.OnPaired(d)
		}
	} else if dev = cfg.Devices.Lookup(peer); dev == nil {
		return nil, ErrUnknownDevice
	} else {
		cfg.Devices.Touch(dev.ID, hello.App)
	}
	reply := map[string]any{"v": 1}
	if cfg.Reply != nil {
		reply = cfg.Reply(dev, hello)
	}
	rb, _ := json.Marshal(reply)
	msg2, cs1, cs2, err := hs.WriteMessage(nil, rb)
	if err != nil {
		return nil, err
	}
	if cs1 == nil || cs2 == nil {
		return nil, ErrHandshake
	}
	_ = ws.SetWriteDeadline(time.Now().Add(15 * time.Second))
	if err := ws.WriteMessage(websocket.BinaryMessage, msg2); err != nil {
		return nil, err
	}
	_ = ws.SetReadDeadline(time.Time{})
	c := newConn(ws, cs2, cs1)
	c.Device, c.Hello = dev, hello
	return c, nil
}

// ClientConfig configures Dial (used by agentctl and tests; the app has its
// own implementation).
type ClientConfig struct {
	URL       string // daemon base URL: https://x.trycloudflare.com or http://127.0.0.1:9473
	DaemonKey []byte
	DeviceKey noise.DHKey
	Mode      string
	PairID    string
	PSK       []byte
	Hello     Hello
	Header    http.Header
}

// WSURL converts a daemon base URL into its WebSocket endpoint.
func WSURL(base, mode, pairID string) (string, error) {
	u, err := url.Parse(strings.TrimRight(base, "/"))
	if err != nil {
		return "", err
	}
	switch u.Scheme {
	case "https", "wss":
		u.Scheme = "wss"
	case "http", "ws":
		u.Scheme = "ws"
	default:
		return "", fmt.Errorf("unsupported URL scheme %q", u.Scheme)
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/v1/ws"
	q := url.Values{"m": {mode}}
	if mode == ModePair {
		q.Set("p", pairID)
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// Dial connects and performs the initiator handshake.
func Dial(ctx context.Context, cfg ClientConfig) (*Conn, error) {
	if cfg.Mode == "" {
		cfg.Mode = ModeConnect
	}
	wsURL, err := WSURL(cfg.URL, cfg.Mode, cfg.PairID)
	if err != nil {
		return nil, err
	}
	d := websocket.Dialer{HandshakeTimeout: 20 * time.Second, ReadBufferSize: 64 << 10, WriteBufferSize: 64 << 10}
	ws, res, err := d.DialContext(ctx, wsURL, cfg.Header)
	if err != nil {
		if res != nil {
			return nil, fmt.Errorf("%w (HTTP %d)", err, res.StatusCode)
		}
		return nil, err
	}
	hc := noise.Config{CipherSuite: Suite, Pattern: noise.HandshakeIK, Initiator: true,
		StaticKeypair: cfg.DeviceKey, PeerStatic: cfg.DaemonKey, Prologue: Prologue(cfg.Mode, cfg.PairID)}
	if cfg.Mode == ModePair {
		hc.PresharedKey, hc.PresharedKeyPlacement = cfg.PSK, 1
	}
	hs, err := noise.NewHandshakeState(hc)
	if err != nil {
		ws.Close()
		return nil, err
	}
	if cfg.Hello.V == 0 {
		cfg.Hello.V = 1
	}
	hb, _ := json.Marshal(cfg.Hello)
	msg1, _, _, err := hs.WriteMessage(nil, hb)
	if err != nil {
		ws.Close()
		return nil, err
	}
	if err := ws.WriteMessage(websocket.BinaryMessage, msg1); err != nil {
		ws.Close()
		return nil, err
	}
	_ = ws.SetReadDeadline(time.Now().Add(20 * time.Second))
	t, msg2, err := ws.ReadMessage()
	if err != nil {
		ws.Close()
		var ce *websocket.CloseError
		if errors.As(err, &ce) && ce.Text != "" {
			return nil, errors.New(ce.Text)
		}
		return nil, fmt.Errorf("handshake: %w", err)
	}
	if t != websocket.BinaryMessage {
		ws.Close()
		return nil, ErrHandshake
	}
	payload, cs1, cs2, err := hs.ReadMessage(nil, msg2)
	if err != nil {
		ws.Close()
		return nil, fmt.Errorf("handshake: the server is not the expected host (%v)", err)
	}
	_ = ws.SetReadDeadline(time.Time{})
	c := newConn(ws, cs1, cs2)
	_ = json.Unmarshal(payload, &c.Reply)
	return c, nil
}
