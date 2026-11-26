package tunnel

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const DefaultCloudflareAPIBase = "https://api.cloudflare.com/client/v4"

// Creds holds Cloudflare credentials parsed from ~/.cloudflare_creds, env, or config.
type Creds struct {
	APIToken    string
	AccountID   string
	ZoneID      string
	Domain      string
	TunnelToken string
}

// StaticConfig describes how to provision or run a static Cloudflare Named Tunnel.
type StaticConfig struct {
	APIBase     string // default https://api.cloudflare.com/client/v4
	CredsFile   string // optional path e.g. ~/.cloudflare_creds
	APIToken    string // Cloudflare API token (Zone:Read, DNS:Edit, Cloudflare Tunnel:Edit)
	AccountID   string // optional if discoverable from zone
	Domain      string // e.g. jdlf.cc
	Hostname    string // optional override e.g. agentc-rpi.jdlf.cc
	TunnelName  string // optional override e.g. agentc-rpi-a1b2c3d4
	TunnelToken string // optional pre-created connector token
	HostName    string // machine hostname (os.Hostname)
	HostID      string // daemon identity ID (used to disambiguate tunnel name)
	Target      string // local origin e.g. http://127.0.0.1:9473
	CacheFile   string // 0600 cache of provisioned tunnel metadata & connector token
	ForceDNS    bool   // allow overwriting non-agentc DNS records on Hostname
	NoFallback  bool   // do not fall back to free quick tunnel if static tunnel fails
	HTTPClient  *http.Client
}

// ProvisionedTunnel is persisted in CacheFile (0600) after API provisioning.
type ProvisionedTunnel struct {
	TunnelID   string `json:"tunnelId,omitempty"`
	TunnelName string `json:"tunnelName,omitempty"`
	AccountID  string `json:"accountId,omitempty"`
	ZoneID     string `json:"zoneId,omitempty"`
	ZoneName   string `json:"zoneName,omitempty"`
	Hostname   string `json:"hostname"`
	Target     string `json:"target"`
	Token      string `json:"token"`
	UpdatedAt  int64  `json:"updatedAt"`
}

// ParseCredsContent parses shell-style KEY=VALUE / export KEY="VALUE" lines.
func ParseCredsContent(content string) Creds {
	var c Creds
	for _, raw := range strings.Split(content, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		eq := strings.IndexByte(line, '=')
		if eq <= 0 {
			continue
		}
		k := strings.ToUpper(strings.TrimSpace(line[:eq]))
		v := strings.TrimSpace(line[eq+1:])
		if len(v) >= 2 && ((v[0] == '\'' && v[len(v)-1] == '\'') || (v[0] == '"' && v[len(v)-1] == '"')) {
			v = v[1 : len(v)-1]
		} else if idx := strings.Index(v, " #"); idx >= 0 {
			v = strings.TrimSpace(v[:idx])
		}
		switch k {
		case "CLOUDFLARE_API_TOKEN", "CF_API_TOKEN":
			c.APIToken = v
		case "CLOUDFLARE_ACCOUNT_ID", "CF_ACCOUNT_ID":
			c.AccountID = v
		case "CLOUDFLARE_ZONE_ID", "CF_ZONE_ID":
			c.ZoneID = v
		case "CLOUDFLARE_DOMAIN", "CF_DOMAIN":
			c.Domain = v
		case "CLOUDFLARE_TUNNEL_TOKEN", "CF_TUNNEL_TOKEN":
			c.TunnelToken = v
		}
	}
	return c
}

// ParseCredsFile reads a Cloudflare credentials file.
func ParseCredsFile(path string) (Creds, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Creds{}, err
	}
	return ParseCredsContent(string(b)), nil
}

// SanitizeDNSLabel turns a hostname or identifier into a valid lowercase DNS label.
func SanitizeDNSLabel(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if idx := strings.IndexByte(s, '.'); idx > 0 {
		s = s[:idx]
	}
	var b strings.Builder
	prevDash := false
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			prevDash = false
		} else if !prevDash && b.Len() > 0 {
			b.WriteByte('-')
			prevDash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > 40 {
		out = strings.Trim(out[:40], "-")
	}
	if out == "" {
		return "host"
	}
	return out
}

// DefaultStaticHostname builds a single-level subdomain `agentc-<host>.<domain>`
// so Cloudflare Free Universal SSL (`*.<domain>`) works out of the box without
// claiming the apex domain or other subdomains on `<domain>`.
func DefaultStaticHostname(hostName, domain string) string {
	domain = strings.Trim(strings.ToLower(strings.TrimSpace(domain)), ".")
	if domain == "" {
		return ""
	}
	return "agentc-" + SanitizeDNSLabel(hostName) + "." + domain
}

// DefaultTunnelName builds a deterministic per-host tunnel name so multiple hosts
// sharing one Cloudflare account never overwrite each other's tunnels.
func DefaultTunnelName(hostName, hostID string) string {
	lbl := SanitizeDNSLabel(hostName)
	var short strings.Builder
	for _, r := range strings.ToLower(hostID) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			short.WriteRune(r)
			if short.Len() >= 8 {
				break
			}
		}
	}
	if short.Len() == 0 {
		return "agentc-" + lbl
	}
	return "agentc-" + lbl + "-" + short.String()
}

// Resolve merges explicit StaticConfig fields, CredsFile, and environment variables.
func (sc StaticConfig) Resolve() (StaticConfig, error) {
	out := sc
	if out.APIBase == "" {
		out.APIBase = DefaultCloudflareAPIBase
	}
	out.APIBase = strings.TrimRight(out.APIBase, "/")
	if out.CredsFile != "" {
		fc, err := ParseCredsFile(out.CredsFile)
		if err != nil {
			return out, fmt.Errorf("read cloudflare creds %s: %w", out.CredsFile, err)
		}
		if out.APIToken == "" {
			out.APIToken = fc.APIToken
		}
		if out.AccountID == "" {
			out.AccountID = fc.AccountID
		}
		if out.Domain == "" {
			out.Domain = fc.Domain
		}
		if out.TunnelToken == "" {
			out.TunnelToken = fc.TunnelToken
		}
	}
	if out.APIToken == "" {
		out.APIToken = strings.TrimSpace(os.Getenv("CLOUDFLARE_API_TOKEN"))
	}
	if out.AccountID == "" {
		out.AccountID = strings.TrimSpace(os.Getenv("CLOUDFLARE_ACCOUNT_ID"))
	}
	if out.Domain == "" {
		out.Domain = strings.TrimSpace(os.Getenv("CLOUDFLARE_DOMAIN"))
	}
	if out.TunnelToken == "" {
		out.TunnelToken = strings.TrimSpace(os.Getenv("CLOUDFLARE_TUNNEL_TOKEN"))
	}
	out.Domain = strings.Trim(strings.ToLower(strings.TrimSpace(out.Domain)), ".")
	out.Hostname = strings.Trim(strings.ToLower(strings.TrimSpace(out.Hostname)), ".")
	if h := strings.TrimPrefix(strings.TrimPrefix(out.Hostname, "https://"), "http://"); h != out.Hostname {
		if idx := strings.IndexByte(h, '/'); idx >= 0 {
			h = h[:idx]
		}
		out.Hostname = strings.Trim(h, ".")
	}
	if out.HostName == "" {
		out.HostName, _ = os.Hostname()
	}
	if out.Hostname == "" && out.Domain != "" {
		out.Hostname = DefaultStaticHostname(out.HostName, out.Domain)
	}
	if out.TunnelName == "" {
		out.TunnelName = DefaultTunnelName(out.HostName, out.HostID)
	}
	return out, nil
}

// Configured reports whether static tunnel credentials are present.
func (sc StaticConfig) Configured() bool {
	r, err := sc.Resolve()
	if err != nil {
		return sc.CredsFile != "" || sc.APIToken != "" || sc.TunnelToken != ""
	}
	if r.TunnelToken != "" && r.Hostname != "" {
		return true
	}
	return r.APIToken != "" && r.Hostname != ""
}

func decodeConnectorTokenID(tok string) (accountID, tunnelID string) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(tok))
	if err != nil {
		raw, err = base64.RawStdEncoding.DecodeString(strings.TrimSpace(tok))
		if err != nil {
			return "", ""
		}
	}
	var payload struct {
		A string `json:"a"`
		T string `json:"t"`
	}
	if json.Unmarshal(raw, &payload) != nil {
		return "", ""
	}
	return payload.A, payload.T
}

type cfEnvelope struct {
	Success bool            `json:"success"`
	Result  json.RawMessage `json:"result"`
	Errors  []struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"errors"`
}

func (sc StaticConfig) cfDo(ctx context.Context, method, path string, body any, out any) error {
	hc := sc.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: 25 * time.Second}
	}
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, sc.APIBase+path, rdr)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+sc.APIToken)
	req.Header.Set("Content-Type", "application/json")
	res, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	respBytes, _ := io.ReadAll(io.LimitReader(res.Body, 2<<20))
	var env cfEnvelope
	if err := json.Unmarshal(respBytes, &env); err != nil {
		return fmt.Errorf("cloudflare %s %s: HTTP %d: invalid JSON", method, path, res.StatusCode)
	}
	if !env.Success || res.StatusCode >= 400 {
		if len(env.Errors) > 0 {
			var msgs []string
			for _, e := range env.Errors {
				msgs = append(msgs, fmt.Sprintf("[%d] %s", e.Code, e.Message))
			}
			return fmt.Errorf("cloudflare %s %s: %s", method, path, strings.Join(msgs, "; "))
		}
		return fmt.Errorf("cloudflare %s %s: HTTP %d", method, path, res.StatusCode)
	}
	if out != nil && len(env.Result) > 0 {
		if err := json.Unmarshal(env.Result, out); err != nil {
			return fmt.Errorf("cloudflare %s %s decode: %w", method, path, err)
		}
	}
	return nil
}

type cfZone struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Status  string `json:"status"`
	Account struct {
		ID string `json:"id"`
	} `json:"account"`
}

func (sc StaticConfig) findZone(ctx context.Context, hostname string) (*cfZone, error) {
	labels := strings.Split(strings.Trim(hostname, "."), ".")
	if len(labels) < 2 {
		return nil, fmt.Errorf("hostname %q must be a fully-qualified domain (e.g. agentc-host.example.com)", hostname)
	}
	for i := 1; i <= len(labels)-2; i++ {
		cand := strings.Join(labels[i:], ".")
		var zones []cfZone
		q := "/zones?name=" + url.QueryEscape(cand) + "&status=active"
		if err := sc.cfDo(ctx, http.MethodGet, q, nil, &zones); err != nil {
			return nil, err
		}
		if len(zones) > 0 {
			return &zones[0], nil
		}
	}
	var zones []cfZone
	if err := sc.cfDo(ctx, http.MethodGet, "/zones?name="+url.QueryEscape(hostname)+"&status=active", nil, &zones); err == nil && len(zones) > 0 {
		return &zones[0], nil
	}
	return nil, fmt.Errorf("no active Cloudflare zone found for %q (check domain & Zone:Read permission on API token)", hostname)
}

// ProvisionStaticTunnel ensures the named tunnel, its ingress rule, and its scoped CNAME record exist,
// returning the connector token needed by `cloudflared tunnel run --token-file`.
func ProvisionStaticTunnel(ctx context.Context, cfg StaticConfig) (*ProvisionedTunnel, error) {
	sc, err := cfg.Resolve()
	if err != nil {
		return nil, err
	}
	if sc.Target == "" {
		return nil, errors.New("missing local target URL for static tunnel")
	}
	if sc.Hostname == "" {
		return nil, errors.New("missing static hostname or Cloudflare domain (set --domain or --hostname)")
	}

	if sc.TunnelToken != "" && sc.APIToken == "" {
		accID, tunID := decodeConnectorTokenID(sc.TunnelToken)
		pt := &ProvisionedTunnel{
			TunnelID:   tunID,
			TunnelName: sc.TunnelName,
			AccountID:  firstNonEmptyStr(sc.AccountID, accID),
			Hostname:   sc.Hostname,
			Target:     sc.Target,
			Token:      strings.TrimSpace(sc.TunnelToken),
			UpdatedAt:  time.Now().UnixMilli(),
		}
		saveProvisionCache(sc.CacheFile, pt)
		return pt, nil
	}

	if sc.APIToken == "" {
		return nil, errors.New("missing Cloudflare API token (pass --creds-file, --api-token, or --tunnel-token)")
	}

	zone, err := sc.findZone(ctx, sc.Hostname)
	if err != nil {
		if cached := loadProvisionCache(sc.CacheFile, sc.Hostname, sc.Target); cached != nil {
			return cached, nil
		}
		return nil, err
	}
	accountID := firstNonEmptyStr(sc.AccountID, zone.Account.ID)
	if accountID == "" {
		return nil, errors.New("could not determine Cloudflare account ID; set CLOUDFLARE_ACCOUNT_ID or --account-id")
	}

	var existing []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	listPath := fmt.Sprintf("/accounts/%s/cfd_tunnel?name=%s&is_deleted=false", url.PathEscape(accountID), url.QueryEscape(sc.TunnelName))
	if err := sc.cfDo(ctx, http.MethodGet, listPath, nil, &existing); err != nil {
		if cached := loadProvisionCache(sc.CacheFile, sc.Hostname, sc.Target); cached != nil {
			return cached, nil
		}
		return nil, err
	}
	tunnelID := ""
	if len(existing) > 0 {
		tunnelID = existing[0].ID
	} else {
		var created struct {
			ID    string `json:"id"`
			Token string `json:"token"`
		}
		createPath := fmt.Sprintf("/accounts/%s/cfd_tunnel", url.PathEscape(accountID))
		if err := sc.cfDo(ctx, http.MethodPost, createPath, map[string]any{
			"name":       sc.TunnelName,
			"config_src": "cloudflare",
		}, &created); err != nil {
			return nil, err
		}
		tunnelID = created.ID
	}
	if tunnelID == "" {
		return nil, errors.New("cloudflare returned empty tunnel ID")
	}

	cfgPath := fmt.Sprintf("/accounts/%s/cfd_tunnel/%s/configurations", url.PathEscape(accountID), url.PathEscape(tunnelID))
	ingressBody := map[string]any{
		"config": map[string]any{
			"ingress": []map[string]any{
				{"hostname": sc.Hostname, "service": sc.Target},
				{"service": "http_status:404"},
			},
		},
	}
	if err := sc.cfDo(ctx, http.MethodPut, cfgPath, ingressBody, nil); err != nil {
		return nil, err
	}

	cnameTarget := tunnelID + ".cfargotunnel.com"
	comment := "Managed by agentc (" + sc.TunnelName + ")"
	type dnsRecord struct {
		ID      string `json:"id"`
		Type    string `json:"type"`
		Name    string `json:"name"`
		Content string `json:"content"`
		Proxied bool   `json:"proxied"`
		Comment string `json:"comment"`
	}
	var records []dnsRecord
	dnsListPath := fmt.Sprintf("/zones/%s/dns_records?name=%s", url.PathEscape(zone.ID), url.QueryEscape(sc.Hostname))
	if err := sc.cfDo(ctx, http.MethodGet, dnsListPath, nil, &records); err != nil {
		return nil, err
	}
	if len(records) == 0 {
		dnsCreatePath := fmt.Sprintf("/zones/%s/dns_records", url.PathEscape(zone.ID))
		if err := sc.cfDo(ctx, http.MethodPost, dnsCreatePath, map[string]any{
			"type":    "CNAME",
			"name":    sc.Hostname,
			"content": cnameTarget,
			"proxied": true,
			"comment": comment,
		}, nil); err != nil {
			return nil, err
		}
	} else {
		rec := records[0]
		oursDNS := strings.EqualFold(rec.Content, cnameTarget) || strings.Contains(strings.ToLower(rec.Comment), "agentc")
		if !oursDNS && !sc.ForceDNS {
			return nil, fmt.Errorf("DNS record %s (%s -> %s) already exists on zone %s and is not managed by agentc; pick a different --hostname or pass --force-dns",
				sc.Hostname, rec.Type, rec.Content, zone.Name)
		}
		if rec.Type != "CNAME" || !strings.EqualFold(rec.Content, cnameTarget) || !rec.Proxied {
			dnsPutPath := fmt.Sprintf("/zones/%s/dns_records/%s", url.PathEscape(zone.ID), url.PathEscape(rec.ID))
			if err := sc.cfDo(ctx, http.MethodPut, dnsPutPath, map[string]any{
				"type":    "CNAME",
				"name":    sc.Hostname,
				"content": cnameTarget,
				"proxied": true,
				"comment": comment,
			}, nil); err != nil {
				return nil, err
			}
		}
	}

	var token string
	tokPath := fmt.Sprintf("/accounts/%s/cfd_tunnel/%s/token", url.PathEscape(accountID), url.PathEscape(tunnelID))
	if err := sc.cfDo(ctx, http.MethodGet, tokPath, nil, &token); err != nil {
		return nil, err
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, errors.New("cloudflare returned empty tunnel connector token")
	}

	pt := &ProvisionedTunnel{
		TunnelID:   tunnelID,
		TunnelName: sc.TunnelName,
		AccountID:  accountID,
		ZoneID:     zone.ID,
		ZoneName:   zone.Name,
		Hostname:   sc.Hostname,
		Target:     sc.Target,
		Token:      token,
		UpdatedAt:  time.Now().UnixMilli(),
	}
	saveProvisionCache(sc.CacheFile, pt)
	return pt, nil
}

// DeleteStaticTunnel removes only agentc's own DNS CNAME record and named tunnel from Cloudflare.
func DeleteStaticTunnel(ctx context.Context, cfg StaticConfig) error {
	sc, err := cfg.Resolve()
	if err != nil {
		return err
	}
	cached := loadProvisionCache(sc.CacheFile, "", "")
	if sc.CacheFile != "" {
		_ = os.Remove(sc.CacheFile)
	}
	if sc.APIToken == "" {
		return nil
	}
	hostname := sc.Hostname
	if hostname == "" && cached != nil {
		hostname = cached.Hostname
	}
	if hostname == "" {
		return nil
	}
	zone, err := sc.findZone(ctx, hostname)
	if err != nil {
		return err
	}
	accountID := firstNonEmptyStr(sc.AccountID, zone.Account.ID)
	tunnelID := ""
	if cached != nil {
		tunnelID = cached.TunnelID
	}
	if tunnelID == "" && accountID != "" {
		var existing []struct {
			ID string `json:"id"`
		}
		listPath := fmt.Sprintf("/accounts/%s/cfd_tunnel?name=%s&is_deleted=false", url.PathEscape(accountID), url.QueryEscape(sc.TunnelName))
		if sc.cfDo(ctx, http.MethodGet, listPath, nil, &existing) == nil && len(existing) > 0 {
			tunnelID = existing[0].ID
		}
	}
	var records []struct {
		ID      string `json:"id"`
		Type    string `json:"type"`
		Content string `json:"content"`
		Comment string `json:"comment"`
	}
	dnsListPath := fmt.Sprintf("/zones/%s/dns_records?name=%s", url.PathEscape(zone.ID), url.QueryEscape(hostname))
	if sc.cfDo(ctx, http.MethodGet, dnsListPath, nil, &records) == nil {
		for _, rec := range records {
			if (tunnelID != "" && strings.EqualFold(rec.Content, tunnelID+".cfargotunnel.com")) ||
				strings.Contains(strings.ToLower(rec.Comment), "agentc") {
				_ = sc.cfDo(ctx, http.MethodDelete, fmt.Sprintf("/zones/%s/dns_records/%s", url.PathEscape(zone.ID), url.PathEscape(rec.ID)), nil, nil)
			}
		}
	}
	if tunnelID != "" && accountID != "" {
		_ = sc.cfDo(ctx, http.MethodDelete, fmt.Sprintf("/accounts/%s/cfd_tunnel/%s/connections", url.PathEscape(accountID), url.PathEscape(tunnelID)), nil, nil)
		if err := sc.cfDo(ctx, http.MethodDelete, fmt.Sprintf("/accounts/%s/cfd_tunnel/%s", url.PathEscape(accountID), url.PathEscape(tunnelID)), nil, nil); err != nil {
			return err
		}
	}
	return nil
}

func saveProvisionCache(path string, pt *ProvisionedTunnel) {
	if path == "" || pt == nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(path), 0o700)
	b, err := json.MarshalIndent(pt, "", "  ")
	if err != nil {
		return
	}
	tmp := path + ".tmp"
	if os.WriteFile(tmp, b, 0o600) == nil {
		_ = os.Rename(tmp, path)
	}
}

func loadProvisionCache(path, wantHost, wantTarget string) *ProvisionedTunnel {
	if path == "" {
		return nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var pt ProvisionedTunnel
	if json.Unmarshal(b, &pt) != nil || pt.Token == "" || pt.Hostname == "" {
		return nil
	}
	if wantHost != "" && !strings.EqualFold(pt.Hostname, wantHost) {
		return nil
	}
	if wantTarget != "" && pt.Target != "" && pt.Target != wantTarget {
		return nil
	}
	return &pt
}

func firstNonEmptyStr(v ...string) string {
	for _, s := range v {
		if strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
	}
	return ""
}
