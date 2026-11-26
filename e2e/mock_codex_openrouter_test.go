package e2e_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

type OpenRouterModelFixture struct {
	ID                  string         `json:"id"`
	Name                string         `json:"name"`
	Description         string         `json:"description"`
	ContextLength       int            `json:"context_length"`
	SupportedParameters []string       `json:"supported_parameters"`
	Architecture        map[string]any `json:"architecture"`
	TopProvider         map[string]any `json:"top_provider"`
	Pricing             map[string]any `json:"pricing"`
}

type RecordedCatalogRequest struct {
	Timestamp  time.Time
	Path       string
	AuthHeader string
}

type RecordedResponsesRequest struct {
	Timestamp       time.Time
	Path            string
	AuthHeader      string
	Model           string
	MaxOutputTokens int
	Stream          bool
	Tools           []map[string]any
	PromptText      string
	RawBody         map[string]any
}

type MockOpenRouterServer struct {
	listener   net.Listener
	httpServer *http.Server
	leafTLS    *tls.Config
	caCertPath string
	addr       string

	mu                 sync.Mutex
	baseModels         []OpenRouterModelFixture
	dynamicModels      []OpenRouterModelFixture
	userModelsStatus   int
	publicModelsStatus int
	forceRespStatus    int
	forceRespBody      string
	catalogReqs        []RecordedCatalogRequest
	responsesReqs      []RecordedResponsesRequest
	tunnelClosers      map[*singleConnListener]*http.Server
}

type connWithCloseHook struct {
	net.Conn
	once    sync.Once
	onClose func()
}

func (c *connWithCloseHook) Close() error {
	err := c.Conn.Close()
	c.once.Do(func() {
		if c.onClose != nil {
			c.onClose()
		}
	})
	return err
}

type singleConnListener struct {
	conn      net.Conn
	once      sync.Once
	closeOnce sync.Once
	done      chan struct{}
}

func (l *singleConnListener) Accept() (net.Conn, error) {
	var c net.Conn
	l.once.Do(func() {
		c = l.conn
	})
	if c != nil {
		return c, nil
	}
	<-l.done
	return nil, net.ErrClosed
}

func (l *singleConnListener) Close() error {
	l.closeOnce.Do(func() {
		close(l.done)
	})
	return nil
}

func (l *singleConnListener) Addr() net.Addr {
	return l.conn.LocalAddr()
}

func NewMockOpenRouterServer(caCertPath string) (*MockOpenRouterServer, error) {
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	caTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1001),
		Subject:               pkix.Name{CommonName: "AgentC E2E Mock OpenRouter Root CA"},
		NotBefore:             now.Add(-1 * time.Hour),
		NotAfter:              now.Add(48 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		return nil, err
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		return nil, err
	}
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})
	if err := os.WriteFile(caCertPath, caPEM, 0o644); err != nil {
		return nil, err
	}

	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	leafTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1002),
		Subject:      pkix.Name{CommonName: "openrouter.ai"},
		DNSNames:     []string{"openrouter.ai", "www.openrouter.ai", "localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		NotBefore:    now.Add(-1 * time.Hour),
		NotAfter:     now.Add(48 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTmpl, caCert, &leafKey.PublicKey, caKey)
	if err != nil {
		return nil, err
	}
	leafKeyDER, err := x509.MarshalECPrivateKey(leafKey)
	if err != nil {
		return nil, err
	}
	leafPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER})
	leafKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: leafKeyDER})
	tlsPair, err := tls.X509KeyPair(leafPEM, leafKeyPEM)
	if err != nil {
		return nil, err
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}

	m := &MockOpenRouterServer{
		listener:   ln,
		caCertPath: caCertPath,
		addr:       ln.Addr().String(),
		leafTLS: &tls.Config{
			Certificates: []tls.Certificate{tlsPair},
			NextProtos:   []string{"http/1.1"},
		},
		baseModels: defaultOpenRouterFixtures(),
	}

	srv := &http.Server{
		Handler:           http.HandlerFunc(m.entryHandler),
		ReadHeaderTimeout: 10 * time.Second,
	}
	m.httpServer = srv
	go func() {
		_ = srv.Serve(ln)
	}()
	return m, nil
}

func defaultOpenRouterFixtures() []OpenRouterModelFixture {
	return []OpenRouterModelFixture{
		{
			ID:                  "google/gemini-3-flash-preview",
			Name:                "Google: Gemini 3 Flash Preview",
			Description:         "Fast multimodal reasoning and coding model via Google BYOK",
			ContextLength:       1048576,
			SupportedParameters: []string{"tools", "tool_choice", "reasoning", "include_reasoning", "max_tokens"},
			Architecture: map[string]any{
				"modality":          "text+image->text",
				"input_modalities":  []string{"text", "image"},
				"output_modalities": []string{"text"},
			},
			TopProvider: map[string]any{"context_length": 1048576, "max_completion_tokens": 65536},
			Pricing:     map[string]any{"prompt": "0.0000005", "completion": "0.000003"},
		},
		{
			ID:                  "google/gemini-2.5-flash",
			Name:                "Google: Gemini 2.5 Flash",
			Description:         "Balanced high-speed reasoning model",
			ContextLength:       1048576,
			SupportedParameters: []string{"tools", "tool_choice", "reasoning", "include_reasoning", "max_tokens"},
			Architecture: map[string]any{
				"modality":          "text+image->text",
				"input_modalities":  []string{"text", "image"},
				"output_modalities": []string{"text"},
			},
			TopProvider: map[string]any{"context_length": 1048576, "max_completion_tokens": 65536},
			Pricing:     map[string]any{"prompt": "0.0000003", "completion": "0.0000025"},
		},
		{
			ID:                  "openai/gpt-4.1-mini",
			Name:                "OpenAI: GPT-4.1 Mini",
			Description:         "Compact OpenAI coding model",
			ContextLength:       1047576,
			SupportedParameters: []string{"tools", "tool_choice", "max_tokens"},
			Architecture: map[string]any{
				"modality":          "text+image->text",
				"input_modalities":  []string{"text", "image"},
				"output_modalities": []string{"text"},
			},
			TopProvider: map[string]any{"context_length": 1047576, "max_completion_tokens": 32768},
			Pricing:     map[string]any{"prompt": "0.0000004", "completion": "0.0000016"},
		},
		{
			ID:                  "deepseek/deepseek-v4.1-flash",
			Name:                "DeepSeek: V4.1 Flash",
			Description:         "Fast reasoning coder model",
			ContextLength:       131072,
			SupportedParameters: []string{"tools", "reasoning", "max_tokens"},
			Architecture: map[string]any{
				"modality":          "text->text",
				"input_modalities":  []string{"text"},
				"output_modalities": []string{"text"},
			},
			TopProvider: map[string]any{"context_length": 131072, "max_completion_tokens": 16384},
			Pricing:     map[string]any{"prompt": "0.0000002", "completion": "0.0000008"},
		},
	}
}

func (m *MockOpenRouterServer) ProxyURL() string {
	return "http://" + m.addr
}

func (m *MockOpenRouterServer) BaseAPIURL() string {
	return "http://" + m.addr + "/api/v1"
}

func (m *MockOpenRouterServer) CACertPath() string {
	return m.caCertPath
}

func (m *MockOpenRouterServer) Close() {
	if m == nil {
		return
	}
	_ = m.httpServer.Close()
	_ = m.listener.Close()
	m.mu.Lock()
	type pair struct {
		scl *singleConnListener
		srv *http.Server
	}
	var active []pair
	for scl, srv := range m.tunnelClosers {
		active = append(active, pair{scl: scl, srv: srv})
	}
	m.tunnelClosers = nil
	m.mu.Unlock()
	for _, p := range active {
		_ = p.scl.Close()
		_ = p.srv.Close()
	}
}

func (m *MockOpenRouterServer) ResetOverrides() {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.dynamicModels = nil
	m.userModelsStatus = 0
	m.publicModelsStatus = 0
	m.forceRespStatus = 0
	m.forceRespBody = ""
}

func (m *MockOpenRouterServer) SetModelsStatusOverrides(userStatus, publicStatus int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.userModelsStatus = userStatus
	m.publicModelsStatus = publicStatus
}

func (m *MockOpenRouterServer) SetResponsesErrorOverride(status int, body string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.forceRespStatus = status
	m.forceRespBody = body
}

func (m *MockOpenRouterServer) AddDynamicModel(mod OpenRouterModelFixture) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.dynamicModels = append(m.dynamicModels, mod)
}

func (m *MockOpenRouterServer) CatalogRequests() []RecordedCatalogRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]RecordedCatalogRequest, len(m.catalogReqs))
	copy(out, m.catalogReqs)
	return out
}

func (m *MockOpenRouterServer) ResponsesRequests() []RecordedResponsesRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]RecordedResponsesRequest, len(m.responsesReqs))
	copy(out, m.responsesReqs)
	return out
}

func (m *MockOpenRouterServer) ClearRequests() {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.catalogReqs = nil
	m.responsesReqs = nil
}

func (m *MockOpenRouterServer) entryHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodConnect {
		hj, ok := w.(http.Hijacker)
		if !ok {
			http.Error(w, "hijack unsupported", http.StatusInternalServerError)
			return
		}
		rawConn, _, err := hj.Hijack()
		if err != nil {
			return
		}
		_, _ = rawConn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))
		tlsConn := tls.Server(rawConn, m.leafTLS)
		if err := tlsConn.Handshake(); err != nil {
			_ = rawConn.Close()
			return
		}
		scl := &singleConnListener{
			done: make(chan struct{}),
		}
		scl.conn = &connWithCloseHook{
			Conn: tlsConn,
			onClose: func() {
				_ = scl.Close()
			},
		}
		innerSrv := &http.Server{
			Handler:           http.HandlerFunc(m.apiHandler),
			ReadHeaderTimeout: 10 * time.Second,
		}
		m.mu.Lock()
		if m.tunnelClosers == nil {
			m.tunnelClosers = make(map[*singleConnListener]*http.Server)
		}
		m.tunnelClosers[scl] = innerSrv
		m.mu.Unlock()
		go func() {
			_ = innerSrv.Serve(scl)
			m.mu.Lock()
			delete(m.tunnelClosers, scl)
			m.mu.Unlock()
		}()
		return
	}
	m.apiHandler(w, r)
}

var affordOverrideRe = regexp.MustCompile(`\[afford:(\d+)\]`)

func (m *MockOpenRouterServer) apiHandler(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	switch {
	case r.Method == http.MethodGet && (path == "/api/v1/models/user" || path == "/models/user"):
		m.mu.Lock()
		m.catalogReqs = append(m.catalogReqs, RecordedCatalogRequest{
			Timestamp:  time.Now(),
			Path:       path,
			AuthHeader: r.Header.Get("Authorization"),
		})
		st := m.userModelsStatus
		models := append([]OpenRouterModelFixture{}, m.baseModels...)
		models = append(models, m.dynamicModels...)
		m.mu.Unlock()

		if st >= 400 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(st)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error": map[string]any{"message": "simulated user models error", "code": st},
			})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": models})

	case r.Method == http.MethodGet && (path == "/api/v1/models" || path == "/models"):
		m.mu.Lock()
		m.catalogReqs = append(m.catalogReqs, RecordedCatalogRequest{
			Timestamp:  time.Now(),
			Path:       path,
			AuthHeader: r.Header.Get("Authorization"),
		})
		st := m.publicModelsStatus
		models := append([]OpenRouterModelFixture{}, m.baseModels...)
		models = append(models, m.dynamicModels...)
		m.mu.Unlock()

		if st >= 400 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(st)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error": map[string]any{"message": "simulated public models error", "code": st},
			})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": models})

	case r.Method == http.MethodPost && (path == "/api/v1/responses" || path == "/responses"):
		m.handleResponses(w, r)

	default:
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "not found: " + path})
	}
}

func extractPromptFromResponsesInput(raw map[string]any) string {
	var parts []string
	switch inp := raw["input"].(type) {
	case string:
		parts = append(parts, inp)
	case []any:
		for _, elem := range inp {
			if em, ok := elem.(map[string]any); ok {
				switch c := em["content"].(type) {
				case string:
					parts = append(parts, c)
				case []any:
					for _, blk := range c {
						if bm, ok := blk.(map[string]any); ok {
							if txt, ok := bm["text"].(string); ok {
								parts = append(parts, txt)
							}
						}
					}
				}
			}
		}
	}
	return strings.Join(parts, "\n")
}

func (m *MockOpenRouterServer) handleResponses(w http.ResponseWriter, r *http.Request) {
	bodyBytes, _ := io.ReadAll(io.LimitReader(r.Body, 8*1024*1024))
	var raw map[string]any
	_ = json.Unmarshal(bodyBytes, &raw)

	model, _ := raw["model"].(string)
	maxOut := 0
	switch v := raw["max_output_tokens"].(type) {
	case float64:
		maxOut = int(v)
	case int:
		maxOut = v
	}
	if maxOut == 0 {
		if v, ok := raw["max_tokens"].(float64); ok {
			maxOut = int(v)
		}
	}
	stream, _ := raw["stream"].(bool)
	var tools []map[string]any
	if rawTools, ok := raw["tools"].([]any); ok {
		for _, rt := range rawTools {
			if tm, ok := rt.(map[string]any); ok {
				tools = append(tools, tm)
			}
		}
	}
	promptTxt := extractPromptFromResponsesInput(raw)

	rec := RecordedResponsesRequest{
		Timestamp:       time.Now(),
		Path:            r.URL.Path,
		AuthHeader:      r.Header.Get("Authorization"),
		Model:           model,
		MaxOutputTokens: maxOut,
		Stream:          stream,
		Tools:           tools,
		PromptText:      promptTxt,
		RawBody:         raw,
	}

	m.mu.Lock()
	m.responsesReqs = append(m.responsesReqs, rec)
	forceSt := m.forceRespStatus
	forceBody := m.forceRespBody
	m.mu.Unlock()

	if forceSt >= 400 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(forceSt)
		_, _ = w.Write([]byte(forceBody))
		return
	}

	if strings.Contains(promptTxt, "[bridge-401]") {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{
				"message": "Invalid OpenRouter API key",
				"code":    401,
			},
		})
		return
	}

	if strings.Contains(promptTxt, "[bridge-402-unaffordable]") {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusPaymentRequired)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{
				"message": fmt.Sprintf("This request requires more credits, or fewer max_tokens. You requested up to %d tokens, but can only afford 120.", maxOut),
				"code":    402,
			},
		})
		return
	}

	if strings.Contains(promptTxt, "[bridge-402]") {
		afford := 1500
		if sub := affordOverrideRe.FindStringSubmatch(promptTxt); len(sub) == 2 {
			if n, err := strconv.Atoi(sub[1]); err == nil && n > 0 {
				afford = n
			}
		}
		if maxOut == 0 || maxOut > afford {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusPaymentRequired)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error": map[string]any{
					"message": fmt.Sprintf("This request requires more credits, or fewer max_tokens. You requested up to %d tokens, but can only afford %d.", maxOut, afford),
					"code":    402,
				},
			})
			return
		}
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)

	writeSSE := func(evName string, payload any) {
		b, _ := json.Marshal(payload)
		_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", evName, string(b))
		if flusher != nil {
			flusher.Flush()
		}
	}

	writeSSE("response.created", map[string]any{
		"type": "response.created",
		"response": map[string]any{
			"id":     "resp_or_mock_1",
			"status": "in_progress",
			"model":  model,
		},
	})

	var outputItems []map[string]any

	if strings.Contains(promptTxt, "[bridge-patch]") {
		incomingPatchType := ""
		for _, t := range tools {
			if name, _ := t["name"].(string); name == "apply_patch" {
				incomingPatchType, _ = t["type"].(string)
				break
			}
		}
		if incomingPatchType == "function" {
			patchContent := "*** Begin Patch\n*** Add File: bridged_patch.txt\n+patched via openrouter bridge\n*** End Patch"
			if strings.Contains(promptTxt, "[literal-plus]") {
				patchContent = strings.Join([]string{
					"*** Begin Patch",
					"*** Add File: notes.md",
					"# Heading",
					"",
					"+ literal bullet",
					"plain line",
					"*** Add File: cmd/app/main.go",
					"+package main",
					"",
					"+func main() {",
					"+\tprintln(\"ok\")",
					"+}",
					"*** End Patch",
				}, "\n")
			}
			patchArgsObj := map[string]string{
				"patch": patchContent,
			}
			argsJSON, _ := json.Marshal(patchArgsObj)
			fcItem := map[string]any{
				"id":        "fc_or_1",
				"type":      "function_call",
				"call_id":   "call_patch_or_1",
				"name":      "apply_patch",
				"status":    "completed",
				"arguments": string(argsJSON),
			}
			writeSSE("response.output_item.added", map[string]any{
				"type":         "response.output_item.added",
				"output_index": 0,
				"item": map[string]any{
					"id":        "fc_or_1",
					"type":      "function_call",
					"call_id":   "call_patch_or_1",
					"name":      "apply_patch",
					"status":    "in_progress",
					"arguments": "",
				},
			})
			writeSSE("response.function_call_arguments.delta", map[string]any{
				"type":         "response.function_call_arguments.delta",
				"item_id":      "fc_or_1",
				"output_index": 0,
				"delta":        string(argsJSON),
			})
			writeSSE("response.output_item.done", map[string]any{
				"type":         "response.output_item.done",
				"output_index": 0,
				"item":         fcItem,
			})
			outputItems = append(outputItems, fcItem)
		} else {
			patchRaw := "*** Begin Patch\n*** Add File: openai_patch.txt\n+native custom patch\n*** End Patch"
			ctItem := map[string]any{
				"id":      "ct_or_1",
				"type":    "custom_tool_call",
				"call_id": "call_patch_or_1",
				"name":    "apply_patch",
				"status":  "completed",
				"input":   patchRaw,
			}
			writeSSE("response.output_item.added", map[string]any{
				"type":         "response.output_item.added",
				"output_index": 0,
				"item":         ctItem,
			})
			writeSSE("response.output_item.done", map[string]any{
				"type":         "response.output_item.done",
				"output_index": 0,
				"item":         ctItem,
			})
			outputItems = append(outputItems, ctItem)
		}
	}

	replyText := fmt.Sprintf("OpenRouter mock response for model=%s max_output_tokens=%d", model, maxOut)
	writeSSE("response.output_text.delta", map[string]any{
		"type":  "response.output_text.delta",
		"delta": replyText,
	})
	msgOut := map[string]any{
		"id":     "msg_or_1",
		"type":   "message",
		"role":   "assistant",
		"status": "completed",
		"content": []map[string]any{
			{"type": "output_text", "text": replyText},
		},
	}
	outputItems = append(outputItems, msgOut)

	writeSSE("response.completed", map[string]any{
		"type": "response.completed",
		"response": map[string]any{
			"id":     "resp_or_mock_1",
			"status": "completed",
			"model":  model,
			"output": outputItems,
			"usage": map[string]any{
				"input_tokens":  120,
				"output_tokens": 28,
				"total_tokens":  148,
			},
		},
	})
	_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	if flusher != nil {
		flusher.Flush()
	}
}
