// Package secure is the end-to-end encrypted transport between the daemon and
// paired apps. It runs over a WebSocket that may cross an untrusted relay (a
// Cloudflare quick tunnel), so the relay only ever sees ciphertext:
//
//   - The daemon has a long-term X25519 identity key. A phone learns it, plus a
//     one-time pairing secret, from the QR code `agentc pair` shows.
//   - Pairing: Noise_IKpsk1_25519_ChaChaPoly_SHA256. The phone proves it knows
//     the pairing secret; its static key is then registered as a device.
//   - Connecting: Noise_IK_25519_ChaChaPoly_SHA256. Only registered device keys
//     are accepted, and only the real daemon can complete the handshake.
//
// After the handshake every WebSocket message is one Noise transport message
// whose plaintext is [flags][data]; flags bit0 = final fragment, bit1 = binary.
package secure

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/flynn/noise"
)

// Suite is the Noise cipher suite used everywhere.
var Suite = noise.NewCipherSuite(noise.DH25519, noise.CipherChaChaPoly, noise.HashSHA256)

// Prologues bind the handshake to its purpose.
const (
	ProloguePrefix = "agentc/1 "
	ModeConnect    = "conn"
	ModePair       = "pair"
)

// Prologue returns the handshake prologue for a mode.
func Prologue(mode, pairID string) []byte {
	if mode == ModePair {
		return []byte(ProloguePrefix + ModePair + "\n" + pairID)
	}
	return []byte(ProloguePrefix + ModeConnect + "\n")
}

// KeyID derives a short stable identifier from a public key.
func KeyID(pub []byte) string {
	sum := sha256.Sum256(pub)
	return base64.RawURLEncoding.EncodeToString(sum[:12])
}

// Fingerprint is a human-comparable form of a public key.
func Fingerprint(pub []byte) string {
	sum := sha256.Sum256(pub)
	h := hex.EncodeToString(sum[:8])
	return h[0:4] + "-" + h[4:8] + "-" + h[8:12] + "-" + h[12:16]
}

// Identity is the daemon's long-term key pair.
type Identity struct {
	Key noise.DHKey
}

type identityFile struct {
	Private string `json:"private"`
	Public  string `json:"public"`
}

// LoadOrCreateIdentity reads the identity from path, creating it on first use.
func LoadOrCreateIdentity(path string) (*Identity, error) {
	if b, err := os.ReadFile(path); err == nil {
		var f identityFile
		if err := json.Unmarshal(b, &f); err != nil {
			return nil, fmt.Errorf("identity %s: %w", path, err)
		}
		priv, err1 := base64.StdEncoding.DecodeString(f.Private)
		pub, err2 := base64.StdEncoding.DecodeString(f.Public)
		if err1 != nil || err2 != nil || len(priv) != 32 || len(pub) != 32 {
			return nil, fmt.Errorf("identity %s: malformed key", path)
		}
		return &Identity{Key: noise.DHKey{Private: priv, Public: pub}}, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	k, err := noise.DH25519.GenerateKeypair(rand.Reader)
	if err != nil {
		return nil, err
	}
	b, _ := json.MarshalIndent(identityFile{Private: base64.StdEncoding.EncodeToString(k.Private), Public: base64.StdEncoding.EncodeToString(k.Public)}, "", "  ")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	if err := writeFileAtomic(path, b, 0o600); err != nil {
		return nil, err
	}
	return &Identity{Key: k}, nil
}

// ID returns the daemon's stable host id.
func (id *Identity) ID() string { return KeyID(id.Key.Public) }

// PublicB64 is the public key in URL-safe base64 (as used in QR codes).
func (id *Identity) PublicB64() string { return base64.RawURLEncoding.EncodeToString(id.Key.Public) }

// Device is a paired app.
type Device struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Platform  string `json:"platform,omitempty"`
	AppVer    string `json:"appVersion,omitempty"`
	PublicKey string `json:"publicKey"`
	PairedAt  int64  `json:"pairedAt"`
	LastSeen  int64  `json:"lastSeen"`
}

// Registry is the persistent list of paired devices.
type Registry struct {
	path    string
	mu      sync.Mutex
	devices map[string]*Device // by id
	dirty   bool
}

// OpenRegistry loads (or starts) the device registry at path.
func OpenRegistry(path string) (*Registry, error) {
	r := &Registry{path: path, devices: map[string]*Device{}}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return r, nil
	}
	if err != nil {
		return nil, err
	}
	var list []*Device
	if err := json.Unmarshal(b, &list); err != nil {
		return nil, fmt.Errorf("devices %s: %w", path, err)
	}
	for _, d := range list {
		r.devices[d.ID] = d
	}
	return r, nil
}

func (r *Registry) saveLocked() error {
	list := make([]*Device, 0, len(r.devices))
	for _, d := range r.devices {
		list = append(list, d)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].PairedAt < list[j].PairedAt })
	b, _ := json.MarshalIndent(list, "", "  ")
	if err := os.MkdirAll(filepath.Dir(r.path), 0o700); err != nil {
		return err
	}
	r.dirty = false
	return writeFileAtomic(r.path, b, 0o600)
}

// Lookup returns the device with this static public key.
func (r *Registry) Lookup(pub []byte) *Device {
	r.mu.Lock()
	defer r.mu.Unlock()
	d := r.devices[KeyID(pub)]
	if d == nil || d.PublicKey != base64.StdEncoding.EncodeToString(pub) {
		return nil
	}
	c := *d
	return &c
}

// Add registers (or re-registers) a device key.
func (r *Registry) Add(pub []byte, name, platform, appVer string) (Device, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now().UnixMilli()
	id := KeyID(pub)
	d := r.devices[id]
	if d == nil {
		d = &Device{ID: id, PublicKey: base64.StdEncoding.EncodeToString(pub), PairedAt: now}
		r.devices[id] = d
	}
	d.Name, d.Platform, d.AppVer, d.LastSeen = name, platform, appVer, now
	return *d, r.saveLocked()
}

// Remove unpairs a device.
func (r *Registry) Remove(id string) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.devices[id]; !ok {
		return false, nil
	}
	delete(r.devices, id)
	return true, r.saveLocked()
}

// Rename sets a device's display name.
func (r *Registry) Rename(id, name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	d := r.devices[id]
	if d == nil {
		return errors.New("no such device")
	}
	d.Name = name
	return r.saveLocked()
}

// Touch records that a device connected (persisted lazily).
func (r *Registry) Touch(id, appVer string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if d := r.devices[id]; d != nil {
		d.LastSeen = time.Now().UnixMilli()
		if appVer != "" {
			d.AppVer = appVer
		}
		r.dirty = true
	}
}

// Flush persists pending LastSeen updates.
func (r *Registry) Flush() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.dirty {
		_ = r.saveLocked()
	}
}

// List returns all devices, oldest first.
func (r *Registry) List() []Device {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Device, 0, len(r.devices))
	for _, d := range r.devices {
		out = append(out, *d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PairedAt < out[j].PairedAt })
	return out
}

// PairToken is a one-time pairing secret shown in a QR code.
type PairToken struct {
	ID      string
	PSK     []byte
	Expires time.Time
}

// Tokens holds outstanding pairing tokens (in memory only).
type Tokens struct {
	mu     sync.Mutex
	tokens map[string]*PairToken
}

// NewTokens returns an empty token set.
func NewTokens() *Tokens { return &Tokens{tokens: map[string]*PairToken{}} }

// Create makes a new pairing token valid for ttl.
func (t *Tokens) Create(ttl time.Duration) (*PairToken, error) {
	idb := make([]byte, 9)
	psk := make([]byte, 32)
	if _, err := rand.Read(idb); err != nil {
		return nil, err
	}
	if _, err := rand.Read(psk); err != nil {
		return nil, err
	}
	tok := &PairToken{ID: base64.RawURLEncoding.EncodeToString(idb), PSK: psk, Expires: time.Now().Add(ttl)}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.gcLocked()
	if len(t.tokens) >= 16 {
		return nil, errors.New("too many pairing codes are open; wait for them to expire")
	}
	t.tokens[tok.ID] = tok
	return tok, nil
}

func (t *Tokens) gcLocked() {
	now := time.Now()
	for id, tok := range t.tokens {
		if now.After(tok.Expires) {
			delete(t.tokens, id)
		}
	}
}

// Get returns a valid token.
func (t *Tokens) Get(id string) (*PairToken, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.gcLocked()
	tok, ok := t.tokens[id]
	return tok, ok
}

// Take atomically claims a valid token; only the first caller succeeds.
func (t *Tokens) Take(id string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.gcLocked()
	if _, ok := t.tokens[id]; !ok {
		return false
	}
	delete(t.tokens, id)
	return true
}

func writeFileAtomic(path string, b []byte, mode os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, mode); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
