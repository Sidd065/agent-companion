// Package push delivers notifications to the companion app through Firebase
// Cloud Messaging (HTTP v1). It authenticates with a Firebase service account
// key (JWT bearer grant, signed locally with the standard library).
//
// Devices register their FCM token with the daemon over Tailscale. Messages
// are data-only so the app renders them (with Approve/Deny/Reply actions).
package push

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

// ServiceAccount is a Google service account key file.
type ServiceAccount struct {
	Type         string `json:"type"`
	ProjectID    string `json:"project_id"`
	PrivateKeyID string `json:"private_key_id"`
	PrivateKey   string `json:"private_key"`
	ClientEmail  string `json:"client_email"`
	TokenURI     string `json:"token_uri"`
}

// DefaultEvents are the notification categories enabled when a client has not
// customized its filter: input required, task done, and halted/error (no minor progress spam).
var DefaultEvents = []string{"input", "done", "halted"}

// Category maps a wire notification kind to one of the 4 user-configurable
// categories ("input", "done", "halted", "progress"), or "" for control/test events.
func Category(kind string) string {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "permission", "question", "plan", "term_blocked":
		return "input"
	case "done", "term_done", "aside":
		return "done"
	case "error", "term_failed", "schedule_error", "halted":
		return "halted"
	case "progress", "schedule_started":
		return "progress"
	default:
		return ""
	}
}

// NormalizeEvents canonicalizes a list of notification category names.
// An explicit empty list becomes ["none"] so JSON persistence distinguishes
// "mute all categories" from "unset (use DefaultEvents)".
func NormalizeEvents(in []string) []string {
	if in == nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	add := func(c string) {
		if !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}
	for _, raw := range in {
		for _, part := range strings.Split(raw, ",") {
			switch strings.ToLower(strings.TrimSpace(part)) {
			case "":
				continue
			case "all", "*":
				return []string{"input", "done", "halted", "progress"}
			case "none", "off":
				return []string{"none"}
			case "input", "waiting", "approval":
				add("input")
			case "done", "completed", "complete":
				add("done")
			case "halted", "error", "failed", "halt":
				add("halted")
			case "progress", "updates", "minor":
				add("progress")
			}
		}
	}
	if len(out) == 0 {
		return []string{"none"}
	}
	return out
}

// AllowsEvents reports whether kind passes the given normalized category filter.
// When events is empty/nil, DefaultEvents ("input", "done", "halted") applies.
func AllowsEvents(events []string, kind string) bool {
	cat := Category(kind)
	if cat == "" {
		return true
	}
	evs := events
	if len(evs) == 0 {
		evs = DefaultEvents
	}
	for _, e := range evs {
		if e == "all" || e == cat {
			return true
		}
		if e == "none" {
			return false
		}
	}
	return false
}

// Device is a registered app install.
type Device struct {
	Token      string   `json:"token"`
	DeviceID   string   `json:"deviceId,omitempty"` // paired device (secure.Device.ID)
	HostKey    string   `json:"hostKey"`
	Device     string   `json:"device"`
	Login      string   `json:"login"`
	Node       string   `json:"node"`
	Events     []string `json:"events,omitempty"`
	Registered int64    `json:"registered"`
	LastOK     int64    `json:"lastOk,omitempty"`
	LastErr    string   `json:"lastErr,omitempty"`
}

// Allows reports whether this device wants push notifications of kind.
func (d Device) Allows(kind string) bool {
	return AllowsEvents(d.Events, kind)
}

// Message is what gets delivered (all values become strings).
type Message struct {
	Kind      string
	Title     string
	Body      string
	SessionID string
	TermID    string
	ReqID     string
	Options   any
	Seq       int64
	ID        string
	Ts        int64
	URL       string // host_url: the daemon's new public URL
}

// Pusher sends FCM messages to registered devices.
type Pusher struct {
	SAPath    string
	StatePath string
	Content   string // "full" (title + excerpt) or "minimal" (no conversation text goes through Firebase)
	mu        sync.Mutex
	tokMu     sync.Mutex // serializes OAuth token fetches
	sa        *ServiceAccount
	key       *rsa.PrivateKey
	saMod     time.Time
	saErr     error
	token     string
	tokenExp  time.Time
	devices   map[string]*Device
	http      *http.Client
	FCMBase   string // https://fcm.googleapis.com (overridable for tests)
	lastErr   string
	lastSent  int64
	sent      int64
}

// New loads registered devices.
func New(saPath, statePath string) *Pusher {
	p := &Pusher{SAPath: saPath, StatePath: statePath, Content: "full", devices: map[string]*Device{},
		http: &http.Client{Timeout: 20 * time.Second}, FCMBase: "https://fcm.googleapis.com"}
	if b, err := os.ReadFile(statePath); err == nil {
		var list []*Device
		if json.Unmarshal(b, &list) == nil {
			for _, d := range list {
				p.devices[d.Token] = d
			}
		}
	}
	return p
}

func (p *Pusher) saveLocked() {
	list := make([]*Device, 0, len(p.devices))
	for _, d := range p.devices {
		list = append(list, d)
	}
	b, _ := json.MarshalIndent(list, "", "  ")
	tmp := p.StatePath + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err == nil {
		_ = os.Rename(tmp, p.StatePath)
	}
}

// loadSALocked (re)reads the service account when the file appears or changes.
func (p *Pusher) loadSALocked() error {
	st, err := os.Stat(p.SAPath)
	if err != nil {
		p.sa, p.key, p.saErr = nil, nil, fmt.Errorf("no Firebase service account key at %s", p.SAPath)
		return p.saErr
	}
	if p.sa != nil && st.ModTime().Equal(p.saMod) {
		return nil
	}
	if st.Mode().Perm()&0o077 != 0 {
		_ = os.Chmod(p.SAPath, 0o600)
	}
	b, err := os.ReadFile(p.SAPath)
	if err != nil {
		p.saErr = err
		return err
	}
	var sa ServiceAccount
	if err := json.Unmarshal(b, &sa); err != nil {
		p.saErr = fmt.Errorf("parse service account: %w", err)
		return p.saErr
	}
	if sa.Type != "service_account" || sa.PrivateKey == "" || sa.ClientEmail == "" || sa.ProjectID == "" {
		p.saErr = errors.New("service account file is missing type/private_key/client_email/project_id")
		return p.saErr
	}
	block, _ := pem.Decode([]byte(sa.PrivateKey))
	if block == nil {
		p.saErr = errors.New("service account private_key is not PEM")
		return p.saErr
	}
	var key *rsa.PrivateKey
	if k, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		rk, ok := k.(*rsa.PrivateKey)
		if !ok {
			p.saErr = errors.New("service account key is not RSA")
			return p.saErr
		}
		key = rk
	} else if rk, err2 := x509.ParsePKCS1PrivateKey(block.Bytes); err2 == nil {
		key = rk
	} else {
		p.saErr = fmt.Errorf("parse private key: %w", err)
		return p.saErr
	}
	if sa.TokenURI == "" {
		sa.TokenURI = "https://oauth2.googleapis.com/token"
	}
	p.sa, p.key, p.saMod, p.saErr = &sa, key, st.ModTime(), nil
	p.token = ""
	log.Printf("push: using Firebase project %s as %s", sa.ProjectID, sa.ClientEmail)
	return nil
}

func b64url(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func (p *Pusher) accessToken(ctx context.Context) (string, string, error) {
	p.tokMu.Lock()
	defer p.tokMu.Unlock()
	p.mu.Lock()
	if err := p.loadSALocked(); err != nil {
		p.mu.Unlock()
		return "", "", err
	}
	if p.token != "" && time.Now().Before(p.tokenExp) {
		t, proj := p.token, p.sa.ProjectID
		p.mu.Unlock()
		return t, proj, nil
	}
	sa, key := *p.sa, p.key
	p.mu.Unlock()

	now := time.Now()
	hdr, _ := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT", "kid": sa.PrivateKeyID})
	claims, _ := json.Marshal(map[string]any{
		"iss": sa.ClientEmail, "scope": "https://www.googleapis.com/auth/firebase.messaging",
		"aud": sa.TokenURI, "iat": now.Unix(), "exp": now.Add(time.Hour).Unix(),
	})
	input := b64url(hdr) + "." + b64url(claims)
	sum := sha256.Sum256([]byte(input))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		return "", "", err
	}
	form := url.Values{"grant_type": {"urn:ietf:params:oauth:grant-type:jwt-bearer"}, "assertion": {input + "." + b64url(sig)}}
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, sa.TokenURI, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := p.http.Do(req)
	if err != nil {
		return "", "", err
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode != 200 {
		return "", "", fmt.Errorf("oauth token: HTTP %d: %s", res.StatusCode, strings.TrimSpace(string(body)))
	}
	var tr struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &tr); err != nil || tr.AccessToken == "" {
		return "", "", fmt.Errorf("oauth token: bad response")
	}
	p.mu.Lock()
	p.token = tr.AccessToken
	p.tokenExp = now.Add(time.Duration(tr.ExpiresIn-120) * time.Second)
	p.mu.Unlock()
	return tr.AccessToken, sa.ProjectID, nil
}

// Register upserts a device token.
func (p *Pusher) Register(d Device) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if d.Events != nil {
		d.Events = NormalizeEvents(d.Events)
	}
	if old, ok := p.devices[d.Token]; ok {
		d.Registered = old.Registered
		d.LastOK = old.LastOK
		if d.Events == nil {
			d.Events = old.Events
		}
	} else {
		d.Registered = time.Now().UnixMilli()
		log.Printf("push: registered device %q (%s)", d.Device, d.Node)
	}
	// One token per paired device: drop stale tokens of the same install.
	for t, o := range p.devices {
		if t != d.Token && ((d.DeviceID != "" && o.DeviceID == d.DeviceID) ||
			(d.DeviceID == "" && o.HostKey == d.HostKey && o.Device == d.Device && o.Node == d.Node)) {
			delete(p.devices, t)
		}
	}
	p.devices[d.Token] = &d
	p.saveLocked()
}

// UnregisterDevice drops every token of an unpaired device.
func (p *Pusher) UnregisterDevice(deviceID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for t, o := range p.devices {
		if o.DeviceID == deviceID {
			delete(p.devices, t)
		}
	}
	p.saveLocked()
}

// Unregister removes a token.
func (p *Pusher) Unregister(token string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.devices, token)
	p.saveLocked()
}

// Status describes push configuration.
func (p *Pusher) Status() map[string]any {
	p.mu.Lock()
	defer p.mu.Unlock()
	_ = p.loadSALocked()
	devs := []map[string]any{}
	for _, d := range p.devices {
		evs := d.Events
		if len(evs) == 0 {
			evs = DefaultEvents
		}
		devs = append(devs, map[string]any{"device": d.Device, "node": d.Node, "hostKey": d.HostKey, "registered": d.Registered,
			"events": evs, "lastOk": d.LastOK, "lastErr": d.LastErr, "token": d.Token[:min(12, len(d.Token))] + "…"})
	}
	m := map[string]any{"configured": p.sa != nil, "serviceAccountPath": p.SAPath, "content": p.Content, "devices": devs,
		"lastError": p.lastErr, "sent": p.sent}
	if p.sa != nil {
		m["projectId"] = p.sa.ProjectID
		m["clientEmail"] = p.sa.ClientEmail
	} else if p.saErr != nil {
		m["error"] = p.saErr.Error()
	}
	return m
}

// Configured reports whether a service account is available.
func (p *Pusher) Configured() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.loadSALocked() == nil
}

func (p *Pusher) data(m Message, hostKey string) map[string]string {
	d := map[string]string{
		"kind": m.Kind, "hostKey": hostKey, "seq": fmt.Sprint(m.Seq), "id": m.ID, "ts": fmt.Sprint(m.Ts),
	}
	if m.SessionID != "" {
		d["sessionId"] = m.SessionID
	}
	if m.TermID != "" {
		d["termId"] = m.TermID
	}
	if m.ReqID != "" {
		d["reqId"] = m.ReqID
	}
	if m.URL != "" {
		d["url"] = m.URL
	}
	if m.Options != nil {
		if b, err := json.Marshal(m.Options); err == nil && len(b) < 1200 {
			d["options"] = string(b)
		}
	}
	if p.Content != "minimal" {
		title, body := m.Title, m.Body
		if len(title) > 120 {
			title = title[:120] + "…"
		}
		if len(body) > 600 {
			body = body[:600] + "…"
		}
		d["title"], d["body"] = strings.ToValidUTF8(title, ""), strings.ToValidUTF8(body, "")
	}
	return d
}

// Send pushes a message to every registered device (asynchronously).
func (p *Pusher) Send(m Message) {
	p.mu.Lock()
	devs := make([]Device, 0, len(p.devices))
	for _, d := range p.devices {
		devs = append(devs, *d)
	}
	p.mu.Unlock()
	if len(devs) == 0 || !p.Configured() {
		return
	}
	for _, d := range devs {
		if !d.Allows(m.Kind) {
			continue
		}
		d := d
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			err := p.sendOne(ctx, d.Token, p.data(m, d.HostKey), m.Kind)
			p.mu.Lock()
			defer p.mu.Unlock()
			cur := p.devices[d.Token]
			if err == nil {
				p.sent++
				if cur != nil {
					cur.LastOK, cur.LastErr = time.Now().UnixMilli(), ""
				}
				return
			}
			p.lastErr = err.Error()
			log.Printf("push to %s: %v", d.Device, err)
			if errors.Is(err, errUnregistered) {
				delete(p.devices, d.Token)
				p.saveLocked()
			} else if cur != nil {
				cur.LastErr = err.Error()
			}
		}()
	}
}

// SendTo pushes to one token synchronously (used by push.test).
func (p *Pusher) SendTo(ctx context.Context, token, hostKey string, m Message) error {
	return p.sendOne(ctx, token, p.data(m, hostKey), m.Kind)
}

var errUnregistered = errors.New("token is no longer registered")

func (p *Pusher) sendOne(ctx context.Context, token string, data map[string]string, kind string) error {
	android := map[string]any{"priority": "HIGH", "ttl": "86400s"}
	if kind == "done" || kind == "term_done" {
		android["ttl"] = "21600s"
	} else if kind == "progress" || kind == "schedule_started" {
		android["ttl"] = "3600s"
	}
	payload, _ := json.Marshal(map[string]any{"message": map[string]any{"token": token, "data": data, "android": android}})
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(attempt*attempt) * 2 * time.Second):
			}
		}
		at, project, err := p.accessToken(ctx)
		if err != nil {
			return err
		}
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost,
			p.FCMBase+"/v1/projects/"+project+"/messages:send", bytes.NewReader(payload))
		req.Header.Set("Authorization", "Bearer "+at)
		req.Header.Set("Content-Type", "application/json")
		res, err := p.http.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		body, _ := io.ReadAll(io.LimitReader(res.Body, 64*1024))
		res.Body.Close()
		switch {
		case res.StatusCode == 200:
			p.mu.Lock()
			p.lastSent = time.Now().UnixMilli()
			p.mu.Unlock()
			return nil
		case res.StatusCode == 404 || (res.StatusCode == 400 && (bytes.Contains(body, []byte("UNREGISTERED")) || bytes.Contains(body, []byte("registration-token-not-registered")))):
			return errUnregistered
		case res.StatusCode == 401:
			p.mu.Lock()
			p.token = ""
			p.mu.Unlock()
			lastErr = fmt.Errorf("FCM 401: %s", strings.TrimSpace(string(body)))
		case res.StatusCode == 429 || res.StatusCode >= 500:
			lastErr = fmt.Errorf("FCM %d: %s", res.StatusCode, strings.TrimSpace(string(body)))
		default:
			return fmt.Errorf("FCM %d: %s", res.StatusCode, strings.TrimSpace(string(body)))
		}
	}
	return lastErr
}
