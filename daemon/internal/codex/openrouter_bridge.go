package codex

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	DefaultOpenRouterBaseURL = "https://openrouter.ai/api/v1"
	DefaultMaxOutputTokens   = 8192
	DefaultAffordCapTTL      = 5 * time.Minute
)

var (
	canOnlyAffordRe     = regexp.MustCompile(`(?i)can only afford\s+(\d+)`)
	unifiedHunkHeaderRe = regexp.MustCompile(`^@@\s*-(\d+)(?:,\d+)?\s+\+(\d+)(?:,\d+)?\s*@@(?:\s?(.*))?$`)
)

// Bridge is an in-process loopback HTTP/SSE proxy that keeps the real
// OpenRouter API key strictly in Go daemon memory, normalizes Codex
// /v1/responses requests, caps max_output_tokens with automatic 402 retry,
// and translates custom:apply_patch <-> function:apply_patch for third-party models.
type Bridge struct {
	mu           sync.RWMutex
	apiKey       string
	upstreamURL  string
	maxTokens    int
	affordCap    int
	affordCapExp time.Time

	bridgeToken string
	ln          net.Listener
	srv         *http.Server
	client      *http.Client
}

// DefaultMaxTokensFromEnv reads CODEX_OPENROUTER_MAX_OUTPUT_TOKENS (default 8192).
func DefaultMaxTokensFromEnv() int {
	if raw := strings.TrimSpace(os.Getenv("CODEX_OPENROUTER_MAX_OUTPUT_TOKENS")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			return n
		}
	}
	return DefaultMaxOutputTokens
}

// NewBridge starts a loopback 127.0.0.1:0 OpenRouter proxy.
func NewBridge(apiKey, upstreamURL string, maxOutputTokens int) (*Bridge, error) {
	if strings.TrimSpace(upstreamURL) == "" {
		upstreamURL = DefaultOpenRouterBaseURL
	}
	if maxOutputTokens <= 0 {
		maxOutputTokens = DefaultMaxTokensFromEnv()
	}
	tokBytes := make([]byte, 32)
	if _, err := rand.Read(tokBytes); err != nil {
		return nil, fmt.Errorf("bridge token: %w", err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("bridge listen: %w", err)
	}
	b := &Bridge{
		apiKey:      strings.TrimSpace(apiKey),
		upstreamURL: strings.TrimRight(strings.TrimSpace(upstreamURL), "/"),
		maxTokens:   maxOutputTokens,
		bridgeToken: hex.EncodeToString(tokBytes),
		ln:          ln,
		client: &http.Client{
			Transport: &http.Transport{
				Proxy:                 http.ProxyFromEnvironment,
				DialContext:           (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
				ForceAttemptHTTP2:     true,
				MaxIdleConns:          32,
				IdleConnTimeout:       90 * time.Second,
				TLSHandshakeTimeout:   15 * time.Second,
				ResponseHeaderTimeout: 5 * time.Minute,
			},
		},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", b.serveHTTP)
	b.srv = &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 30 * time.Second,
	}
	go func() {
		_ = b.srv.Serve(ln)
	}()
	return b, nil
}

// BaseURL returns the loopback OpenAI-compatible /v1 base URL for Codex.
func (b *Bridge) BaseURL() string {
	return "http://" + b.ln.Addr().String() + "/v1"
}

// BridgeToken returns the random per-daemon bearer token passed to child codex processes.
func (b *Bridge) BridgeToken() string { return b.bridgeToken }

// SetAPIKey updates the in-memory OpenRouter API key without restarting the bridge.
func (b *Bridge) SetAPIKey(key string) {
	trimmed := strings.TrimSpace(key)
	b.mu.Lock()
	if b.apiKey != trimmed {
		b.apiKey = trimmed
		b.affordCap = 0
		b.affordCapExp = time.Time{}
	}
	b.mu.Unlock()
}

// HasAPIKey reports whether a non-empty OpenRouter key is currently loaded in memory.
func (b *Bridge) HasAPIKey() bool {
	b.mu.RLock()
	ok := b.apiKey != ""
	b.mu.RUnlock()
	return ok
}

// SetUpstreamURL overrides the upstream base URL (used in hermetic tests).
func (b *Bridge) SetUpstreamURL(u string) {
	trimmed := strings.TrimRight(strings.TrimSpace(u), "/")
	b.mu.Lock()
	if trimmed != "" && b.upstreamURL != trimmed {
		b.upstreamURL = trimmed
		b.affordCap = 0
		b.affordCapExp = time.Time{}
	}
	b.mu.Unlock()
}

// SetMaxOutputTokens overrides the max_output_tokens cap.
func (b *Bridge) SetMaxOutputTokens(n int) {
	if n <= 0 {
		n = DefaultMaxTokensFromEnv()
	}
	b.mu.Lock()
	if n > 0 && b.maxTokens != n {
		b.maxTokens = n
		b.affordCap = 0
		b.affordCapExp = time.Time{}
	}
	b.mu.Unlock()
}

func (b *Bridge) effectiveMaxTokens(baseMax int) int {
	b.mu.RLock()
	capTok := b.affordCap
	exp := b.affordCapExp
	b.mu.RUnlock()
	if capTok > 0 && time.Now().Before(exp) && capTok < baseMax {
		return capTok
	}
	return baseMax
}

func (b *Bridge) recordAffordCap(capTok int) {
	if capTok <= 0 {
		return
	}
	b.mu.Lock()
	b.affordCap = capTok
	b.affordCapExp = time.Now().Add(DefaultAffordCapTTL)
	b.mu.Unlock()
}

// Close shuts down the loopback listener.
func (b *Bridge) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return b.srv.Shutdown(ctx)
}

func (b *Bridge) checkAuth(r *http.Request) bool {
	got := strings.TrimSpace(r.Header.Get("Authorization"))
	want := "Bearer " + b.bridgeToken
	if len(got) != len(want) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

func (b *Bridge) snapshotConfig() (apiKey, upstream string, maxTokens int) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.apiKey, b.upstreamURL, b.maxTokens
}

func writeBridgeErr(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{"message": msg},
	})
}

func (b *Bridge) serveHTTP(w http.ResponseWriter, r *http.Request) {
	if !b.checkAuth(r) {
		writeBridgeErr(w, http.StatusUnauthorized, "invalid bridge bearer token")
		return
	}
	apiKey, upstream, maxTokens := b.snapshotConfig()
	if apiKey == "" {
		writeBridgeErr(w, http.StatusUnauthorized, "OpenRouter API key is not configured in agentc")
		return
	}
	// Re-read env override dynamically if set so tests using t.Setenv work seamlessly.
	if raw := strings.TrimSpace(os.Getenv("CODEX_OPENROUTER_MAX_OUTPUT_TOKENS")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			maxTokens = n
		}
	}

	relPath := strings.TrimPrefix(r.URL.Path, "/v1")
	if !strings.HasPrefix(relPath, "/") {
		relPath = "/" + relPath
	}

	if r.Method == http.MethodPost && relPath == "/responses" {
		b.handleResponses(w, r, apiKey, upstream, maxTokens)
		return
	}
	b.proxyPassThrough(w, r, relPath, apiKey, upstream)
}

func (b *Bridge) proxyPassThrough(w http.ResponseWriter, r *http.Request, relPath, apiKey, upstream string) {
	target := upstream + relPath
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}
	var bodyReader io.Reader
	if r.Body != nil {
		defer r.Body.Close()
		raw, err := io.ReadAll(io.LimitReader(r.Body, 32<<20))
		if err != nil {
			writeBridgeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		bodyReader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(r.Context(), r.Method, target, bodyReader)
	if err != nil {
		writeBridgeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	for k, vv := range r.Header {
		if strings.EqualFold(k, "Authorization") || strings.EqualFold(k, "Host") || strings.EqualFold(k, "Content-Length") {
			continue
		}
		for _, v := range vv {
			req.Header.Add(k, v)
		}
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("HTTP-Referer", "https://github.com/Sidd065/agent-companion")
	req.Header.Set("X-Title", "agentc")

	resp, err := b.client.Do(req)
	if err != nil {
		writeBridgeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	defer resp.Body.Close()
	copyResponseHeaders(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

func (b *Bridge) handleResponses(w http.ResponseWriter, r *http.Request, apiKey, upstream string, maxTokens int) {
	defer r.Body.Close()
	rawReq, err := io.ReadAll(io.LimitReader(r.Body, 64<<20))
	if err != nil {
		writeBridgeErr(w, http.StatusBadRequest, "read request body: "+err.Error())
		return
	}
	var body map[string]any
	if err := json.Unmarshal(rawReq, &body); err != nil {
		writeBridgeErr(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}

	model, _ := body["model"].(string)
	model = NormalizeOpenRouterSlug(model)
	body["model"] = model

	delete(body, "client_metadata")
	delete(body, "prompt_cache_key")

	nonOpenAI := !strings.HasPrefix(strings.ToLower(model), "openai/")
	if nonOpenAI {
		stripEncryptedReasoningInclude(body)
	}
	flattenNamespaceTools(body)

	effMax := b.effectiveMaxTokens(maxTokens)
	curMax, ok := asJSONInt(body["max_output_tokens"])
	if !ok || curMax <= 0 || curMax > effMax {
		curMax = effMax
		body["max_output_tokens"] = curMax
	}

	if nonOpenAI {
		rewriteApplyPatchInRequest(body)
	}

	isStream, _ := body["stream"].(bool)
	resp, errBody, err := b.postResponses(r.Context(), upstream+"/responses", apiKey, body, isStream)
	if err != nil {
		writeBridgeErr(w, http.StatusBadGateway, err.Error())
		return
	}

	// Fallback if an openai/* model on OpenRouter rejected custom tool type (400).
	if !nonOpenAI && resp.StatusCode == http.StatusBadRequest && bytes.Contains(bytes.ToLower(errBody), []byte("custom")) {
		_ = resp.Body.Close()
		if rewriteApplyPatchInRequest(body) {
			resp, errBody, err = b.postResponses(r.Context(), upstream+"/responses", apiKey, body, isStream)
			if err != nil {
				writeBridgeErr(w, http.StatusBadGateway, err.Error())
				return
			}
		}
	}

	// Cross-model / cross-endpoint mid-thread switch: if OpenRouter rejects historical
	// provider-encrypted reasoning or compaction items (HTTP 404/400 mentioning "encrypted"),
	// strip those encrypted items from input[] and retry once cleanly.
	if (resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusBadRequest) &&
		bytes.Contains(bytes.ToLower(errBody), []byte("encrypted")) {
		if stripEncryptedItemsFromInput(body) {
			_ = resp.Body.Close()
			resp, errBody, err = b.postResponses(r.Context(), upstream+"/responses", apiKey, body, isStream)
			if err != nil {
				writeBridgeErr(w, http.StatusBadGateway, err.Error())
				return
			}
		}
	}

	// HTTP 402 "$0-credit BYOK preflight" retry: parse "can only afford <N>".
	if resp.StatusCode == http.StatusPaymentRequired {
		if m := canOnlyAffordRe.FindSubmatch(errBody); len(m) == 2 {
			if afford, perr := strconv.Atoi(string(m[1])); perr == nil && afford >= 256 {
				retryMax := curMax
				if afford-64 < retryMax {
					retryMax = afford - 64
				}
				if retryMax > 0 && retryMax < curMax {
					_ = resp.Body.Close()
					body["max_output_tokens"] = retryMax
					resp, errBody, err = b.postResponses(r.Context(), upstream+"/responses", apiKey, body, isStream)
					if err != nil {
						writeBridgeErr(w, http.StatusBadGateway, err.Error())
						return
					}
					if resp.StatusCode == http.StatusOK {
						b.recordAffordCap(retryMax)
					}
				}
			}
		}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		copyResponseHeaders(w.Header(), resp.Header)
		w.WriteHeader(resp.StatusCode)
		_, _ = w.Write(errBody)
		return
	}

	ct := strings.ToLower(resp.Header.Get("Content-Type"))
	if strings.Contains(ct, "text/event-stream") {
		copyResponseHeaders(w.Header(), resp.Header)
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
		streamResponsesSSE(w, resp.Body)
		return
	}

	// Non-streaming JSON response: rewrite any function_call(apply_patch) in output[].
	rawResp, err := io.ReadAll(resp.Body)
	if err != nil {
		writeBridgeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	rewritten := rewriteNonStreamResponseJSON(rawResp)
	copyResponseHeaders(w.Header(), resp.Header)
	w.Header().Del("Content-Length")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(rewritten)
}

func (b *Bridge) postResponses(ctx context.Context, url, apiKey string, body map[string]any, stream bool) (*http.Response, []byte, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")
	if stream {
		req.Header.Set("Accept", "text/event-stream")
	}
	req.Header.Set("HTTP-Referer", "https://github.com/Sidd065/agent-companion")
	req.Header.Set("X-Title", "agentc")

	resp, err := b.client.Do(req)
	if err != nil {
		return nil, nil, err
	}
	if resp.StatusCode != http.StatusOK {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
		return resp, errBody, nil
	}
	return resp, nil, nil
}

func copyResponseHeaders(dst, src http.Header) {
	for k, vv := range src {
		if strings.EqualFold(k, "Content-Length") || strings.EqualFold(k, "Transfer-Encoding") || strings.EqualFold(k, "Connection") {
			continue
		}
		for _, v := range vv {
			dst.Add(k, v)
		}
	}
}

func stripEncryptedItemsFromInput(body map[string]any) bool {
	stripEncryptedReasoningInclude(body)
	inp, ok := body["input"].([]any)
	if !ok || len(inp) == 0 {
		return false
	}
	changed := false
	kept := make([]any, 0, len(inp))
	for _, raw := range inp {
		m, ok := raw.(map[string]any)
		if !ok {
			kept = append(kept, raw)
			continue
		}
		t, _ := m["type"].(string)
		_, hasEnc := m["encrypted_content"]
		if t == "compaction" || t == "compaction_summary" || (t == "reasoning" && hasEnc) {
			changed = true
			continue
		}
		if hasEnc {
			cp := make(map[string]any, len(m))
			for k, v := range m {
				if k == "encrypted_content" {
					continue
				}
				cp[k] = v
			}
			kept = append(kept, cp)
			changed = true
			continue
		}
		kept = append(kept, raw)
	}
	if changed {
		body["input"] = kept
	}
	return changed
}

func stripEncryptedReasoningInclude(body map[string]any) {
	inc, ok := body["include"].([]any)
	if !ok {
		return
	}
	var kept []any
	for _, item := range inc {
		if s, ok := item.(string); ok && s == "reasoning.encrypted_content" {
			continue
		}
		kept = append(kept, item)
	}
	if len(kept) == 0 {
		delete(body, "include")
	} else {
		body["include"] = kept
	}
}

func flattenNamespaceTools(body map[string]any) {
	tools, ok := body["tools"].([]any)
	if !ok || len(tools) == 0 {
		return
	}
	var out []any
	changed := false
	for _, entry := range tools {
		tm, ok := entry.(map[string]any)
		if !ok {
			out = append(out, entry)
			continue
		}
		if t, _ := tm["type"].(string); t == "namespace" {
			changed = true
			if inner, ok := tm["tools"].([]any); ok {
				out = append(out, inner...)
			}
			continue
		}
		out = append(out, entry)
	}
	if changed {
		body["tools"] = out
	}
}

const applyPatchToolDescription = `Use the apply_patch function tool to create, edit, move, or delete files. Pass the V4A patch string in the JSON "patch" argument.

V4A patch format rules (strict — NOT unified git diff):
1. Must start with "*** Begin Patch" on line 1 and end with "*** End Patch" on the last line.
2. Each file section starts with EXACTLY ONE header:
   - "*** Add File: <path>" — create a new file; EVERY content line MUST start with "+".
   - "*** Delete File: <path>" — delete an existing file (no body lines follow).
   - "*** Update File: <path>" — edit an existing file (optionally followed by "*** Move to: <new_path>").
3. Inside "*** Update File: <path>", include one or more hunks:
   - Start each hunk with bare "@@" (preferred) or "@@ <exact_enclosing_line>" (only to disambiguate duplicate blocks; do NOT repeat that anchor line as the first hunk line).
   - NEVER use unified-diff line numbers like "@@ -1,4 +1,5 @@", and NEVER include "diff --git", "--- a/...", or "+++ b/..." headers.
   - Prefix EVERY line inside a hunk with EXACTLY ONE character: " " (single space) for unchanged context, "-" for removed lines, or "+" for added lines.

Example:
*** Begin Patch
*** Update File: src/calc.py
@@
 def add(a, b):
-    return a - b
+    return a + b
*** Add File: src/version.py
+VERSION = "1.0.0"
*** Delete File: src/obsolete.py
*** End Patch`

const applyPatchParamDescription = "Complete V4A patch starting with '*** Begin Patch' and ending with '*** End Patch'. Use '*** Add File: <path>' ('+' lines), '*** Delete File: <path>', or '*** Update File: <path>' with bare '@@' hunks where every hunk line starts with ' ' (context), '-' (remove), or '+' (add). Never include '---'/'+++' headers or '@@ -a,b +c,d @@' line numbers."

// rewriteApplyPatchInRequest translates Codex's custom Lark-grammar apply_patch
// tool and historical custom_tool_call* items into standard function tool definitions.
func rewriteApplyPatchInRequest(body map[string]any) bool {
	rewrote := false
	if tools, ok := body["tools"].([]any); ok {
		for i, entry := range tools {
			tm, ok := entry.(map[string]any)
			if !ok {
				continue
			}
			t, _ := tm["type"].(string)
			name, _ := tm["name"].(string)
			if t == "custom" && name == "apply_patch" {
				tools[i] = map[string]any{
					"type":        "function",
					"name":        "apply_patch",
					"description": applyPatchToolDescription,
					"parameters": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"patch": map[string]any{
								"type":        "string",
								"description": applyPatchParamDescription,
							},
						},
						"required": []string{"patch"},
					},
				}
				rewrote = true
			}
		}
	}

	if inputs, ok := body["input"].([]any); ok {
		patchCallIDs := map[string]bool{}
		for i, entry := range inputs {
			im, ok := entry.(map[string]any)
			if !ok {
				continue
			}
			switch typ, _ := im["type"].(string); typ {
			case "custom_tool_call":
				name, _ := im["name"].(string)
				if name != "apply_patch" {
					continue
				}
				callID, _ := im["call_id"].(string)
				if callID != "" {
					patchCallIDs[callID] = true
				}
				origID, _ := im["id"].(string)
				fcID := origID
				if strings.HasPrefix(fcID, "ctc_") {
					fcID = "fc_" + strings.TrimPrefix(fcID, "ctc_")
				} else if fcID != "" && !strings.HasPrefix(fcID, "fc_") {
					fcID = "fc_" + fcID
				}
				patchStr, _ := im["input"].(string)
				argsBytes, _ := json.Marshal(map[string]string{"patch": patchStr})
				fnItem := map[string]any{
					"type":      "function_call",
					"call_id":   callID,
					"name":      "apply_patch",
					"status":    firstNonEmpty(asString(im["status"]), "completed"),
					"arguments": string(argsBytes),
				}
				if fcID != "" {
					fnItem["id"] = fcID
				}
				inputs[i] = fnItem
				rewrote = true
			case "custom_tool_call_output":
				callID, _ := im["call_id"].(string)
				if callID != "" || patchCallIDs[callID] {
					inputs[i] = map[string]any{
						"type":    "function_call_output",
						"call_id": callID,
						"output":  asString(im["output"]),
					}
					rewrote = true
				}
			}
		}
	}
	return rewrote
}

// extractPatchInput extracts V4A patch text from JSON function arguments
// and normalizes common non-OpenAI LLM unified-diff/markdown quirks into valid V4A.
func extractPatchInput(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(trimmed), &m); err == nil && m != nil {
		var hintPath string
		for _, pk := range []string{"path", "file", "file_path", "filename", "filepath"} {
			if pv, ok := m[pk].(string); ok && strings.TrimSpace(pv) != "" {
				hintPath = strings.TrimSpace(pv)
				break
			}
		}
		for _, k := range []string{"patch", "input", "diff", "content", "changes"} {
			if v, ok := m[k].(string); ok && v != "" {
				return normalizeV4APatchWithHint(v, hintPath)
			}
		}
		if len(m) == 1 {
			for _, v := range m {
				if s, ok := v.(string); ok {
					return normalizeV4APatchWithHint(s, "")
				}
			}
		}
	}
	var s string
	if err := json.Unmarshal([]byte(trimmed), &s); err == nil && s != "" {
		return normalizeV4APatchWithHint(s, "")
	}
	return normalizeV4APatchWithHint(raw, "")
}

func normalizeV4APatch(raw string) string {
	return normalizeV4APatchWithHint(raw, "")
}

func normalizeV4APatchWithHint(raw, hintPath string) string {
	if strings.TrimSpace(raw) == "" {
		return ""
	}
	s := strings.ReplaceAll(raw, "\r\n", "\n")
	lines := strings.Split(s, "\n")

	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	for len(lines) > 0 && strings.TrimSpace(lines[0]) == "" {
		lines = lines[1:]
	}
	if len(lines) == 0 {
		return ""
	}

	if !looksLikePatchLine(lines[0]) && !strings.HasPrefix(strings.TrimSpace(lines[0]), "```") {
		for idx, l := range lines {
			tl := strings.TrimSpace(l)
			if tl == "*** Begin Patch" || strings.HasPrefix(tl, "```") {
				lines = lines[idx:]
				break
			}
		}
	}

	if len(lines) > 0 && strings.HasPrefix(strings.TrimSpace(lines[0]), "```") {
		lines = lines[1:]
		for idx := len(lines) - 1; idx >= 0; idx-- {
			tl := strings.TrimSpace(lines[idx])
			if tl == "" {
				continue
			}
			if tl == "```" {
				lines = lines[:idx]
			}
			break
		}
		for len(lines) > 0 && lines[len(lines)-1] == "" {
			lines = lines[:len(lines)-1]
		}
		for len(lines) > 0 && strings.TrimSpace(lines[0]) == "" {
			lines = lines[1:]
		}
	}
	if len(lines) == 0 {
		return ""
	}

	const (
		secNone = iota
		secAdd
		secDelete
		secUpdate
	)

	sec := secNone
	inHunk := false
	sawMove := false
	deleteFromUnified := false
	var addBuf []string
	var addFromUnified bool

	out := []string{"*** Begin Patch"}

	flushAdd := func() {
		if sec != secAdd {
			return
		}
		for len(addBuf) > 0 && addBuf[len(addBuf)-1] == "" {
			addBuf = addBuf[:len(addBuf)-1]
		}
		hasNonEmpty := false
		allNonEmptyPlus := true
		for _, l := range addBuf {
			if l == "" {
				continue
			}
			hasNonEmpty = true
			if !strings.HasPrefix(l, "+") {
				allNonEmptyPlus = false
				break
			}
		}
		alreadyPrefixed := addFromUnified || (hasNonEmpty && allNonEmptyPlus)
		for _, l := range addBuf {
			if alreadyPrefixed && strings.HasPrefix(l, "+") {
				out = append(out, l)
			} else {
				out = append(out, "+"+l)
			}
		}
		addBuf = nil
		addFromUnified = false
	}

	isSectionBoundary := func(idx int) bool {
		if idx >= len(lines) {
			return true
		}
		l := lines[idx]
		tl := strings.TrimSpace(l)
		if tl == "*** Begin Patch" || tl == "*** End Patch" {
			return true
		}
		if strings.HasPrefix(l, "*** Add File:") || strings.HasPrefix(l, "*** Delete File:") || strings.HasPrefix(l, "*** Update File:") {
			return true
		}
		if strings.HasPrefix(l, "diff --git ") && isGitDiffHeaderAhead(lines, idx) {
			return true
		}
		if isUnifiedHeaderPair(lines, idx) {
			return true
		}
		return false
	}

	allEmptyUntilBoundary := func(start int) bool {
		for j := start; j < len(lines); j++ {
			if isSectionBoundary(j) {
				return true
			}
			if lines[j] != "" {
				return false
			}
		}
		return true
	}

	nextHunkLineTrimmed := func(start int) string {
		for j := start; j < len(lines); j++ {
			if isSectionBoundary(j) {
				return ""
			}
			lj := lines[j]
			if strings.HasPrefix(lj, "@@") || strings.TrimSpace(lj) == "*** End of File" {
				return ""
			}
			if strings.HasPrefix(lj, `\ No newline at end of file`) {
				continue
			}
			if len(lj) > 0 && (lj[0] == ' ' || lj[0] == '-' || lj[0] == '+') {
				lj = lj[1:]
			}
			t := strings.TrimSpace(lj)
			if t != "" {
				return t
			}
		}
		return ""
	}

	sawExplicitBegin := false
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)

		if trimmed == "*** Begin Patch" {
			sawExplicitBegin = true
			continue
		}
		if trimmed == "*** End Patch" {
			flushAdd()
			sec = secNone
			break
		}
		if strings.HasPrefix(trimmed, "*** Environment ID:") && len(out) == 1 {
			out = append(out, trimmed)
			continue
		}

		if strings.HasPrefix(line, "*** Add File:") {
			flushAdd()
			p := strings.TrimSpace(strings.TrimPrefix(line, "*** Add File:"))
			out = append(out, "*** Add File: "+p)
			sec = secAdd
			inHunk = false
			sawMove = false
			deleteFromUnified = false
			continue
		}
		if strings.HasPrefix(line, "*** Delete File:") {
			flushAdd()
			p := strings.TrimSpace(strings.TrimPrefix(line, "*** Delete File:"))
			out = append(out, "*** Delete File: "+p)
			sec = secDelete
			inHunk = false
			sawMove = false
			deleteFromUnified = false
			continue
		}
		if strings.HasPrefix(line, "*** Update File:") {
			flushAdd()
			p := strings.TrimSpace(strings.TrimPrefix(line, "*** Update File:"))
			out = append(out, "*** Update File: "+p)
			sec = secUpdate
			inHunk = false
			sawMove = false
			deleteFromUnified = false
			continue
		}

		if (strings.HasPrefix(line, "diff --git ") && isGitDiffHeaderAhead(lines, i)) || isUnifiedHeaderPair(lines, i) {
			oldP, newP, isNew, isDel, nextIdx := consumeUnifiedFileHeaders(lines, i)
			if (sec == secUpdate && !inHunk) || (sec == secAdd && len(addBuf) == 0) {
				if sec == secUpdate && !sawMove && newP != "" && newP != "/dev/null" && oldP != "" && oldP != "/dev/null" && newP != oldP {
					out = append(out, "*** Move to: "+newP)
					sawMove = true
				}
				i = nextIdx - 1
				continue
			}
			flushAdd()
			inHunk = false
			sawMove = false
			if isDel || newP == "/dev/null" {
				target := oldP
				if target != "" && target != "/dev/null" {
					out = append(out, "*** Delete File: "+target)
				}
				sec = secDelete
				deleteFromUnified = true
			} else if isNew || oldP == "/dev/null" {
				target := newP
				if target != "" && target != "/dev/null" {
					out = append(out, "*** Add File: "+target)
				}
				sec = secAdd
				addFromUnified = true
				deleteFromUnified = false
			} else {
				target := firstNonEmpty(oldP, newP)
				if target != "" {
					out = append(out, "*** Update File: "+target)
					if newP != "" && oldP != "" && newP != oldP {
						out = append(out, "*** Move to: "+newP)
						sawMove = true
					}
					sec = secUpdate
				}
				deleteFromUnified = false
			}
			i = nextIdx - 1
			continue
		}

		switch sec {
		case secNone:
			if trimmed == "" {
				continue
			}
			if hintPath != "" && (strings.HasPrefix(line, "@@") || line[0] == '-' || line[0] == '+' || line[0] == ' ') {
				out = append(out, "*** Update File: "+hintPath)
				sec = secUpdate
				inHunk = false
				sawMove = false
				i--
				continue
			}
			if !sawExplicitBegin && len(out) == 1 {
				return raw
			}

		case secDelete:
			if trimmed == "" || deleteFromUnified {
				continue
			}

		case secAdd:
			if trimmed == "*** End of File" || strings.HasPrefix(line, `\ No newline at end of file`) {
				continue
			}
			if unifiedHunkHeaderRe.MatchString(line) {
				continue
			}
			if len(addBuf) == 0 && (trimmed == "@@" || strings.HasPrefix(line, "--- ") || strings.HasPrefix(line, "+++ ")) {
				continue
			}
			addBuf = append(addBuf, line)

		case secUpdate:
			if !inHunk && !sawMove && strings.HasPrefix(line, "*** Move to:") {
				dst := strings.TrimSpace(strings.TrimPrefix(line, "*** Move to:"))
				out = append(out, "*** Move to: "+dst)
				sawMove = true
				continue
			}
			if strings.HasPrefix(line, `\ No newline at end of file`) {
				continue
			}
			if !inHunk {
				if line == "" {
					continue
				}
				if isGitMetaLine(line) {
					if strings.HasPrefix(line, "rename to ") && !sawMove {
						if dst := cleanUnifiedPath(strings.TrimPrefix(line, "rename to "), ""); dst != "" {
							out = append(out, "*** Move to: "+dst)
							sawMove = true
						}
					}
					continue
				}
				if strings.HasPrefix(line, "--- ") || strings.HasPrefix(line, "+++ ") {
					continue
				}
			}

			if strings.HasPrefix(line, "@@") {
				if m := unifiedHunkHeaderRe.FindStringSubmatch(line); len(m) == 4 {
					anchor := strings.TrimSpace(m[3])
					if anchor == "" || anchor == nextHunkLineTrimmed(i+1) {
						out = append(out, "@@")
					} else {
						out = append(out, "@@ "+anchor)
					}
				} else if trimmed == "@@" {
					out = append(out, "@@")
				} else {
					out = append(out, line)
				}
				inHunk = true
				continue
			}

			if trimmed == "*** End of File" {
				if inHunk {
					out = append(out, "*** End of File")
					inHunk = false
				}
				continue
			}

			if line == "" && allEmptyUntilBoundary(i) {
				continue
			}

			if !inHunk {
				out = append(out, "@@")
				inHunk = true
			}

			if line == "" {
				out = append(out, " ")
			} else if line[0] == ' ' || line[0] == '+' || line[0] == '-' {
				out = append(out, line)
			} else {
				out = append(out, " "+line)
			}
		}
	}

	flushAdd()
	if len(out) == 1 && !sawExplicitBegin {
		return raw
	}
	out = append(out, "*** End Patch")
	return strings.Join(out, "\n")
}

func looksLikePatchLine(l string) bool {
	tl := strings.TrimSpace(l)
	return strings.HasPrefix(tl, "*** ") ||
		strings.HasPrefix(l, "diff --git ") ||
		strings.HasPrefix(l, "--- ") ||
		strings.HasPrefix(l, "+++ ") ||
		strings.HasPrefix(l, "@@")
}

func isUnifiedHeaderPair(lines []string, i int) bool {
	if i+1 >= len(lines) {
		return false
	}
	l0, l1 := lines[i], lines[i+1]
	return (strings.HasPrefix(l0, "--- ") || strings.HasPrefix(l0, "---\t")) &&
		(strings.HasPrefix(l1, "+++ ") || strings.HasPrefix(l1, "+++\t"))
}

func isGitMetaLine(l string) bool {
	for _, pfx := range []string{
		"index ", "old mode ", "new mode ", "new file mode ", "deleted file mode ",
		"similarity index ", "dissimilarity index ", "rename from ", "rename to ",
		"copy from ", "copy to ",
	} {
		if strings.HasPrefix(l, pfx) {
			return true
		}
	}
	return false
}

func isGitDiffHeaderAhead(lines []string, i int) bool {
	if !strings.HasPrefix(lines[i], "diff --git ") {
		return false
	}
	for j := i + 1; j < len(lines) && j <= i+8; j++ {
		if isUnifiedHeaderPair(lines, j) || isGitMetaLine(lines[j]) {
			return true
		}
	}
	return false
}

func consumeUnifiedFileHeaders(lines []string, start int) (oldPath, newPath string, isNew, isDel bool, nextIdx int) {
	i := start
	if strings.HasPrefix(lines[i], "diff --git ") {
		rest := strings.TrimSpace(strings.TrimPrefix(lines[i], "diff --git "))
		if fields := strings.Fields(rest); len(fields) == 2 {
			oldPath = cleanUnifiedPath(fields[0], "a/")
			newPath = cleanUnifiedPath(fields[1], "b/")
		}
		i++
	}
	for i < len(lines) {
		l := lines[i]
		if strings.HasPrefix(l, "new file mode ") {
			isNew = true
			i++
			continue
		}
		if strings.HasPrefix(l, "deleted file mode ") {
			isDel = true
			i++
			continue
		}
		if strings.HasPrefix(l, "rename from ") {
			oldPath = cleanUnifiedPath(strings.TrimPrefix(l, "rename from "), "")
			i++
			continue
		}
		if strings.HasPrefix(l, "rename to ") {
			newPath = cleanUnifiedPath(strings.TrimPrefix(l, "rename to "), "")
			i++
			continue
		}
		if isGitMetaLine(l) {
			i++
			continue
		}
		if strings.HasPrefix(l, "--- ") || strings.HasPrefix(l, "---\t") {
			oldPath = cleanUnifiedPath(strings.TrimSpace(l[3:]), "a/")
			i++
			if i < len(lines) && (strings.HasPrefix(lines[i], "+++ ") || strings.HasPrefix(lines[i], "+++\t")) {
				newPath = cleanUnifiedPath(strings.TrimSpace(lines[i][3:]), "b/")
				i++
			}
			break
		}
		break
	}
	return oldPath, newPath, isNew, isDel, i
}

func cleanUnifiedPath(raw, stripPrefix string) string {
	p := strings.TrimSpace(raw)
	if idx := strings.IndexByte(p, '\t'); idx >= 0 {
		p = strings.TrimSpace(p[:idx])
	}
	if len(p) >= 2 && p[0] == '"' && p[len(p)-1] == '"' {
		var unq string
		if json.Unmarshal([]byte(p), &unq) == nil {
			p = unq
		}
	}
	if p == "/dev/null" || p == "dev/null" {
		return "/dev/null"
	}
	if stripPrefix != "" && strings.HasPrefix(p, stripPrefix) {
		return strings.TrimPrefix(p, stripPrefix)
	}
	return p
}

type patchCallState struct {
	outputIndex  int
	itemID       string
	callID       string
	buf          strings.Builder
	emittedAdded bool
	emittedInput bool
}

func streamResponsesSSE(w http.ResponseWriter, body io.Reader) {
	flusher, _ := w.(http.Flusher)
	flush := func() {
		if flusher != nil {
			flusher.Flush()
		}
	}
	br := bufio.NewReaderSize(body, 64*1024)
	byIdx := map[int]*patchCallState{}
	byID := map[string]*patchCallState{}

	var eventName string
	var dataLines []string
	var rawBlock []string

	processFrame := func() {
		if len(rawBlock) == 0 {
			return
		}
		defer func() {
			eventName = ""
			dataLines = dataLines[:0]
			rawBlock = rawBlock[:0]
		}()

		if len(dataLines) == 0 {
			for _, l := range rawBlock {
				_, _ = io.WriteString(w, l)
			}
			_, _ = io.WriteString(w, "\n")
			flush()
			return
		}
		dataPayload := strings.Join(dataLines, "\n")
		if strings.TrimSpace(dataPayload) == "[DONE]" {
			for _, l := range rawBlock {
				_, _ = io.WriteString(w, l)
			}
			_, _ = io.WriteString(w, "\n")
			flush()
			return
		}

		var ev map[string]any
		if err := json.Unmarshal([]byte(dataPayload), &ev); err != nil {
			for _, l := range rawBlock {
				_, _ = io.WriteString(w, l)
			}
			_, _ = io.WriteString(w, "\n")
			flush()
			return
		}

		evType := firstNonEmpty(asString(ev["type"]), eventName)
		outIdx, hasOutIdx := asJSONInt(ev["output_index"])
		itemID := asString(ev["item_id"])

		writeSSE := func(name string, obj map[string]any) {
			b, err := json.Marshal(obj)
			if err != nil {
				return
			}
			if name != "" {
				_, _ = fmt.Fprintf(w, "event: %s\n", name)
			}
			_, _ = fmt.Fprintf(w, "data: %s\n\n", string(b))
			flush()
		}

		findState := func() *patchCallState {
			if itemID != "" {
				if st, ok := byID[itemID]; ok {
					return st
				}
				// Explicit item_id belongs to a different output item (e.g. exec_command); do not match byIdx!
				return nil
			}
			if hasOutIdx {
				if st, ok := byIdx[outIdx]; ok {
					return st
				}
			}
			return nil
		}

		switch evType {
		case "response.output_item.added":
			if item, ok := ev["item"].(map[string]any); ok {
				if asString(item["type"]) == "function_call" && asString(item["name"]) == "apply_patch" {
					id := firstNonEmpty(asString(item["id"]), itemID, fmt.Sprintf("ctc_%d", outIdx))
					callID := firstNonEmpty(asString(item["call_id"]), id)
					st := &patchCallState{
						outputIndex:  outIdx,
						itemID:       id,
						callID:       callID,
						emittedAdded: true,
					}
					if hasOutIdx {
						byIdx[outIdx] = st
					}
					byID[id] = st
					if callID != "" {
						byID[callID] = st
					}
					if origID := asString(item["id"]); origID != "" {
						byID[origID] = st
					}
					ev["item"] = map[string]any{
						"type":    "custom_tool_call",
						"id":      id,
						"call_id": callID,
						"name":    "apply_patch",
						"status":  "in_progress",
						"input":   "",
					}
					writeSSE("response.output_item.added", ev)
					return
				}
			}

		case "response.function_call_arguments.delta":
			if st := findState(); st != nil {
				st.buf.WriteString(asString(ev["delta"]))
				return
			}

		case "response.function_call_arguments.done":
			if st := findState(); st != nil {
				rawArgs := firstNonEmpty(asString(ev["arguments"]), st.buf.String())
				patchText := extractPatchInput(rawArgs)
				st.emittedInput = true
				writeSSE("response.custom_tool_call_input.delta", map[string]any{
					"type":         "response.custom_tool_call_input.delta",
					"item_id":      st.itemID,
					"output_index": st.outputIndex,
					"delta":        patchText,
				})
				writeSSE("response.custom_tool_call_input.done", map[string]any{
					"type":         "response.custom_tool_call_input.done",
					"item_id":      st.itemID,
					"output_index": st.outputIndex,
					"input":        patchText,
				})
				return
			}

		case "response.output_item.done":
			item, _ := ev["item"].(map[string]any)
			isApplyPatchItem := item != nil && asString(item["type"]) == "function_call" && asString(item["name"]) == "apply_patch"
			isNonPatchItem := item != nil && (asString(item["type"]) != "function_call" || (asString(item["name"]) != "" && asString(item["name"]) != "apply_patch"))
			var st *patchCallState
			if !isNonPatchItem {
				st = findState()
				if st == nil && item != nil {
					if origID := asString(item["id"]); origID != "" {
						st = byID[origID]
					}
					if st == nil {
						if cid := asString(item["call_id"]); cid != "" {
							st = byID[cid]
						}
					}
				}
			}
			if isApplyPatchItem || st != nil {
				if st == nil {
					id := firstNonEmpty(asString(item["id"]), itemID, fmt.Sprintf("ctc_%d", outIdx))
					callID := firstNonEmpty(asString(item["call_id"]), id)
					st = &patchCallState{outputIndex: outIdx, itemID: id, callID: callID}
					byIdx[outIdx] = st
					byID[id] = st
				}
				var itemArgs string
				if item != nil {
					itemArgs = asString(item["arguments"])
				}
				patchText := extractPatchInput(firstNonEmpty(itemArgs, st.buf.String()))
				if !st.emittedAdded {
					st.emittedAdded = true
					writeSSE("response.output_item.added", map[string]any{
						"type":         "response.output_item.added",
						"output_index": st.outputIndex,
						"item": map[string]any{
							"type":    "custom_tool_call",
							"id":      st.itemID,
							"call_id": st.callID,
							"name":    "apply_patch",
							"status":  "in_progress",
							"input":   "",
						},
					})
				}
				if !st.emittedInput {
					st.emittedInput = true
					writeSSE("response.custom_tool_call_input.delta", map[string]any{
						"type":         "response.custom_tool_call_input.delta",
						"item_id":      st.itemID,
						"output_index": st.outputIndex,
						"delta":        patchText,
					})
					writeSSE("response.custom_tool_call_input.done", map[string]any{
						"type":         "response.custom_tool_call_input.done",
						"item_id":      st.itemID,
						"output_index": st.outputIndex,
						"input":        patchText,
					})
				}
				ev["item"] = map[string]any{
					"type":    "custom_tool_call",
					"id":      st.itemID,
					"call_id": st.callID,
					"name":    "apply_patch",
					"status":  "completed",
					"input":   patchText,
				}
				writeSSE("response.output_item.done", ev)
				return
			}

		case "response.completed":
			if respObj, ok := ev["response"].(map[string]any); ok {
				if rewriteCompletedOutputItems(respObj) {
					writeSSE("response.completed", ev)
					return
				}
			}
		}

		// Pass through untouched.
		for _, l := range rawBlock {
			_, _ = io.WriteString(w, l)
		}
		_, _ = io.WriteString(w, "\n")
		flush()
	}

	for {
		line, err := br.ReadString('\n')
		if len(line) > 0 {
			trimmed := strings.TrimRight(line, "\r\n")
			if trimmed == "" {
				processFrame()
			} else {
				if !strings.HasSuffix(line, "\n") {
					line += "\n"
				}
				rawBlock = append(rawBlock, line)
				if strings.HasPrefix(trimmed, "event:") {
					eventName = strings.TrimSpace(strings.TrimPrefix(trimmed, "event:"))
				} else if strings.HasPrefix(trimmed, "data:") {
					d := strings.TrimPrefix(trimmed, "data:")
					d = strings.TrimPrefix(d, " ")
					dataLines = append(dataLines, d)
				}
			}
		}
		if err != nil {
			processFrame()
			break
		}
	}
}

func rewriteCompletedOutputItems(respObj map[string]any) bool {
	outputs, ok := respObj["output"].([]any)
	if !ok {
		return false
	}
	changed := false
	for i, entry := range outputs {
		im, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		if asString(im["type"]) == "function_call" && asString(im["name"]) == "apply_patch" {
			id := firstNonEmpty(asString(im["id"]), fmt.Sprintf("ctc_%d", i))
			callID := firstNonEmpty(asString(im["call_id"]), id)
			patchText := extractPatchInput(asString(im["arguments"]))
			outputs[i] = map[string]any{
				"type":    "custom_tool_call",
				"id":      id,
				"call_id": callID,
				"name":    "apply_patch",
				"status":  "completed",
				"input":   patchText,
			}
			changed = true
		}
	}
	return changed
}

func rewriteNonStreamResponseJSON(raw []byte) []byte {
	var respObj map[string]any
	if err := json.Unmarshal(raw, &respObj); err != nil {
		return raw
	}
	if !rewriteCompletedOutputItems(respObj) {
		return raw
	}
	out, err := json.Marshal(respObj)
	if err != nil {
		return raw
	}
	return out
}

func asJSONInt(v any) (int, bool) {
	switch x := v.(type) {
	case float64:
		return int(x), true
	case int:
		return x, true
	case int64:
		return int(x), true
	case json.Number:
		n, err := x.Int64()
		return int(n), err == nil
	default:
		return 0, false
	}
}

func asString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
