package secure

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/flynn/noise"
	"github.com/gorilla/websocket"
)

type testServer struct {
	id     *Identity
	reg    *Registry
	tokens *Tokens
	srv    *httptest.Server
	conns  chan *Conn
	errs   chan error
}

func newTestServer(t *testing.T) *testServer {
	t.Helper()
	dir := t.TempDir()
	id, err := LoadOrCreateIdentity(filepath.Join(dir, "identity.json"))
	if err != nil {
		t.Fatal(err)
	}
	reg, err := OpenRegistry(filepath.Join(dir, "devices.json"))
	if err != nil {
		t.Fatal(err)
	}
	ts := &testServer{id: id, reg: reg, tokens: NewTokens(), conns: make(chan *Conn, 4), errs: make(chan error, 4)}
	up := websocket.Upgrader{}
	ts.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		c, err := Accept(ws, r.URL.Query().Get("m"), r.URL.Query().Get("p"), ServerConfig{Identity: id, Devices: reg, Tokens: ts.tokens,
			Reply: func(d *Device, h Hello) map[string]any {
				return map[string]any{"v": 1, "device": d.ID, "purpose": h.Purpose}
			}})
		if err != nil {
			_ = ws.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.ClosePolicyViolation, err.Error()), time.Now().Add(time.Second))
			ws.Close()
			ts.errs <- err
			return
		}
		ts.conns <- c
	}))
	t.Cleanup(ts.srv.Close)
	return ts
}

func newKey(t *testing.T) noise.DHKey {
	k, err := noise.DH25519.GenerateKeypair(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func (ts *testServer) pair(t *testing.T, dev noise.DHKey) *Conn {
	t.Helper()
	tok, err := ts.tokens.Create(time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	c, err := Dial(context.Background(), ClientConfig{URL: ts.srv.URL, DaemonKey: ts.id.Key.Public, DeviceKey: dev, Mode: ModePair,
		PairID: tok.ID, PSK: tok.PSK, Hello: Hello{Purpose: "rpc", Name: "Test phone", Platform: "android"}})
	if err != nil {
		t.Fatalf("pair: %v", err)
	}
	return c
}

func TestPairThenConnectAndRoundTrip(t *testing.T) {
	ts := newTestServer(t)
	dev := newKey(t)
	c := ts.pair(t, dev)
	srv := <-ts.conns
	if srv.Device == nil || srv.Device.Name != "Test phone" || c.Reply["device"] != srv.Device.ID {
		t.Fatalf("paired device mismatch: %+v / %v", srv.Device, c.Reply)
	}
	if len(ts.reg.List()) != 1 {
		t.Fatalf("registry: %v", ts.reg.List())
	}
	c.Close()
	srv.Close()

	c, err := Dial(context.Background(), ClientConfig{URL: ts.srv.URL, DaemonKey: ts.id.Key.Public, DeviceKey: dev, Hello: Hello{Purpose: "term", Term: "t1"}})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer c.Close()
	srv = <-ts.conns
	defer srv.Close()
	if srv.Hello.Purpose != "term" || srv.Hello.Term != "t1" || c.Reply["purpose"] != "term" {
		t.Fatalf("hello not carried: %+v %v", srv.Hello, c.Reply)
	}
	big := make([]byte, 3*maxFragment+123) // several fragments
	_, _ = rand.Read(big)
	cases := []struct {
		mt   int
		data []byte
	}{{TextMessage, []byte(`{"id":1,"method":"hello"}`)}, {BinaryMessage, big}, {TextMessage, []byte{}}, {BinaryMessage, []byte{0}}}
	for i, tc := range cases {
		if err := c.WriteMessage(tc.mt, tc.data); err != nil {
			t.Fatal(err)
		}
		mt, got, err := srv.ReadMessage()
		if err != nil || mt != tc.mt || !bytes.Equal(got, tc.data) {
			t.Fatalf("case %d client→server: mt=%d len=%d err=%v", i, mt, len(got), err)
		}
		if err := srv.WriteMessage(tc.mt, tc.data); err != nil {
			t.Fatal(err)
		}
		mt, got, err = c.ReadMessage()
		if err != nil || mt != tc.mt || !bytes.Equal(got, tc.data) {
			t.Fatalf("case %d server→client: mt=%d len=%d err=%v", i, mt, len(got), err)
		}
	}
}

func TestRejectsStrangersAndBadPairing(t *testing.T) {
	ts := newTestServer(t)
	// Unpaired device key.
	_, err := Dial(context.Background(), ClientConfig{URL: ts.srv.URL, DaemonKey: ts.id.Key.Public, DeviceKey: newKey(t)})
	if err == nil {
		t.Fatal("unknown device connected")
	}
	if e := <-ts.errs; !errors.Is(e, ErrUnknownDevice) {
		t.Fatalf("server error = %v", e)
	}
	// Wrong pairing secret: rejected, and the code stays usable.
	tok, _ := ts.tokens.Create(time.Minute)
	_, err = Dial(context.Background(), ClientConfig{URL: ts.srv.URL, DaemonKey: ts.id.Key.Public, DeviceKey: newKey(t), Mode: ModePair,
		PairID: tok.ID, PSK: bytes.Repeat([]byte{7}, 32)})
	if err == nil || !errors.Is(<-ts.errs, ErrPairing) {
		t.Fatalf("bad psk accepted: %v", err)
	}
	if _, ok := ts.tokens.Get(tok.ID); !ok {
		t.Fatal("token consumed by a failed attempt")
	}
	// Correct secret pairs once; the code can't be reused.
	dev := newKey(t)
	cfg := ClientConfig{URL: ts.srv.URL, DaemonKey: ts.id.Key.Public, DeviceKey: dev, Mode: ModePair, PairID: tok.ID, PSK: tok.PSK}
	c, err := Dial(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	(<-ts.conns).Close()
	cfg.DeviceKey = newKey(t)
	if _, err := Dial(context.Background(), cfg); err == nil {
		t.Fatal("pairing code reused")
	}
	<-ts.errs
	// An impostor daemon (different identity) can't complete the handshake.
	other := newKey(t)
	if _, err := Dial(context.Background(), ClientConfig{URL: ts.srv.URL, DaemonKey: other.Public, DeviceKey: dev}); err == nil {
		t.Fatal("handshake succeeded against the wrong daemon key")
	}
	<-ts.errs
}

func TestRegistryPersistsAndRemoves(t *testing.T) {
	path := filepath.Join(t.TempDir(), "devices.json")
	r, _ := OpenRegistry(path)
	k := make([]byte, 32)
	k[0] = 9
	d, err := r.Add(k, "A", "android", "0.2.0")
	if err != nil {
		t.Fatal(err)
	}
	r2, err := OpenRegistry(path)
	if err != nil || r2.Lookup(k) == nil || r2.Lookup(k).Name != "A" {
		t.Fatalf("reload: %v", err)
	}
	if ok, _ := r2.Remove(d.ID); !ok || r2.Lookup(k) != nil {
		t.Fatal("remove failed")
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Fatalf("devices.json mode %v", fi.Mode().Perm())
	}
}

func TestWSURL(t *testing.T) {
	u, _ := WSURL("https://a-b.trycloudflare.com/", ModePair, "XYZ")
	if u != "wss://a-b.trycloudflare.com/v1/ws?m=pair&p=XYZ" {
		t.Fatal(u)
	}
	u, _ = WSURL("http://127.0.0.1:9473", ModeConnect, "")
	if u != "ws://127.0.0.1:9473/v1/ws?m=conn" {
		t.Fatal(u)
	}
}

// fixedReader makes ephemeral keys deterministic for test vectors.
type fixedReader struct{ b []byte }

func (f *fixedReader) Read(p []byte) (int, error) {
	n := copy(p, f.b)
	f.b = f.b[n:]
	for i := n; i < len(p); i++ {
		p[i] = 0
	}
	return len(p), nil
}

func det(seed byte) noise.DHKey {
	k, _ := noise.DH25519.GenerateKeypair(&fixedReader{b: bytes.Repeat([]byte{seed}, 32)})
	return k
}

// TestVectors checks deterministic handshakes and, with GEN_NOISE_VECTORS set,
// writes them for the Android client's unit test.
func TestVectors(t *testing.T) {
	type transport struct {
		Dir    string `json:"dir"` // i2r | r2i
		Plain  string `json:"plain"`
		Cipher string `json:"cipher"`
	}
	type vector struct {
		Name               string      `json:"name"`
		Prologue           string      `json:"prologue"`
		PSK                string      `json:"psk,omitempty"`
		InitStaticPriv     string      `json:"initStaticPriv"`
		InitEphemeralPriv  string      `json:"initEphemeralPriv"`
		RespStaticPub      string      `json:"respStaticPub"`
		RespStaticPriv     string      `json:"respStaticPriv"`
		RespEphemeralPriv  string      `json:"respEphemeralPriv"`
		Payload1, Payload2 string      `json:"-"`
		Msg1Payload        string      `json:"msg1Payload"`
		Msg1               string      `json:"msg1"`
		Msg2Payload        string      `json:"msg2Payload"`
		Msg2               string      `json:"msg2"`
		Transport          []transport `json:"transport"`
		HandshakeHash      string      `json:"handshakeHash"`
	}
	hx := hex.EncodeToString
	var out []vector
	for _, mode := range []string{ModeConnect, ModePair} {
		is, ie, rs, re := det(1), det(2), det(3), det(4)
		psk := bytes.Repeat([]byte{5}, 32)
		pro := Prologue(mode, "PAIRID")
		mk := func(init bool) *noise.HandshakeState {
			c := noise.Config{CipherSuite: Suite, Pattern: noise.HandshakeIK, Initiator: init, Prologue: pro}
			// flynn/noise draws ephemeral keys from Random (EphemeralKeypair is
			// ignored), so feed it the bytes of the fixed ephemeral private keys.
			if init {
				c.StaticKeypair, c.PeerStatic, c.Random = is, rs.Public, &fixedReader{b: append([]byte{}, ie.Private...)}
			} else {
				c.StaticKeypair, c.Random = rs, &fixedReader{b: append([]byte{}, re.Private...)}
			}
			if mode == ModePair {
				c.PresharedKey, c.PresharedKeyPlacement = psk, 1
			}
			h, err := noise.NewHandshakeState(c)
			if err != nil {
				t.Fatal(err)
			}
			return h
		}
		ini, res := mk(true), mk(false)
		p1 := []byte(`{"v":1,"purpose":"rpc","name":"Pixel"}`)
		p2 := []byte(`{"v":1,"host":"dev-box"}`)
		m1, _, _, err := ini.WriteMessage(nil, p1)
		if err != nil {
			t.Fatal(err)
		}
		got1, _, _, err := res.ReadMessage(nil, m1)
		if err != nil || !bytes.Equal(got1, p1) {
			t.Fatalf("%s msg1: %v", mode, err)
		}
		m2, rc1, rc2, err := res.WriteMessage(nil, p2)
		if err != nil {
			t.Fatal(err)
		}
		got2, ic1, ic2, err := ini.ReadMessage(nil, m2)
		if err != nil || !bytes.Equal(got2, p2) {
			t.Fatalf("%s msg2: %v", mode, err)
		}
		v := vector{Name: "Noise_IK" + map[string]string{ModePair: "psk1"}[mode] + "_25519_ChaChaPoly_SHA256", Prologue: hx(pro),
			InitStaticPriv: hx(is.Private), InitEphemeralPriv: hx(ie.Private), RespStaticPub: hx(rs.Public), RespStaticPriv: hx(rs.Private),
			RespEphemeralPriv: hx(re.Private), Msg1Payload: hx(p1), Msg1: hx(m1), Msg2Payload: hx(p2), Msg2: hx(m2), HandshakeHash: hx(ini.ChannelBinding())}
		if mode == ModePair {
			v.PSK = hx(psk)
		}
		for i, pt := range [][]byte{{1, 'h', 'i'}, {3, 0, 1, 2}, {1}} {
			ct, _ := ic1.Encrypt(nil, nil, pt) // initiator → responder
			if back, err := rc1.Decrypt(nil, nil, ct); err != nil || !bytes.Equal(back, pt) {
				t.Fatalf("i2r %d", i)
			}
			v.Transport = append(v.Transport, transport{"i2r", hx(pt), hx(ct)})
			ct2, _ := rc2.Encrypt(nil, nil, pt) // responder → initiator
			if back, err := ic2.Decrypt(nil, nil, ct2); err != nil || !bytes.Equal(back, pt) {
				t.Fatalf("r2i %d", i)
			}
			v.Transport = append(v.Transport, transport{"r2i", hx(pt), hx(ct2)})
		}
		out = append(out, v)
	}
	if p := os.Getenv("GEN_NOISE_VECTORS"); p != "" {
		b, _ := json.MarshalIndent(out, "", "  ")
		if err := os.WriteFile(p, append(b, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("wrote %s", p)
	}
	_ = base64.StdEncoding
	_ = strings.TrimSpace
}
