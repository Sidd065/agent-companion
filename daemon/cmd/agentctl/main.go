// agentctl is a command-line client for the agentc daemon (scripting and
// integration tests). It pairs like a phone and talks the same end-to-end
// encrypted protocol:
//
//	agentctl pair LINK            pair using a link from `agentc pair`
//	agentctl pair --local         pair through the daemon's local socket (tests)
//	agentctl call METHOD [JSON]   one RPC
//	agentctl watch                print events
//	agentctl itest [scenario...]  end-to-end tests
package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/flynn/noise"

	"agentc/internal/secure"
)

// Creds is a paired host as stored by agentctl.
type Creds struct {
	URL        string `json:"url"`
	DaemonKey  string `json:"daemonKey"`
	DevicePriv string `json:"devicePriv"`
	DevicePub  string `json:"devicePub"`
	Host       string `json:"host,omitempty"`
}

func (c *Creds) keys() (daemon []byte, dev noise.DHKey, err error) {
	daemon, err = base64.RawURLEncoding.DecodeString(c.DaemonKey)
	if err != nil {
		return
	}
	dev.Private, err = base64.StdEncoding.DecodeString(c.DevicePriv)
	if err != nil {
		return
	}
	dev.Public, err = base64.StdEncoding.DecodeString(c.DevicePub)
	return
}

// Client is a minimal RPC client over a secure connection.
type Client struct {
	conn    *secure.Conn
	creds   *Creds
	nextID  atomic.Int64
	mu      sync.Mutex
	pending map[int64]chan map[string]any
	Events  chan map[string]any
}

func dial(cr *Creds, purpose secure.Hello) (*secure.Conn, error) {
	dk, dev, err := cr.keys()
	if err != nil {
		return nil, fmt.Errorf("credentials: %w", err)
	}
	if purpose.Name == "" {
		purpose.Name, purpose.Platform, purpose.App = "agentctl", "cli", "agentctl"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return secure.Dial(ctx, secure.ClientConfig{URL: cr.URL, DaemonKey: dk, DeviceKey: dev, Hello: purpose})
}

func connect(cr *Creds) (*Client, error) {
	conn, err := dial(cr, secure.Hello{Purpose: "rpc"})
	if err != nil {
		return nil, err
	}
	c := &Client{conn: conn, creds: cr, pending: map[int64]chan map[string]any{}, Events: make(chan map[string]any, 10000)}
	go c.read()
	return c, nil
}

func (c *Client) read() {
	for {
		_, b, err := c.conn.ReadMessage()
		if err != nil {
			close(c.Events)
			return
		}
		var m map[string]any
		if json.Unmarshal(b, &m) != nil {
			continue
		}
		if idv, ok := m["id"]; ok && m["event"] == nil {
			id := int64(idv.(float64))
			c.mu.Lock()
			ch := c.pending[id]
			delete(c.pending, id)
			c.mu.Unlock()
			if ch != nil {
				ch <- m
			}
			continue
		}
		c.Events <- m
	}
}

// Call performs an RPC.
func (c *Client) Call(method string, params any, timeout time.Duration) (any, error) {
	id := c.nextID.Add(1)
	ch := make(chan map[string]any, 1)
	c.mu.Lock()
	c.pending[id] = ch
	c.mu.Unlock()
	b, _ := json.Marshal(map[string]any{"id": id, "method": method, "params": params})
	if err := c.conn.WriteMessage(secure.TextMessage, b); err != nil {
		return nil, err
	}
	if timeout == 0 {
		timeout = 2 * time.Minute
	}
	select {
	case m := <-ch:
		if e, ok := m["error"].(map[string]any); ok {
			return nil, fmt.Errorf("%v", e["message"])
		}
		return m["result"], nil
	case <-time.After(timeout):
		return nil, fmt.Errorf("timeout calling %s", method)
	}
}

// parseLink understands both QR forms:
//
//	https://host/p/<pairId>#k=<key>&s=<secret>&n=<name>
//	agentc://pair?u=<base>&p=<pairId>&k=<key>&s=<secret>&n=<name>
func parseLink(link string) (base, pairID string, key, psk []byte, name string, err error) {
	u, err := url.Parse(strings.TrimSpace(link))
	if err != nil {
		return
	}
	var q url.Values
	if u.Scheme == "agentc" {
		q = u.Query()
		base, pairID = q.Get("u"), q.Get("p")
	} else {
		q, err = url.ParseQuery(u.Fragment)
		if err != nil {
			return
		}
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		if len(parts) != 2 || parts[0] != "p" {
			err = errors.New("not a pairing link")
			return
		}
		base, pairID = u.Scheme+"://"+u.Host, parts[1]
	}
	if key, err = base64.RawURLEncoding.DecodeString(q.Get("k")); err != nil || len(key) != 32 {
		err = errors.New("pairing link: bad host key")
		return
	}
	if psk, err = base64.RawURLEncoding.DecodeString(q.Get("s")); err != nil || len(psk) != 32 {
		err = errors.New("pairing link: bad secret")
		return
	}
	return base, pairID, key, psk, q.Get("n"), nil
}

func pair(link, credsPath, overrideURL string) error {
	base, pairID, key, psk, name, err := parseLink(link)
	if err != nil {
		return err
	}
	if overrideURL != "" {
		base = overrideURL
	}
	dev, err := noise.DH25519.GenerateKeypair(rand.Reader)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, err := secure.Dial(ctx, secure.ClientConfig{URL: base, DaemonKey: key, DeviceKey: dev, Mode: secure.ModePair, PairID: pairID, PSK: psk,
		Hello: secure.Hello{Purpose: "rpc", Name: "agentctl", Platform: "cli", App: "agentctl"}})
	if err != nil {
		return err
	}
	conn.Close()
	cr := Creds{URL: base, DaemonKey: base64.RawURLEncoding.EncodeToString(key), DevicePriv: base64.StdEncoding.EncodeToString(dev.Private),
		DevicePub: base64.StdEncoding.EncodeToString(dev.Public), Host: name}
	b, _ := json.MarshalIndent(cr, "", "  ")
	if err := os.MkdirAll(filepath.Dir(credsPath), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(credsPath, b, 0o600); err != nil {
		return err
	}
	fmt.Printf("paired with %s (%s); credentials in %s\n", name, base, credsPath)
	return nil
}

func localPairLink(sock, url string) (string, error) {
	hc := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", sock)
	}}}
	b, _ := json.Marshal(map[string]any{"url": url})
	res, err := hc.Post("http://agentc/local/pair", "application/json", strings.NewReader(string(b)))
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(res.Body).Decode(&out)
	if res.StatusCode != 200 {
		return "", fmt.Errorf("%v", out["error"])
	}
	return fmt.Sprint(out["url"]), nil
}

func defaultSock() string {
	rt := os.Getenv("XDG_RUNTIME_DIR")
	if rt == "" {
		rt = filepath.Join(os.TempDir(), fmt.Sprintf("agentc-%d", os.Getuid()))
	}
	return filepath.Join(rt, "agentc", "agentc.sock")
}

func main() {
	home, _ := os.UserHomeDir()
	credsPath := flag.String("creds", envOr("AGENTCTL_CREDS", filepath.Join(home, ".config", "agentctl", "host.json")), "paired host credentials file")
	urlFlag := flag.String("url", os.Getenv("AGENTCTL_URL"), "override the daemon URL (e.g. http://127.0.0.1:9473)")
	sock := flag.String("sock", envOr("AGENTC_SOCK", defaultSock()), "daemon local socket (for pair --local)")
	flag.Parse()
	args := flag.Args()
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: agentctl [--creds FILE] [--url URL] pair LINK|--local | call METHOD [JSON] | watch | itest [scenario...]")
		os.Exit(2)
	}
	if args[0] == "pair" {
		link := ""
		if len(args) > 1 && args[1] != "--local" {
			link = args[1]
		} else {
			base := *urlFlag
			if base == "" {
				base = "http://127.0.0.1:9473"
			}
			var err error
			if link, err = localPairLink(*sock, base); err != nil {
				fmt.Fprintln(os.Stderr, "pair:", err)
				os.Exit(1)
			}
		}
		if err := pair(link, *credsPath, *urlFlag); err != nil {
			fmt.Fprintln(os.Stderr, "pair:", err)
			os.Exit(1)
		}
		return
	}
	b, err := os.ReadFile(*credsPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "not paired yet (agentctl pair …):", err)
		os.Exit(1)
	}
	var cr Creds
	if err := json.Unmarshal(b, &cr); err != nil {
		fmt.Fprintln(os.Stderr, "credentials:", err)
		os.Exit(1)
	}
	if *urlFlag != "" {
		cr.URL = *urlFlag
	}
	c, err := connect(&cr)
	if err != nil {
		fmt.Fprintln(os.Stderr, "connect:", err)
		os.Exit(1)
	}
	switch args[0] {
	case "call":
		var params any = map[string]any{}
		if len(args) > 2 {
			if err := json.Unmarshal([]byte(args[2]), &params); err != nil {
				fmt.Fprintln(os.Stderr, "bad JSON params:", err)
				os.Exit(2)
			}
		}
		res, err := c.Call(args[1], params, 10*time.Minute)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		out, _ := json.MarshalIndent(res, "", "  ")
		fmt.Println(string(out))
	case "watch":
		for ev := range c.Events {
			b, _ := json.Marshal(ev)
			s := string(b)
			if len(s) > 400 {
				s = s[:400] + "…"
			}
			fmt.Println(s)
		}
	case "itest":
		os.Exit(itest(c, &cr, args[1:]))
	default:
		fmt.Fprintln(os.Stderr, "unknown command", args[0])
		os.Exit(2)
	}
}

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
