package server

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/flynn/noise"

	"agentc/internal/core"
	"agentc/internal/secure"
)

func TestFileDownloadAndUploadOverNoiseWS(t *testing.T) {
	tmp := t.TempDir()
	id, err := secure.LoadOrCreateIdentity(filepath.Join(tmp, "identity.json"))
	if err != nil {
		t.Fatalf("identity: %v", err)
	}
	devs, err := secure.OpenRegistry(filepath.Join(tmp, "devices.json"))
	if err != nil {
		t.Fatalf("devices: %v", err)
	}
	hub := core.NewHub()
	mgr := core.NewManager(core.Config{
		StateDir: tmp, ConfigDir: tmp, WorktreeDir: filepath.Join(tmp, "wt"), HomeDir: tmp,
	}, hub)
	defer mgr.Shutdown()

	srv := &Server{
		Mgr: mgr, Identity: id, Devices: devs, Tokens: secure.NewTokens(), Started: time.Now(),
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// Pair a client over Noise-IKpsk1
	tok, err := srv.Tokens.Create(2 * time.Minute)
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	devKey, err := noise.DH25519.GenerateKeypair(rand.Reader)
	if err != nil {
		t.Fatalf("devKey: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	pairConn, err := secure.Dial(ctx, secure.ClientConfig{
		URL: ts.URL, DaemonKey: id.Key.Public, DeviceKey: devKey,
		Mode: secure.ModePair, PairID: tok.ID, PSK: tok.PSK,
		Hello: secure.Hello{V: 1, Purpose: "rpc", Name: "FileTransferTest", Platform: "android"},
	})
	if err != nil {
		t.Fatalf("pair dial: %v", err)
	}
	pairConn.Close()

	conn, err := secure.Dial(ctx, secure.ClientConfig{
		URL: ts.URL, DaemonKey: id.Key.Public, DeviceKey: devKey,
		Mode:  secure.ModeConnect,
		Hello: secure.Hello{V: 1, Purpose: "rpc", Name: "FileTransferTest", Platform: "android"},
	})
	if err != nil {
		t.Fatalf("connect dial: %v", err)
	}
	defer conn.Close()

	call := func(method string, params any) (json.RawMessage, error) {
		req, _ := json.Marshal(map[string]any{"id": 1, "method": method, "params": params})
		if err := conn.WriteMessage(secure.TextMessage, req); err != nil {
			return nil, err
		}
		for {
			_, raw, err := conn.ReadMessage()
			if err != nil {
				return nil, err
			}
			var resp struct {
				ID     int64           `json:"id"`
				Result json.RawMessage `json:"result"`
				Error  *struct {
					Message string `json:"message"`
				} `json:"error"`
			}
			_ = json.Unmarshal(raw, &resp)
			if resp.ID != 1 {
				continue // skip broadcast event
			}
			if resp.Error != nil {
				return nil, &rpcErr{msg: resp.Error.Message}
			}
			return resp.Result, nil
		}
	}

	// 1. Large multi-chunk binary file (2.7 MiB) download verified by SHA-256
	bigBytes := make([]byte, 2*1024*1024+713*1024)
	if _, err := rand.Read(bigBytes); err != nil {
		t.Fatalf("rand: %v", err)
	}
	bigPath := filepath.Join(tmp, "release-artifact.deb")
	if err := os.WriteFile(bigPath, bigBytes, 0o644); err != nil {
		t.Fatalf("write big: %v", err)
	}
	wantHash := sha256.Sum256(bigBytes)

	var downloaded bytes.Buffer
	var off int64
	chunks := 0
	for {
		raw, err := call("fs.download", map[string]any{"path": bigPath, "offset": off, "length": 512 * 1024})
		if err != nil {
			t.Fatalf("fs.download at %d: %v", off, err)
		}
		var r struct {
			Data string `json:"data"`
			Size int64  `json:"size"`
			EOF  bool   `json:"eof"`
		}
		if err := json.Unmarshal(raw, &r); err != nil {
			t.Fatalf("decode chunk: %v", err)
		}
		b, err := base64.StdEncoding.DecodeString(r.Data)
		if err != nil {
			t.Fatalf("b64 decode: %v", err)
		}
		downloaded.Write(b)
		off += int64(len(b))
		chunks++
		if r.Size != int64(len(bigBytes)) {
			t.Fatalf("expected size %d, got %d", len(bigBytes), r.Size)
		}
		if r.EOF || len(b) == 0 {
			break
		}
	}
	if chunks < 5 {
		t.Fatalf("expected multi-chunk transfer (>=5 chunks), got %d", chunks)
	}
	gotHash := sha256.Sum256(downloaded.Bytes())
	if gotHash != wantHash {
		t.Fatalf("sha256 mismatch: want %s, got %s", hex.EncodeToString(wantHash[:]), hex.EncodeToString(gotHash[:]))
	}

	// 2. Empty (0-byte) file download
	emptyPath := filepath.Join(tmp, "empty.txt")
	if err := os.WriteFile(emptyPath, nil, 0o644); err != nil {
		t.Fatalf("write empty: %v", err)
	}
	raw, err := call("fs.download", map[string]any{"path": emptyPath, "offset": 0, "length": 512 * 1024})
	if err != nil {
		t.Fatalf("empty download: %v", err)
	}
	var er struct {
		Data string `json:"data"`
		Size int64  `json:"size"`
		EOF  bool   `json:"eof"`
	}
	_ = json.Unmarshal(raw, &er)
	if !er.EOF || er.Size != 0 || er.Data != "" {
		t.Fatalf("unexpected empty result: %+v", er)
	}

	// 3. Directory rejection
	if _, err := call("fs.download", map[string]any{"path": tmp, "offset": 0, "length": 1024}); err == nil {
		t.Fatalf("expected directory download to fail")
	}

	// 4. Out-of-range offset rejection
	if _, err := call("fs.download", map[string]any{"path": emptyPath, "offset": 10, "length": 1024}); err == nil {
		t.Fatalf("expected out-of-range offset to fail")
	}

	// 5. Upload round-trip
	upPath := filepath.Join(tmp, "sub", "uploaded.bin")
	startRaw, err := call("fs.uploadStart", map[string]any{"path": upPath, "overwrite": false, "mkdirs": true})
	if err != nil {
		t.Fatalf("uploadStart: %v", err)
	}
	var st struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(startRaw, &st)
	payload := []byte("hello-encrypted-upload-payload")
	if _, err := call("fs.uploadChunk", map[string]any{
		"id": st.ID, "offset": 0, "data": base64.StdEncoding.EncodeToString(payload),
	}); err != nil {
		t.Fatalf("uploadChunk: %v", err)
	}
	if _, err := call("fs.uploadFinish", map[string]any{"id": st.ID}); err != nil {
		t.Fatalf("uploadFinish: %v", err)
	}
	gotUp, err := os.ReadFile(upPath)
	if err != nil || !bytes.Equal(gotUp, payload) {
		t.Fatalf("uploaded content mismatch: err=%v got=%q", err, string(gotUp))
	}
}

type rpcErr struct{ msg string }

func (e *rpcErr) Error() string { return e.msg }
