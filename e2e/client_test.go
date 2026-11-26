package e2e_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/flynn/noise"
	"github.com/gorilla/websocket"
)

var noiseSuite = noise.NewCipherSuite(noise.DH25519, noise.CipherChaChaPoly, noise.HashSHA256)

const (
	noiseMaxFragment = 65518
	noiseFlagFinal   = 0x01
	noiseFlagBinary  = 0x02
)

type WireHello struct {
	V        int    `json:"v"`
	Purpose  string `json:"purpose"`
	Term     string `json:"term,omitempty"`
	Cols     int    `json:"cols,omitempty"`
	Rows     int    `json:"rows,omitempty"`
	Name     string `json:"name,omitempty"`
	Platform string `json:"platform,omitempty"`
	App      string `json:"app,omitempty"`
}

type WireHelloReply struct {
	V               int      `json:"v"`
	ProtocolVersion int      `json:"protocolVersion"`
	Capabilities    []string `json:"capabilities"`
	Host            string   `json:"host"`
	HostID          string   `json:"hostId"`
	Version         string   `json:"version"`
	DeviceID        string   `json:"deviceId"`
	DeviceName      string   `json:"deviceName"`
	PublicURL       string   `json:"publicUrl"`
}

type PairResponse struct {
	URL         string `json:"url"`
	Base        string `json:"base"`
	Expires     int64  `json:"expires"`
	Host        string `json:"host"`
	Fingerprint string `json:"fingerprint"`
}

type ParsedPairLink struct {
	HTTPBase  string
	WSBase    string
	PairID    string
	DaemonPub []byte
	PSK       []byte
	HostName  string
}

type DeviceWire struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Platform   string `json:"platform,omitempty"`
	AppVersion string `json:"appVersion,omitempty"`
	PublicKey  string `json:"publicKey"`
	PairedAt   int64  `json:"pairedAt"`
	LastSeen   int64  `json:"lastSeen"`
}

type TokenUsageWire struct {
	InputTokens       int64   `json:"inputTokens"`
	OutputTokens      int64   `json:"outputTokens"`
	CachedReadTokens  int64   `json:"cachedReadTokens"`
	CachedWriteTokens int64   `json:"cachedWriteTokens"`
	ThoughtTokens     int64   `json:"thoughtTokens"`
	TotalTokens       int64   `json:"totalTokens"`
	CostUSD           float64 `json:"costUsd,omitempty"`
}

type CheckpointFileStatWire struct {
	Path      string `json:"path"`
	Status    string `json:"status"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
}

type TurnCheckpointWire struct {
	TurnIndex    int                      `json:"turnIndex"`
	PreRef       string                   `json:"preRef,omitempty"`
	PostRef      string                   `json:"postRef,omitempty"`
	PreSha       string                   `json:"preSha,omitempty"`
	PostSha      string                   `json:"postSha,omitempty"`
	Files        []CheckpointFileStatWire `json:"files,omitempty"`
	TotalAdd     int                      `json:"totalAdd"`
	TotalDel     int                      `json:"totalDel"`
	SkippedLarge int                      `json:"skippedLarge,omitempty"`
	CreatedAt    int64                    `json:"createdAt"`
	Error        string                   `json:"error,omitempty"`
}

type SessionSummaryWire struct {
	ID                 string              `json:"id"`
	Agent              string              `json:"agent"`
	Title              string              `json:"title"`
	Cwd                string              `json:"cwd"`
	State              string              `json:"state"`
	Live               bool                `json:"live"`
	Known              bool                `json:"known"`
	Attention          string              `json:"attention,omitempty"`
	PendingPermissions int                 `json:"pendingPermissions"`
	Queued             int                 `json:"queued"`
	Model              string              `json:"model,omitempty"`
	ModelName          string              `json:"modelName,omitempty"`
	Mode               string              `json:"mode,omitempty"`
	Thought            string              `json:"thought,omitempty"`
	SkipPermissions    bool                `json:"skipPermissions"`
	CreatedAt          int64               `json:"createdAt"`
	UpdatedAt          int64               `json:"updatedAt"`
	LastText           string              `json:"lastText,omitempty"`
	ContextUsed        int64               `json:"contextUsed"`
	ContextSize        int64               `json:"contextSize"`
	Usage              TokenUsageWire      `json:"usage"`
	Turns              int                 `json:"turns"`
	Archived           bool                `json:"archived"`
	Pinned             bool                `json:"pinned"`
	ForkedFrom         string              `json:"forkedFrom,omitempty"`
	Origin             string              `json:"origin,omitempty"`
	Error              string              `json:"error,omitempty"`
	PID                int                 `json:"pid,omitempty"`
	ProjectID          string              `json:"projectId,omitempty"`
	WorktreePath       string              `json:"worktreePath,omitempty"`
	Branch             string              `json:"branch,omitempty"`
	Settled            bool                `json:"settled,omitempty"`
	SnoozedUntil       int64               `json:"snoozedUntil,omitempty"`
	Draft              string              `json:"draft,omitempty"`
	LastCheckpoint     *TurnCheckpointWire `json:"lastCheckpoint,omitempty"`
}

type ModelInfoWire struct {
	ModelID           string `json:"modelId"`
	Name              string `json:"name"`
	Description       string `json:"description,omitempty"`
	Provider          string `json:"provider,omitempty"`
	ContextWindow     int    `json:"contextWindow,omitempty"`
	SupportsReasoning bool   `json:"supportsReasoning,omitempty"`
	SupportsTools     bool   `json:"supportsTools,omitempty"`
	SupportsImages    bool   `json:"supportsImages,omitempty"`
	IsCustom          bool   `json:"isCustom,omitempty"`
}

type ModelsStateWire struct {
	AvailableModels []ModelInfoWire `json:"availableModels"`
	CurrentModelID  string          `json:"currentModelId"`
}

type ModeInfoWire struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type ModesStateWire struct {
	AvailableModes []ModeInfoWire `json:"availableModes"`
	CurrentModeID  string         `json:"currentModeId"`
}

type ConfigOptionValueWire struct {
	Value       string `json:"value"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type ConfigOptionWire struct {
	ID           string                  `json:"id"`
	Name         string                  `json:"name"`
	Category     string                  `json:"category,omitempty"`
	Type         string                  `json:"type"`
	CurrentValue string                  `json:"currentValue"`
	Options      []ConfigOptionValueWire `json:"options"`
}

type SessionDetailWire struct {
	SessionSummaryWire
	AddDirs       []string             `json:"addDirs,omitempty"`
	Sandbox       bool                 `json:"sandbox,omitempty"`
	AgentName     string               `json:"agentName,omitempty"`
	ExtraArgs     []string             `json:"extraArgs,omitempty"`
	Models        *ModelsStateWire     `json:"models,omitempty"`
	Modes         *ModesStateWire      `json:"modes,omitempty"`
	ConfigOptions []ConfigOptionWire   `json:"configOptions,omitempty"`
	Checkpoints   []TurnCheckpointWire `json:"checkpoints,omitempty"`
}

type PermissionOptionWire struct {
	OptionID string `json:"optionId"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
}

type PermCardWire struct {
	ReqID       string                 `json:"reqId"`
	CallID      string                 `json:"callId,omitempty"`
	Title       string                 `json:"title,omitempty"`
	ToolKind    string                 `json:"toolKind,omitempty"`
	Options     []PermissionOptionWire `json:"options"`
	Chosen      string                 `json:"chosen,omitempty"`
	Tool        string                 `json:"tool,omitempty"`
	Kind        string                 `json:"kind,omitempty"`
	Input       json.RawMessage        `json:"input,omitempty"`
	Reason      string                 `json:"reason,omitempty"`
	BlockedPath string                 `json:"blockedPath,omitempty"`
	Answer      json.RawMessage        `json:"answer,omitempty"`
}

type ToolCallWire struct {
	CallID    string          `json:"callId"`
	Name      string          `json:"name,omitempty"`
	Kind      string          `json:"kind,omitempty"`
	Title     string          `json:"title,omitempty"`
	Status    string          `json:"status,omitempty"`
	Locations json.RawMessage `json:"locations,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	Output    json.RawMessage `json:"output,omitempty"`
	Content   json.RawMessage `json:"content,omitempty"`
	StartTs   int64           `json:"startTs,omitempty"`
	EndTs     int64           `json:"endTs,omitempty"`
}

type TurnInfoWire struct {
	TurnIndex  int                 `json:"turnIndex,omitempty"`
	StopReason string              `json:"stopReason"`
	Usage      *TokenUsageWire     `json:"usage,omitempty"`
	DurationMs int64               `json:"durationMs"`
	Error      string              `json:"error,omitempty"`
	Checkpoint *TurnCheckpointWire `json:"checkpoint,omitempty"`
}

type TranscriptItemWire struct {
	ID    string          `json:"id"`
	Kind  string          `json:"kind"`
	Seq   int64           `json:"seq"`
	Ts    int64           `json:"ts"`
	Text  string          `json:"text,omitempty"`
	State string          `json:"state,omitempty"`
	Tool  *ToolCallWire   `json:"tool,omitempty"`
	Plan  json.RawMessage `json:"plan,omitempty"`
	Perm  *PermCardWire   `json:"perm,omitempty"`
	Level string          `json:"level,omitempty"`
	Turn  *TurnInfoWire   `json:"turn,omitempty"`
	Trunc bool            `json:"trunc,omitempty"`
	Ref   string          `json:"ref,omitempty"`
}

type TranscriptOpWire struct {
	Seq   int64                `json:"seq"`
	Op    string               `json:"op"`
	ID    string               `json:"id,omitempty"`
	Item  *TranscriptItemWire  `json:"item,omitempty"`
	Text  string               `json:"text,omitempty"`
	Items []TranscriptItemWire `json:"items,omitempty"`
}

type PushEvent struct {
	Event string          `json:"event"`
	Data  json.RawMessage `json:"data"`
	Seq   int64           `json:"-"`
}

type RPCError struct {
	Message string
}

func (e *RPCError) Error() string {
	return e.Message
}

func GenerateDeviceKeypair() (noise.DHKey, error) {
	return noise.DH25519.GenerateKeypair(rand.Reader)
}

func KeyIDFromPub(pub []byte) string {
	sum := sha256.Sum256(pub)
	return base64.RawURLEncoding.EncodeToString(sum[:12])
}

func ParsePairURL(rawURL string) (*ParsedPairLink, error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(u.Path, "/p/") {
		return nil, fmt.Errorf("unexpected pair path %q in %q", u.Path, rawURL)
	}
	pairID := strings.TrimPrefix(u.Path, "/p/")
	frag, err := url.ParseQuery(u.Fragment)
	if err != nil {
		return nil, err
	}
	pubBytes, err := base64.RawURLEncoding.DecodeString(frag.Get("k"))
	if err != nil || len(pubBytes) != 32 {
		return nil, fmt.Errorf("invalid k fragment in pair URL: err=%v len=%d", err, len(pubBytes))
	}
	pskBytes, err := base64.RawURLEncoding.DecodeString(frag.Get("s"))
	if err != nil || len(pskBytes) != 32 {
		return nil, fmt.Errorf("invalid s fragment in pair URL: err=%v len=%d", err, len(pskBytes))
	}
	wsScheme := "ws"
	if u.Scheme == "https" {
		wsScheme = "wss"
	}
	return &ParsedPairLink{
		HTTPBase:  u.Scheme + "://" + u.Host,
		WSBase:    wsScheme + "://" + u.Host,
		PairID:    pairID,
		DaemonPub: pubBytes,
		PSK:       pskBytes,
		HostName:  frag.Get("n"),
	}, nil
}

type UnixLocalClient struct {
	sockPath string
	hc       *http.Client
}

func NewUnixLocalClient(sockPath string) *UnixLocalClient {
	tr := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", sockPath)
		},
	}
	return &UnixLocalClient{
		sockPath: sockPath,
		hc: &http.Client{
			Transport: tr,
			Timeout:   8 * time.Second,
		},
	}
}

func (u *UnixLocalClient) StatusRaw(ctx context.Context) (map[string]any, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://agentc/local/status", nil)
	if err != nil {
		return nil, nil, err
	}
	resp, err := u.hc.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, b, fmt.Errorf("local status HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, b, err
	}
	return m, b, nil
}

func (u *UnixLocalClient) Pair(ctx context.Context, baseURL string, ttlSec int) (*PairResponse, *ParsedPairLink, error) {
	if ttlSec <= 0 {
		ttlSec = 300
	}
	body, _ := json.Marshal(map[string]any{
		"ttlSeconds": ttlSec,
		"url":        baseURL,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://agentc/local/pair", bytes.NewReader(body))
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := u.hc.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("local pair HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	var pr PairResponse
	if err := json.Unmarshal(b, &pr); err != nil {
		return nil, nil, err
	}
	parsed, err := ParsePairURL(pr.URL)
	if err != nil {
		return &pr, nil, err
	}
	return &pr, parsed, nil
}

func (u *UnixLocalClient) Devices(ctx context.Context) ([]DeviceWire, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://agentc/local/devices", nil)
	if err != nil {
		return nil, err
	}
	resp, err := u.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("local devices HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	var list []DeviceWire
	if err := json.Unmarshal(b, &list); err != nil {
		return nil, err
	}
	return list, nil
}

func (u *UnixLocalClient) RevokeDevice(ctx context.Context, devID string) (bool, error) {
	body, _ := json.Marshal(map[string]any{"id": devID})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://agentc/local/devices/revoke", bytes.NewReader(body))
	if err != nil {
		return false, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := u.hc.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("local revoke HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	var out struct {
		Removed bool `json:"removed"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return false, err
	}
	return out.Removed, nil
}

func PairDeviceOverNoiseWS(ctx context.Context, link *ParsedPairLink, devKey noise.DHKey, deviceName, platform string, extraHeaders http.Header) (*WireHelloReply, error) {
	wsURL := fmt.Sprintf("%s/v1/ws?m=pair&p=%s", strings.TrimRight(link.WSBase, "/"), url.QueryEscape(link.PairID))
	dialer := websocket.Dialer{
		HandshakeTimeout: 8 * time.Second,
		Proxy:            nil,
	}
	wsConn, _, err := dialer.DialContext(ctx, wsURL, extraHeaders)
	if err != nil {
		return nil, err
	}
	defer wsConn.Close()

	hs, err := noise.NewHandshakeState(noise.Config{
		CipherSuite:           noiseSuite,
		Pattern:               noise.HandshakeIK,
		Initiator:             true,
		StaticKeypair:         devKey,
		PeerStatic:            link.DaemonPub,
		Prologue:              []byte("agentc/1 pair\n" + link.PairID),
		PresharedKey:          link.PSK,
		PresharedKeyPlacement: 1,
	})
	if err != nil {
		return nil, err
	}

	helloBytes, _ := json.Marshal(WireHello{
		V:        1,
		Purpose:  "rpc",
		Name:     deviceName,
		Platform: platform,
		App:      "0.5.0-e2e",
	})
	msg1, _, _, err := hs.WriteMessage(nil, helloBytes)
	if err != nil {
		return nil, err
	}
	_ = wsConn.SetWriteDeadline(time.Now().Add(8 * time.Second))
	if err := wsConn.WriteMessage(websocket.BinaryMessage, msg1); err != nil {
		return nil, err
	}
	_ = wsConn.SetReadDeadline(time.Now().Add(8 * time.Second))
	mt, msg2, err := wsConn.ReadMessage()
	if err != nil {
		return nil, err
	}
	if mt != websocket.BinaryMessage {
		return nil, fmt.Errorf("expected binary handshake msg2, got type %d", mt)
	}
	replyBytes, _, _, err := hs.ReadMessage(nil, msg2)
	if err != nil {
		return nil, err
	}
	var reply WireHelloReply
	if err := json.Unmarshal(replyBytes, &reply); err != nil {
		return nil, err
	}
	_ = wsConn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(1*time.Second))
	return &reply, nil
}

type sessionStateTracker struct {
	summary SessionSummaryWire
	detail  *SessionDetailWire
	seq     int64
	items   []TranscriptItemWire
}

type rpcRespSlot struct {
	result json.RawMessage
	err    error
}

type NoiseRPCClient struct {
	ws         *websocket.Conn
	csOut      *noise.CipherState
	csIn       *noise.CipherState
	helloReply WireHelloReply
	devKey     noise.DHKey
	daemonPub  []byte

	writeMu sync.Mutex
	nextID  atomic.Int64

	mu       sync.Mutex
	cond     *sync.Cond
	pending  map[int64]chan rpcRespSlot
	events   []PushEvent
	eventSeq int64
	sessions map[string]*sessionStateTracker
	closed   bool
	closeErr error
	closedCh chan struct{}
}

func ConnectNoiseRPCClient(ctx context.Context, wsBase string, daemonPub []byte, devKey noise.DHKey, deviceName, platform string, extraHeaders http.Header) (*NoiseRPCClient, error) {
	wsURL := strings.TrimRight(wsBase, "/") + "/v1/ws?m=conn"
	dialer := websocket.Dialer{
		HandshakeTimeout: 8 * time.Second,
		Proxy:            nil,
	}
	wsConn, _, err := dialer.DialContext(ctx, wsURL, extraHeaders)
	if err != nil {
		return nil, err
	}

	hs, err := noise.NewHandshakeState(noise.Config{
		CipherSuite:   noiseSuite,
		Pattern:       noise.HandshakeIK,
		Initiator:     true,
		StaticKeypair: devKey,
		PeerStatic:    daemonPub,
		Prologue:      []byte("agentc/1 conn\n"),
	})
	if err != nil {
		_ = wsConn.Close()
		return nil, err
	}

	helloBytes, _ := json.Marshal(WireHello{
		V:        1,
		Purpose:  "rpc",
		Name:     deviceName,
		Platform: platform,
		App:      "0.5.0-e2e",
	})
	msg1, _, _, err := hs.WriteMessage(nil, helloBytes)
	if err != nil {
		_ = wsConn.Close()
		return nil, err
	}
	_ = wsConn.SetWriteDeadline(time.Now().Add(8 * time.Second))
	if err := wsConn.WriteMessage(websocket.BinaryMessage, msg1); err != nil {
		_ = wsConn.Close()
		return nil, err
	}
	_ = wsConn.SetReadDeadline(time.Now().Add(8 * time.Second))
	mt, msg2, err := wsConn.ReadMessage()
	if err != nil {
		_ = wsConn.Close()
		return nil, err
	}
	if mt != websocket.BinaryMessage {
		_ = wsConn.Close()
		return nil, fmt.Errorf("expected binary handshake msg2, got type %d", mt)
	}
	replyBytes, cs1, cs2, err := hs.ReadMessage(nil, msg2)
	if err != nil {
		_ = wsConn.Close()
		return nil, err
	}
	_ = wsConn.SetReadDeadline(time.Time{})
	_ = wsConn.SetWriteDeadline(time.Time{})

	var reply WireHelloReply
	if err := json.Unmarshal(replyBytes, &reply); err != nil {
		_ = wsConn.Close()
		return nil, err
	}

	c := &NoiseRPCClient{
		ws:         wsConn,
		csOut:      cs1,
		csIn:       cs2,
		helloReply: reply,
		devKey:     devKey,
		daemonPub:  append([]byte(nil), daemonPub...),
		pending:    map[int64]chan rpcRespSlot{},
		sessions:   map[string]*sessionStateTracker{},
		closedCh:   make(chan struct{}),
	}
	c.cond = sync.NewCond(&c.mu)
	go c.readLoop()
	return c, nil
}

func (c *NoiseRPCClient) HelloReply() WireHelloReply {
	return c.helloReply
}

func (c *NoiseRPCClient) ClosedChan() <-chan struct{} {
	return c.closedCh
}

func (c *NoiseRPCClient) CloseError() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closeErr
}

func (c *NoiseRPCClient) Close() error {
	c.writeMu.Lock()
	_ = c.ws.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(500*time.Millisecond))
	err := c.ws.Close()
	c.writeMu.Unlock()
	return err
}

func (c *NoiseRPCClient) writeEncrypted(isBinary bool, data []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	rem := data
	for {
		n := len(rem)
		if n > noiseMaxFragment {
			n = noiseMaxFragment
		}
		flags := byte(0)
		if n == len(rem) {
			flags |= noiseFlagFinal
		}
		if isBinary {
			flags |= noiseFlagBinary
		}
		pt := make([]byte, 1+n)
		pt[0] = flags
		copy(pt[1:], rem[:n])

		ct, err := c.csOut.Encrypt(nil, nil, pt)
		if err != nil {
			return err
		}
		_ = c.ws.SetWriteDeadline(time.Now().Add(15 * time.Second))
		if err := c.ws.WriteMessage(websocket.BinaryMessage, ct); err != nil {
			return err
		}
		rem = rem[n:]
		if len(rem) == 0 {
			break
		}
	}
	return nil
}

func (c *NoiseRPCClient) readEncrypted() (bool, []byte, error) {
	var buf []byte
	for {
		mt, ct, err := c.ws.ReadMessage()
		if err != nil {
			return false, nil, err
		}
		if mt != websocket.BinaryMessage {
			return false, nil, fmt.Errorf("unexpected non-binary ws frame type %d", mt)
		}
		pt, err := c.csIn.Decrypt(nil, nil, ct)
		if err != nil {
			return false, nil, err
		}
		if len(pt) < 1 {
			return false, nil, errors.New("empty decrypted noise plaintext")
		}
		flags := pt[0]
		buf = append(buf, pt[1:]...)
		if flags&noiseFlagFinal != 0 {
			return (flags & noiseFlagBinary) != 0, buf, nil
		}
	}
}

func (c *NoiseRPCClient) readLoop() {
	var readErr error
	for {
		_, payload, err := c.readEncrypted()
		if err != nil {
			readErr = err
			break
		}
		var raw struct {
			ID     *int64          `json:"id"`
			Event  string          `json:"event"`
			Data   json.RawMessage `json:"data"`
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(payload, &raw) != nil {
			continue
		}
		if raw.Event != "" {
			c.mu.Lock()
			c.eventSeq++
			ev := PushEvent{
				Event: raw.Event,
				Data:  raw.Data,
				Seq:   c.eventSeq,
			}
			c.events = append(c.events, ev)
			c.applyEventLocked(ev)
			c.cond.Broadcast()
			c.mu.Unlock()
			continue
		}
		if raw.ID != nil {
			id := *raw.ID
			c.mu.Lock()
			ch := c.pending[id]
			delete(c.pending, id)
			c.mu.Unlock()
			if ch != nil {
				if raw.Error != nil {
					ch <- rpcRespSlot{err: &RPCError{Message: raw.Error.Message}}
				} else {
					ch <- rpcRespSlot{result: raw.Result}
				}
			}
		}
	}

	c.mu.Lock()
	c.closed = true
	c.closeErr = readErr
	for id, ch := range c.pending {
		delete(c.pending, id)
		ch <- rpcRespSlot{err: fmt.Errorf("noise connection closed: %w", readErr)}
	}
	close(c.closedCh)
	c.cond.Broadcast()
	c.mu.Unlock()
}

func (c *NoiseRPCClient) ensureTrackerLocked(sid string) *sessionStateTracker {
	st := c.sessions[sid]
	if st == nil {
		st = &sessionStateTracker{}
		c.sessions[sid] = st
	}
	return st
}

func applyTranscriptOp(items []TranscriptItemWire, op TranscriptOpWire) []TranscriptItemWire {
	switch op.Op {
	case "reset":
		cp := make([]TranscriptItemWire, len(op.Items))
		copy(cp, op.Items)
		return cp
	case "add":
		if op.Item != nil {
			for i := range items {
				if items[i].ID == op.Item.ID {
					items[i] = *op.Item
					return items
				}
			}
			return append(items, *op.Item)
		}
	case "append":
		for i := range items {
			if items[i].ID == op.ID {
				items[i].Text += op.Text
				return items
			}
		}
	case "set":
		if op.Item != nil {
			for i := range items {
				if items[i].ID == op.ID || items[i].ID == op.Item.ID {
					items[i] = *op.Item
					return items
				}
			}
			return append(items, *op.Item)
		}
	case "remove":
		out := items[:0]
		for _, it := range items {
			if it.ID != op.ID {
				out = append(out, it)
			}
		}
		return out
	}
	return items
}

func (c *NoiseRPCClient) applyEventLocked(ev PushEvent) {
	switch ev.Event {
	case "session.snapshot":
		var snap struct {
			ID      string               `json:"id"`
			Session SessionDetailWire    `json:"session"`
			Seq     int64                `json:"seq"`
			Items   []TranscriptItemWire `json:"items"`
		}
		if json.Unmarshal(ev.Data, &snap) == nil && snap.ID != "" {
			st := c.ensureTrackerLocked(snap.ID)
			st.detail = &snap.Session
			st.summary = snap.Session.SessionSummaryWire
			st.seq = snap.Seq
			st.items = append([]TranscriptItemWire(nil), snap.Items...)
		}
	case "session.delta":
		var delta struct {
			ID      string             `json:"id"`
			Session SessionDetailWire  `json:"session"`
			Seq     int64              `json:"seq"`
			Ops     []TranscriptOpWire `json:"ops"`
		}
		if json.Unmarshal(ev.Data, &delta) == nil && delta.ID != "" {
			st := c.ensureTrackerLocked(delta.ID)
			st.detail = &delta.Session
			st.summary = delta.Session.SessionSummaryWire
			st.seq = delta.Seq
			for _, op := range delta.Ops {
				st.items = applyTranscriptOp(st.items, op)
			}
		}
	case "session.ops":
		var opsEv struct {
			ID  string             `json:"id"`
			Ops []TranscriptOpWire `json:"ops"`
		}
		if json.Unmarshal(ev.Data, &opsEv) == nil && opsEv.ID != "" {
			st := c.ensureTrackerLocked(opsEv.ID)
			for _, op := range opsEv.Ops {
				if op.Seq > st.seq {
					st.seq = op.Seq
				}
				st.items = applyTranscriptOp(st.items, op)
			}
		}
	case "session.changed":
		var sum SessionSummaryWire
		if json.Unmarshal(ev.Data, &sum) == nil && sum.ID != "" {
			st := c.ensureTrackerLocked(sum.ID)
			st.summary = sum
		}
	case "session.config":
		var cfgEv struct {
			ID      string            `json:"id"`
			Session SessionDetailWire `json:"session"`
		}
		if json.Unmarshal(ev.Data, &cfgEv) == nil && cfgEv.ID != "" {
			st := c.ensureTrackerLocked(cfgEv.ID)
			st.detail = &cfgEv.Session
			st.summary = cfgEv.Session.SessionSummaryWire
		}
	}
}

func (c *NoiseRPCClient) CallRaw(ctx context.Context, method string, params any) (json.RawMessage, error) {
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
	}

	id := c.nextID.Add(1)
	if params == nil {
		params = map[string]any{}
	}
	reqBytes, err := json.Marshal(map[string]any{
		"id":     id,
		"method": method,
		"params": params,
	})
	if err != nil {
		return nil, err
	}

	ch := make(chan rpcRespSlot, 1)
	c.mu.Lock()
	if c.closed {
		err := c.closeErr
		c.mu.Unlock()
		return nil, fmt.Errorf("client already closed: %w", err)
	}
	c.pending[id] = ch
	c.mu.Unlock()

	if err := c.writeEncrypted(false, reqBytes); err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, err
	}

	select {
	case slot := <-ch:
		return slot.result, slot.err
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, fmt.Errorf("rpc %s timed out: %w", method, ctx.Err())
	}
}

func (c *NoiseRPCClient) Call(ctx context.Context, method string, params any, out any) error {
	raw, err := c.CallRaw(ctx, method, params)
	if err != nil {
		return err
	}
	if out != nil && len(raw) > 0 {
		return json.Unmarshal(raw, out)
	}
	return nil
}

func (c *NoiseRPCClient) EventMarker() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.eventSeq
}

func (c *NoiseRPCClient) EventsSince(marker int64) []PushEvent {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []PushEvent
	for _, ev := range c.events {
		if ev.Seq > marker {
			out = append(out, ev)
		}
	}
	return out
}

func (c *NoiseRPCClient) WaitForEvent(sinceMarker int64, timeout time.Duration, pred func(PushEvent) bool) (PushEvent, error) {
	deadline := time.Now().Add(timeout)
	done := make(chan struct{})
	defer close(done)
	go func() {
		timer := time.NewTimer(timeout)
		defer timer.Stop()
		select {
		case <-timer.C:
			c.mu.Lock()
			c.cond.Broadcast()
			c.mu.Unlock()
		case <-done:
		}
	}()

	c.mu.Lock()
	defer c.mu.Unlock()
	scanIdx := 0
	for {
		for scanIdx < len(c.events) {
			ev := c.events[scanIdx]
			scanIdx++
			if ev.Seq <= sinceMarker {
				continue
			}
			if pred(ev) {
				return ev, nil
			}
		}
		if c.closed {
			return PushEvent{}, fmt.Errorf("connection closed waiting for event: %w", c.closeErr)
		}
		if time.Now().After(deadline) {
			return PushEvent{}, fmt.Errorf("timed out after %v waiting for event", timeout)
		}
		c.cond.Wait()
	}
}

func (c *NoiseRPCClient) WaitForSession(sid string, timeout time.Duration, pred func(sum SessionSummaryWire, detail *SessionDetailWire, items []TranscriptItemWire) bool) (SessionSummaryWire, []TranscriptItemWire, error) {
	deadline := time.Now().Add(timeout)
	done := make(chan struct{})
	defer close(done)
	go func() {
		timer := time.NewTimer(timeout)
		defer timer.Stop()
		select {
		case <-timer.C:
			c.mu.Lock()
			c.cond.Broadcast()
			c.mu.Unlock()
		case <-done:
		}
	}()

	c.mu.Lock()
	defer c.mu.Unlock()
	for {
		if st := c.sessions[sid]; st != nil {
			itemsCopy := make([]TranscriptItemWire, len(st.items))
			copy(itemsCopy, st.items)
			if pred(st.summary, st.detail, itemsCopy) {
				return st.summary, itemsCopy, nil
			}
		}
		if c.closed {
			return SessionSummaryWire{}, nil, fmt.Errorf("connection closed waiting for session %s: %w", sid, c.closeErr)
		}
		if time.Now().After(deadline) {
			var lastState string
			var lastTurns int
			var lastErr string
			if st := c.sessions[sid]; st != nil {
				lastState = st.summary.State
				lastTurns = st.summary.Turns
				lastErr = st.summary.Error
			}
			return SessionSummaryWire{}, nil, fmt.Errorf("timed out after %v waiting for session %s (last state=%q turns=%d err=%q)", timeout, sid, lastState, lastTurns, lastErr)
		}
		c.cond.Wait()
	}
}

func (c *NoiseRPCClient) WaitForTurnIdle(sid string, minTurns int, timeout time.Duration) (SessionSummaryWire, []TranscriptItemWire, error) {
	sum, items, err := c.WaitForSession(sid, timeout, func(sum SessionSummaryWire, _ *SessionDetailWire, items []TranscriptItemWire) bool {
		if sum.State == "idle" && sum.Turns >= minTurns {
			return true
		}
		if sum.State == "idle" && sum.Error != "" {
			return true
		}
		return false
	})
	if err != nil {
		return sum, items, err
	}
	if sum.State == "idle" && sum.Turns < minTurns && sum.Error != "" {
		return sum, items, fmt.Errorf("session %s failed before turn %d: %s", sid, minTurns, sum.Error)
	}
	return sum, items, nil
}

func (c *NoiseRPCClient) WaitForTurnSettled(sid string, minTurns int, timeout time.Duration) (SessionSummaryWire, []TranscriptItemWire, error) {
	return c.WaitForSession(sid, timeout, func(sum SessionSummaryWire, _ *SessionDetailWire, items []TranscriptItemWire) bool {
		if sum.State != "idle" {
			return false
		}
		if sum.Turns >= minTurns || sum.Error != "" {
			return true
		}
		for _, it := range items {
			if it.Kind == "notice" && it.Level == "error" {
				return true
			}
		}
		return false
	})
}

func (c *NoiseRPCClient) WaitForPendingPerm(sid string, timeout time.Duration) (TranscriptItemWire, error) {
	var found TranscriptItemWire
	var settledWithoutPerm bool
	initSum, _, _, _ := c.SessionSnapshotState(sid)
	baseTurns := initSum.Turns
	sum, _, err := c.WaitForSession(sid, timeout, func(s SessionSummaryWire, _ *SessionDetailWire, items []TranscriptItemWire) bool {
		for _, it := range items {
			if it.Kind == "perm" && it.State == "pending" && it.Perm != nil && it.Perm.ReqID != "" {
				found = it
				return true
			}
		}
		if s.State == "idle" && (s.Turns > baseTurns || s.Error != "") {
			settledWithoutPerm = true
			return true
		}
		return false
	})
	if err != nil {
		return found, err
	}
	if settledWithoutPerm && found.ID == "" {
		return found, fmt.Errorf("turn finished (state=%q turns=%d err=%q) without emitting a pending permission request", sum.State, sum.Turns, sum.Error)
	}
	return found, nil
}

func (c *NoiseRPCClient) SessionSnapshotState(sid string) (SessionSummaryWire, *SessionDetailWire, int64, []TranscriptItemWire) {
	c.mu.Lock()
	defer c.mu.Unlock()
	st := c.sessions[sid]
	if st == nil {
		return SessionSummaryWire{}, nil, 0, nil
	}
	itemsCopy := make([]TranscriptItemWire, len(st.items))
	copy(itemsCopy, st.items)
	var detCopy *SessionDetailWire
	if st.detail != nil {
		tmp := *st.detail
		detCopy = &tmp
	}
	return st.summary, detCopy, st.seq, itemsCopy
}

func PickPermOptionID(card *PermCardWire, intent string) string {
	if card == nil || len(card.Options) == 0 {
		switch intent {
		case "allow_always":
			return "acceptForSession"
		case "reject_once":
			return "decline"
		default:
			return "accept"
		}
	}
	switch intent {
	case "allow_once":
		for _, o := range card.Options {
			if o.Kind == "allow_once" || slices.Contains([]string{"accept", "allow", "allow_once"}, o.OptionID) {
				return o.OptionID
			}
		}
	case "allow_always":
		for _, o := range card.Options {
			if o.Kind == "allow_always" || slices.Contains([]string{"acceptForSession", "allow_always", "always"}, o.OptionID) {
				return o.OptionID
			}
		}
	case "reject_once":
		for _, o := range card.Options {
			if o.Kind == "reject_once" || o.Kind == "reject" || slices.Contains([]string{"decline", "deny", "reject"}, o.OptionID) {
				return o.OptionID
			}
		}
	}
	return card.Options[0].OptionID
}
