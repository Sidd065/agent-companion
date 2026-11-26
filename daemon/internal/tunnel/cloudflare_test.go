package tunnel

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestParseCredsContentAndDefaults(t *testing.T) {
	raw := `
# Shared credentials
export CLOUDFLARE_API_TOKEN="cfat_test_account_token_123"
CLOUDFLARE_ACCOUNT_ID=acc_987654
R2_ACCESS_KEY_ID=ignored_r2_key
R2_SECRET_ACCESS_KEY=ignored_r2_secret
`
	c := ParseCredsContent(raw)
	if c.APIToken != "cfat_test_account_token_123" || c.AccountID != "acc_987654" {
		t.Fatalf("unexpected creds: %+v", c)
	}
	if got := DefaultStaticHostname("dev_box.local", "jdlf.cc"); got != "agentc-dev-box.jdlf.cc" {
		t.Fatalf("DefaultStaticHostname = %q", got)
	}
	if got := DefaultTunnelName("rpi", "H-AbCd1234-9999"); got != "agentc-rpi-habcd123" {
		t.Fatalf("DefaultTunnelName = %q", got)
	}
}

func TestProvisionAndDeleteStaticTunnelSharedDomainSafety(t *testing.T) {
	var mu sync.Mutex
	tunnels := map[string]string{}
	ingressSaved := false
	dnsRecords := map[string]map[string]any{
		"other-app.jdlf.cc": {
			"id":      "dns-other",
			"type":    "A",
			"name":    "other-app.jdlf.cc",
			"content": "203.0.113.10",
			"proxied": true,
			"comment": "Another project on shared domain",
		},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/user/tokens/verify" {
			// Account-owned tokens (cfat_...) fail on /user/tokens/verify; ensure we never call it!
			w.WriteHeader(400)
			_ = json.NewEncoder(w).Encode(map[string]any{"success": false, "errors": []map[string]any{{"code": 1000, "message": "Invalid API Token"}}})
			return
		}
		if r.Header.Get("Authorization") != "Bearer cfat_unit_test" {
			w.WriteHeader(401)
			_ = json.NewEncoder(w).Encode(map[string]any{"success": false})
			return
		}
		mu.Lock()
		defer mu.Unlock()
		switch {
		case r.Method == "GET" && r.URL.Path == "/zones":
			if r.URL.Query().Get("name") == "jdlf.cc" {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"success": true,
					"result": []map[string]any{{
						"id": "zone_jdlf", "name": "jdlf.cc", "status": "active",
						"account": map[string]any{"id": "acc_jdlf"},
					}},
				})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "result": []any{}})
		case r.Method == "GET" && r.URL.Path == "/accounts/acc_jdlf/cfd_tunnel":
			name := r.URL.Query().Get("name")
			if id, ok := tunnels[name]; ok {
				_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "result": []map[string]any{{"id": id, "name": name}}})
			} else {
				_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "result": []any{}})
			}
		case r.Method == "POST" && r.URL.Path == "/accounts/acc_jdlf/cfd_tunnel":
			var body struct {
				Name string `json:"name"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			tunnels[body.Name] = "tun_12345"
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "result": map[string]any{"id": "tun_12345"}})
		case r.Method == "PUT" && r.URL.Path == "/accounts/acc_jdlf/cfd_tunnel/tun_12345/configurations":
			ingressSaved = true
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "result": map[string]any{}})
		case r.Method == "GET" && r.URL.Path == "/accounts/acc_jdlf/cfd_tunnel/tun_12345/token":
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "result": "connector_token_xyz"})
		case r.Method == "GET" && r.URL.Path == "/zones/zone_jdlf/dns_records":
			h := r.URL.Query().Get("name")
			if rec, ok := dnsRecords[h]; ok {
				_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "result": []any{rec}})
			} else {
				_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "result": []any{}})
			}
		case r.Method == "POST" && r.URL.Path == "/zones/zone_jdlf/dns_records":
			var rec map[string]any
			_ = json.NewDecoder(r.Body).Decode(&rec)
			rec["id"] = "dns_agentc"
			dnsRecords[rec["name"].(string)] = rec
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "result": rec})
		case r.Method == "DELETE" && strings.HasPrefix(r.URL.Path, "/zones/zone_jdlf/dns_records/"):
			id := filepath.Base(r.URL.Path)
			for k, v := range dnsRecords {
				if v["id"] == id {
					delete(dnsRecords, k)
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "result": map[string]any{"id": id}})
		case r.Method == "DELETE" && strings.HasPrefix(r.URL.Path, "/accounts/acc_jdlf/cfd_tunnel/tun_12345"):
			if r.URL.Path == "/accounts/acc_jdlf/cfd_tunnel/tun_12345" {
				tunnels = map[string]string{}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "result": map[string]any{}})
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
	}))
	defer srv.Close()

	tmp := t.TempDir()
	credsPath := filepath.Join(tmp, ".cloudflare_creds")
	_ = os.WriteFile(credsPath, []byte("CLOUDFLARE_API_TOKEN=cfat_unit_test\n"), 0o600)
	cachePath := filepath.Join(tmp, "cf-cache.json")

	// 1. Attempting to claim other-app.jdlf.cc must fail without ForceDNS!
	_, err := ProvisionStaticTunnel(context.Background(), StaticConfig{
		APIBase:   srv.URL,
		CredsFile: credsPath,
		Domain:    "jdlf.cc",
		Hostname:  "other-app.jdlf.cc",
		HostName:  "rpi",
		HostID:    "id123456",
		Target:    "http://127.0.0.1:9473",
		CacheFile: cachePath,
	})
	if err == nil || !strings.Contains(err.Error(), "not managed by agentc") {
		t.Fatalf("expected shared-domain DNS protection error, got: %v", err)
	}

	// 2. Default per-host subdomain agentc-rpi.jdlf.cc succeeds cleanly.
	pt, err := ProvisionStaticTunnel(context.Background(), StaticConfig{
		APIBase:   srv.URL,
		CredsFile: credsPath,
		Domain:    "jdlf.cc",
		HostName:  "rpi",
		HostID:    "id123456",
		Target:    "http://127.0.0.1:9473",
		CacheFile: cachePath,
	})
	if err != nil {
		t.Fatalf("ProvisionStaticTunnel failed: %v", err)
	}
	if pt.Hostname != "agentc-rpi.jdlf.cc" || pt.TunnelID != "tun_12345" || pt.Token != "connector_token_xyz" || !ingressSaved {
		t.Fatalf("unexpected provisioned tunnel: %+v (ingress=%v)", pt, ingressSaved)
	}

	// 3. DeleteStaticTunnel removes ONLY agentc-rpi.jdlf.cc and leaves other-app.jdlf.cc untouched!
	if err := DeleteStaticTunnel(context.Background(), StaticConfig{
		APIBase:   srv.URL,
		CredsFile: credsPath,
		Domain:    "jdlf.cc",
		HostName:  "rpi",
		HostID:    "id123456",
		CacheFile: cachePath,
	}); err != nil {
		t.Fatalf("DeleteStaticTunnel: %v", err)
	}
	mu.Lock()
	_, otherStillExists := dnsRecords["other-app.jdlf.cc"]
	_, agentcStillExists := dnsRecords["agentc-rpi.jdlf.cc"]
	mu.Unlock()
	if !otherStillExists || agentcStillExists {
		t.Fatalf("DNS records after delete: other=%v agentc=%v", otherStillExists, agentcStillExists)
	}
}

func TestManagerFallbackToFreeQuickTunnel(t *testing.T) {
	tmp := t.TempDir()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	metricsAddr := ln.Addr().String()
	_ = ln.Close()

	fakeBin := filepath.Join(tmp, "cloudflared")
	script := `#!/usr/bin/env python3
import http.server, json, sys
args = sys.argv[1:]
metrics = args[args.index("--metrics") + 1]
host, port = metrics.split(":")
class H(http.server.BaseHTTPRequestHandler):
    def log_message(self, *a): pass
    def do_GET(self):
        if self.path == "/quicktunnel":
            self.send_response(200)
            self.end_headers()
            self.wfile.write(json.dumps({"hostname": "fallback-quick.trycloudflare.com"}).encode())
        else:
            self.send_response(404)
            self.end_headers()
http.server.HTTPServer((host, int(port)), H).serve_forever()
`
	if err := os.WriteFile(fakeBin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	urlCh := make(chan string, 1)
	mgr := &Manager{
		Bin:         fakeBin,
		Target:      "http://127.0.0.1:9473",
		MetricsAddr: metricsAddr,
		StateFile:   filepath.Join(tmp, "tunnel.json"),
		TokenFile:   filepath.Join(tmp, "cloudflared.token"),
		LogFile:     filepath.Join(tmp, "cloudflared.log"),
		Mode:        "static",
		Static: StaticConfig{
			APIBase:   "http://127.0.0.1:1", // unreachable API -> must trigger automatic quick-tunnel fallback!
			APIToken:  "bad_token",
			Domain:    "jdlf.cc",
			HostName:  "rpi",
			Target:    "http://127.0.0.1:9473",
			CacheFile: filepath.Join(tmp, "cf-cache.json"),
		},
		OnURL: func(u string) {
			select {
			case urlCh <- u:
			default:
			}
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer func() {
		cancel()
		_ = mgr.Stop()
	}()
	go mgr.Run(ctx)

	select {
	case got := <-urlCh:
		if got != "https://fallback-quick.trycloudflare.com" {
			t.Fatalf("got URL %q", got)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for quick-tunnel fallback")
	}
	st := mgr.Status()
	if st["mode"] != "cloudflare-quick" || st["fallbackError"] == "" {
		t.Fatalf("expected cloudflare-quick with fallbackError, got: %+v", st)
	}
}
