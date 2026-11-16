package push

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSendSignsJWTAndDelivers(t *testing.T) {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	der, _ := x509.MarshalPKCS8PrivateKey(key)
	pemKey := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))

	var mu sync.Mutex
	var got []map[string]any
	var tokenCalls int
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		jwt := r.PostForm.Get("assertion")
		parts := strings.Split(jwt, ".")
		if len(parts) != 3 || r.PostForm.Get("grant_type") != "urn:ietf:params:oauth:grant-type:jwt-bearer" {
			http.Error(w, "bad grant", 400)
			return
		}
		sig, _ := base64.RawURLEncoding.DecodeString(parts[2])
		sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
		if err := rsa.VerifyPKCS1v15(&key.PublicKey, crypto.SHA256, sum[:], sig); err != nil {
			http.Error(w, "bad signature", 401)
			return
		}
		cl, _ := base64.RawURLEncoding.DecodeString(parts[1])
		var claims map[string]any
		_ = json.Unmarshal(cl, &claims)
		if claims["scope"] != "https://www.googleapis.com/auth/firebase.messaging" || claims["iss"] != "sa@test.iam.gserviceaccount.com" {
			http.Error(w, "bad claims", 401)
			return
		}
		mu.Lock()
		tokenCalls++
		mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "at-123", "expires_in": 3600, "token_type": "Bearer"})
	})
	mux.HandleFunc("/v1/projects/proj-1/messages:send", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer at-123" {
			http.Error(w, "unauthorized", 401)
			return
		}
		b, _ := io.ReadAll(r.Body)
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		msg := m["message"].(map[string]any)
		if msg["token"] == "dead-token" {
			w.WriteHeader(404)
			_, _ = w.Write([]byte(`{"error":{"status":"NOT_FOUND","details":[{"errorCode":"UNREGISTERED"}]}}`))
			return
		}
		mu.Lock()
		got = append(got, msg)
		mu.Unlock()
		_, _ = w.Write([]byte(`{"name":"projects/proj-1/messages/1"}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	dir := t.TempDir()
	sa, _ := json.Marshal(map[string]string{"type": "service_account", "project_id": "proj-1", "private_key_id": "kid1",
		"private_key": pemKey, "client_email": "sa@test.iam.gserviceaccount.com", "token_uri": srv.URL + "/token"})
	saPath := filepath.Join(dir, "sa.json")
	_ = os.WriteFile(saPath, sa, 0o644)
	p := New(saPath, filepath.Join(dir, "devices.json"))
	p.FCMBase = srv.URL
	if !p.Configured() {
		t.Fatalf("not configured: %v", p.Status())
	}
	if st, _ := os.Stat(saPath); st.Mode().Perm() != 0o600 {
		t.Fatalf("service account key should be chmod 600, got %v", st.Mode().Perm())
	}
	p.Register(Device{Token: "good-token", HostKey: "h1", Device: "Pixel"})
	p.Register(Device{Token: "dead-token", HostKey: "h1", Device: "Old phone"})
	p.Send(Message{Kind: "done", Title: "My session", Body: "All done", SessionID: "s1", Seq: 7, ID: "n7", Ts: 1})
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(got)
		mu.Unlock()
		p.mu.Lock()
		_, deadLeft := p.devices["dead-token"]
		p.mu.Unlock()
		if n == 1 && !deadLeft {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 {
		t.Fatalf("expected 1 delivered message, got %d", len(got))
	}
	data := got[0]["data"].(map[string]any)
	if data["kind"] != "done" || data["title"] != "My session" || data["sessionId"] != "s1" || data["hostKey"] != "h1" || data["seq"] != "7" {
		t.Fatalf("bad data payload: %v", data)
	}
	if got[0]["android"].(map[string]any)["priority"] != "HIGH" {
		t.Fatalf("expected high priority")
	}
	p.mu.Lock()
	_, deadLeft := p.devices["dead-token"]
	p.mu.Unlock()
	if deadLeft {
		t.Fatalf("unregistered token should have been pruned")
	}
	if tokenCalls != 1 {
		t.Fatalf("access token should be cached, got %d token calls", tokenCalls)
	}
	// minimal content mode keeps conversation text off FCM
	p.Content = "minimal"
	d := p.data(Message{Kind: "done", Title: "secret", Body: "secret"}, "h1")
	if _, ok := d["title"]; ok {
		t.Fatalf("minimal mode leaked title")
	}
	_ = url.Values{}
}

// TestLiveAccessToken exchanges a real service-account key for an FCM access
// token. Opt-in: PUSH_LIVE_SA=/path/to/key.json go test ./internal/push -run Live
func TestLiveAccessToken(t *testing.T) {
	sa := os.Getenv("PUSH_LIVE_SA")
	if sa == "" {
		t.Skip("set PUSH_LIVE_SA to run")
	}
	p := New(sa, filepath.Join(t.TempDir(), "devices.json"))
	tok, project, err := p.accessToken(context.Background())
	if err != nil {
		t.Fatalf("access token: %v", err)
	}
	if len(tok) < 20 || project == "" {
		t.Fatalf("unexpected token/project: %d chars, %q", len(tok), project)
	}
	t.Logf("got access token (%d chars) for project %s", len(tok), project)
}
