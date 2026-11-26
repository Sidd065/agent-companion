package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

func main() {
	args := os.Args[1:]
	if len(args) > 0 && (args[0] == "--version" || args[0] == "-V" || args[0] == "version") {
		if os.Getenv("FAKECODEX_FAIL_VERSION") == "1" {
			fmt.Fprintln(os.Stderr, "simulated version failure")
			os.Exit(1)
		}
		fmt.Println("codex-cli 0.172.0")
		os.Exit(0)
	}

	if slices.Contains(args, "app-server") {
		runAppServer(args)
		return
	}

	runTUIMode(args)
}

func runTUIMode(args []string) {
	cwd, _ := os.Getwd()
	fmt.Printf("FAKEX_TUI_READY args=%s cwd=%s\n", strings.Join(args, " "), cwd)
	_ = os.Stdout.Sync()

	sigCh := make(chan os.Signal, 2)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	lineCh := make(chan string, 4)
	go func() {
		sc := bufio.NewScanner(os.Stdin)
		for sc.Scan() {
			lineCh <- strings.TrimSpace(sc.Text())
		}
		close(lineCh)
	}()

	for {
		select {
		case <-sigCh:
			fmt.Println("FAKEX_TUI_BYE")
			return
		case line, ok := <-lineCh:
			if !ok {
				return
			}
			switch {
			case slices.Contains([]string{"exit", "quit", "q", "/exit", "/quit"}, strings.ToLower(line)):
				fmt.Println("FAKEX_TUI_BYE")
				return
			case strings.HasPrefix(line, "env:"):
				k := strings.TrimSpace(strings.TrimPrefix(line, "env:"))
				fmt.Printf("FAKEX_TUI_ENV:%s=%s\n", k, os.Getenv(k))
			default:
				if line != "" {
					fmt.Printf("FAKEX_TUI_ECHO:%s\n", line)
				}
			}
		}
	}
}

type rpcEnvelope struct {
	JSONRPC string          `json:"jsonrpc,omitempty"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcErrObj      `json:"error,omitempty"`
}

type rpcErrObj struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type storedTurn struct {
	ID        string           `json:"id"`
	Status    string           `json:"status"`
	Items     []map[string]any `json:"items"`
	CreatedAt int64            `json:"createdAt"`
}

type storedThread struct {
	ID              string         `json:"id"`
	SessionID       string         `json:"sessionId"`
	ForkedFromID    string         `json:"forkedFromId,omitempty"`
	CLIVersion      string         `json:"cliVersion"`
	CreatedAt       int64          `json:"createdAt"`
	UpdatedAt       int64          `json:"updatedAt"`
	Cwd             string         `json:"cwd"`
	Ephemeral       bool           `json:"ephemeral"`
	Model           string         `json:"model"`
	ModelProvider   string         `json:"modelProvider"`
	ReasoningEffort string         `json:"reasoningEffort,omitempty"`
	ApprovalPolicy  string         `json:"approvalPolicy,omitempty"`
	Sandbox         string         `json:"sandbox,omitempty"`
	CollabMode      string         `json:"collaborationMode,omitempty"`
	Name            string         `json:"name"`
	Preview         string         `json:"preview"`
	Source          string         `json:"source"`
	Turns           []storedTurn   `json:"turns"`
	Config          map[string]any `json:"config,omitempty"`
	Resumed         bool           `json:"resumed,omitempty"`
	persisted       bool
}

type activeTurn struct {
	turnID   string
	threadID string
	cancelCh chan struct{}
	steerCh  chan string
	once     sync.Once
}

func (at *activeTurn) cancel() {
	at.once.Do(func() {
		close(at.cancelCh)
	})
}

type appServer struct {
	cliArgs           []string
	codexHome         string
	stateDir          string
	modelProvider     string
	defaultModel      string
	bridgeBaseURL     string
	bridgeEnvKey      string
	bridgeBearerToken string
	modelCatalogPath  string
	configOverrides   map[string]string

	writeMu sync.Mutex
	stateMu sync.Mutex
	threads map[string]*storedThread
	active  map[string]*activeTurn

	reqMu       sync.Mutex
	nextReqSeq  int64
	pendingReqs map[string]chan rpcEnvelope
}

func randHex(nBytes int) string {
	b := make([]byte, nBytes)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

var (
	baseURLInlineRe     = regexp.MustCompile(`base_url\s*=\s*"([^"]+)"|base_url\s*=\s*'([^']+)'|base_url\s*=\s*([^,\s}]+)`)
	envKeyInlineRe      = regexp.MustCompile(`env_key\s*=\s*"([^"]+)"|env_key\s*=\s*'([^']+)'|env_key\s*=\s*([^,\s}]+)`)
	bearerTokenInlineRe = regexp.MustCompile(`experimental_bearer_token\s*=\s*"([^"]+)"|experimental_bearer_token\s*=\s*'([^']+)'|experimental_bearer_token\s*=\s*([^,\s}]+)`)
)

func firstRegexGroup(re *regexp.Regexp, s string) string {
	if m := re.FindStringSubmatch(s); len(m) > 0 {
		for _, grp := range m[1:] {
			if grp != "" {
				return grp
			}
		}
	}
	return ""
}

func stripTOMLComment(line string) string {
	inDouble := false
	inSingle := false
	escaped := false
	for i := 0; i < len(line); i++ {
		c := line[i]
		if inDouble {
			if escaped {
				escaped = false
				continue
			}
			if c == '\\' {
				escaped = true
				continue
			}
			if c == '"' {
				inDouble = false
			}
			continue
		}
		if inSingle {
			if c == '\'' {
				inSingle = false
			}
			continue
		}
		switch c {
		case '"':
			inDouble = true
		case '\'':
			inSingle = true
		case '#':
			return line[:i]
		}
	}
	return line
}

func stripOuterQuotes(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}

func parseConfigKV(kv string, out map[string]string) {
	idx := strings.IndexByte(kv, '=')
	if idx <= 0 {
		return
	}
	k := strings.TrimSpace(kv[:idx])
	v := strings.TrimSpace(kv[idx+1:])
	unq := stripOuterQuotes(v)
	out[k] = unq
	if strings.HasPrefix(k, "model_providers.") &&
		!strings.HasSuffix(k, ".base_url") &&
		!strings.HasSuffix(k, ".env_key") &&
		!strings.HasSuffix(k, ".experimental_bearer_token") {
		if u := firstRegexGroup(baseURLInlineRe, unq); u != "" {
			out[k+".base_url"] = u
		}
		if ek := firstRegexGroup(envKeyInlineRe, unq); ek != "" {
			out[k+".env_key"] = ek
		}
		if bt := firstRegexGroup(bearerTokenInlineRe, unq); bt != "" {
			out[k+".experimental_bearer_token"] = bt
		}
	}
}

func parseTOMLFile(path string, out map[string]string) {
	b, err := os.ReadFile(path)
	if err != nil {
		return
	}
	section := ""
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		line := strings.TrimSpace(stripTOMLComment(sc.Text()))
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.TrimSpace(line[1 : len(line)-1])
			continue
		}
		idx := strings.IndexByte(line, '=')
		if idx <= 0 {
			continue
		}
		k := strings.TrimSpace(line[:idx])
		rawVal := strings.TrimSpace(line[idx+1:])
		fullKey := k
		if section != "" {
			fullKey = section + "." + k
		}
		parseConfigKV(fullKey+"="+rawVal, out)
	}
}

func runAppServer(args []string) {
	codexHome := os.Getenv("CODEX_HOME")
	if codexHome == "" {
		if h, err := os.UserHomeDir(); err == nil {
			codexHome = filepath.Join(h, ".codex")
		}
	}
	stateDir := os.Getenv("FAKECODEX_STATE_DIR")
	if stateDir == "" {
		stateDir = codexHome
	}
	if stateDir != "" {
		_ = os.MkdirAll(filepath.Join(stateDir, "threads"), 0o700)
	}

	cfg := map[string]string{}
	if codexHome != "" {
		parseTOMLFile(filepath.Join(codexHome, "config.toml"), cfg)
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case (a == "-c" || a == "--config") && i+1 < len(args):
			parseConfigKV(args[i+1], cfg)
			i++
		case strings.HasPrefix(a, "-c="):
			parseConfigKV(strings.TrimPrefix(a, "-c="), cfg)
		case strings.HasPrefix(a, "--config="):
			parseConfigKV(strings.TrimPrefix(a, "--config="), cfg)
		}
	}

	bridgeURL := firstNonEmpty(
		cfg["model_providers.openrouter.base_url"],
		cfg["openai_base_url"],
		os.Getenv("OPENAI_BASE_URL"),
	)
	bridgeEnvKey := cfg["model_providers.openrouter.env_key"]
	bridgeBearer := cfg["model_providers.openrouter.experimental_bearer_token"]
	for k, v := range cfg {
		if bridgeURL == "" && strings.HasSuffix(k, ".base_url") && v != "" {
			bridgeURL = v
		}
		if bridgeEnvKey == "" && strings.HasSuffix(k, ".env_key") && v != "" {
			bridgeEnvKey = v
		}
		if bridgeBearer == "" && strings.HasSuffix(k, ".experimental_bearer_token") && v != "" {
			bridgeBearer = v
		}
	}

	srv := &appServer{
		cliArgs:           args,
		codexHome:         codexHome,
		stateDir:          stateDir,
		modelProvider:     firstNonEmpty(cfg["model_provider"], "openai"),
		defaultModel:      firstNonEmpty(cfg["model"], "gpt-5.3-codex"),
		bridgeBaseURL:     bridgeURL,
		bridgeEnvKey:      bridgeEnvKey,
		bridgeBearerToken: bridgeBearer,
		modelCatalogPath:  cfg["model_catalog_json"],
		configOverrides:   cfg,
		threads:           map[string]*storedThread{},
		active:            map[string]*activeTurn{},
		pendingReqs:       map[string]chan rpcEnvelope{},
	}
	srv.loadThreadsFromDisk()

	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var env rpcEnvelope
		if err := json.Unmarshal(line, &env); err != nil {
			continue
		}
		if env.Method == "" && len(env.ID) > 0 {
			idKey := normalizeIDKey(env.ID)
			srv.reqMu.Lock()
			ch := srv.pendingReqs[idKey]
			delete(srv.pendingReqs, idKey)
			srv.reqMu.Unlock()
			if ch != nil {
				ch <- env
			}
			continue
		}
		if env.Method != "" && (len(env.ID) == 0 || string(env.ID) == "null") {
			continue
		}
		srv.handleRequest(env)
	}
}

func normalizeIDKey(raw json.RawMessage) string {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return "s:" + s
	}
	var n int64
	if err := json.Unmarshal(raw, &n); err == nil {
		return fmt.Sprintf("n:%d", n)
	}
	return string(bytes.TrimSpace(raw))
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func (s *appServer) sendJSON(v any) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, _ = os.Stdout.Write(append(b, '\n'))
	_ = os.Stdout.Sync()
}

func (s *appServer) sendResult(id json.RawMessage, res any) {
	raw, _ := json.Marshal(res)
	s.sendJSON(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"result":  json.RawMessage(raw),
	})
}

func (s *appServer) sendError(id json.RawMessage, code int, msg string) {
	s.sendJSON(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"error": map[string]any{
			"code":    code,
			"message": msg,
		},
	})
}

func (s *appServer) sendNotification(method string, params any) {
	s.sendJSON(map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  params,
	})
}

func (s *appServer) callClient(ctx context.Context, method string, params any) (json.RawMessage, error) {
	s.reqMu.Lock()
	s.nextReqSeq++
	reqID := fmt.Sprintf("srv-req-%d", s.nextReqSeq)
	idRaw, _ := json.Marshal(reqID)
	idKey := normalizeIDKey(idRaw)
	ch := make(chan rpcEnvelope, 1)
	s.pendingReqs[idKey] = ch
	s.reqMu.Unlock()

	s.sendJSON(map[string]any{
		"jsonrpc": "2.0",
		"id":      reqID,
		"method":  method,
		"params":  params,
	})

	select {
	case resp := <-ch:
		if resp.Error != nil {
			return nil, errors.New(resp.Error.Message)
		}
		return resp.Result, nil
	case <-ctx.Done():
		s.reqMu.Lock()
		delete(s.pendingReqs, idKey)
		s.reqMu.Unlock()
		return nil, ctx.Err()
	}
}

func (s *appServer) loadThreadsFromDisk() {
	if s.stateDir == "" {
		return
	}
	entries, err := os.ReadDir(filepath.Join(s.stateDir, "threads"))
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(s.stateDir, "threads", e.Name()))
		if err != nil {
			continue
		}
		var th storedThread
		if json.Unmarshal(b, &th) == nil && th.ID != "" {
			th.persisted = true
			if _, exists := s.threads[th.ID]; !exists {
				s.threads[th.ID] = &th
			}
		}
	}
}

func (s *appServer) saveThreadLocked(th *storedThread) {
	if s.stateDir == "" || th == nil || th.Ephemeral || !th.persisted {
		return
	}
	dir := filepath.Join(s.stateDir, "threads")
	_ = os.MkdirAll(dir, 0o700)
	b, err := json.MarshalIndent(th, "", "  ")
	if err != nil {
		return
	}
	tmp := filepath.Join(dir, th.ID+".json.tmp")
	dst := filepath.Join(dir, th.ID+".json")
	if os.WriteFile(tmp, b, 0o600) == nil {
		_ = os.Rename(tmp, dst)
	}
}

func (s *appServer) readCatalogModels(catalogPath string) ([]map[string]any, []string) {
	p := firstNonEmpty(catalogPath, s.modelCatalogPath)
	if p == "" && s.codexHome != "" {
		tmpCfg := map[string]string{}
		parseTOMLFile(filepath.Join(s.codexHome, "config.toml"), tmpCfg)
		p = tmpCfg["model_catalog_json"]
	}
	var data []map[string]any
	var slugs []string
	if p != "" {
		if b, err := os.ReadFile(p); err == nil {
			var parsed struct {
				Models []struct {
					Slug                     string `json:"slug"`
					ID                       string `json:"id"`
					Model                    string `json:"model"`
					DisplayName              string `json:"display_name"`
					Name                     string `json:"name"`
					Description              string `json:"description"`
					ContextWindow            int    `json:"context_window"`
					DefaultReasoningLevel    string `json:"default_reasoning_level"`
					SupportedReasoningLevels []struct {
						Effort      string `json:"effort"`
						Description string `json:"description"`
					} `json:"supported_reasoning_levels"`
					InputModalities []string `json:"input_modalities"`
				} `json:"models"`
			}
			if json.Unmarshal(b, &parsed) == nil {
				for _, m := range parsed.Models {
					slug := firstNonEmpty(m.Slug, m.Model, m.ID)
					if slug == "" {
						continue
					}
					slugs = append(slugs, slug)
					efforts := []map[string]any{
						{"reasoningEffort": "low", "description": "Low"},
						{"reasoningEffort": "medium", "description": "Medium"},
						{"reasoningEffort": "high", "description": "High"},
						{"reasoningEffort": "xhigh", "description": "Extra high"},
					}
					if len(m.SupportedReasoningLevels) > 0 {
						efforts = nil
						for _, e := range m.SupportedReasoningLevels {
							efforts = append(efforts, map[string]any{
								"reasoningEffort": e.Effort,
								"description":     e.Description,
							})
						}
					}
					mods := m.InputModalities
					if len(mods) == 0 {
						mods = []string{"text", "image"}
					}
					data = append(data, map[string]any{
						"id":                        slug,
						"model":                     slug,
						"displayName":               firstNonEmpty(m.DisplayName, m.Name, slug),
						"description":               m.Description,
						"hidden":                    false,
						"isDefault":                 len(data) == 0,
						"defaultReasoningEffort":    firstNonEmpty(m.DefaultReasoningLevel, "medium"),
						"supportedReasoningEfforts": efforts,
						"inputModalities":           mods,
					})
				}
			}
		}
	}
	builtins := []struct {
		id   string
		name string
	}{
		{"gpt-5.3-codex", "GPT-5.3 Codex"},
		{"o4-mini", "o4-mini"},
	}
	for _, bi := range builtins {
		if !slices.Contains(slugs, bi.id) {
			data = append(data, map[string]any{
				"id":                     bi.id,
				"model":                  bi.id,
				"displayName":            bi.name,
				"description":            "Built-in Codex model",
				"hidden":                 false,
				"isDefault":              len(data) == 0,
				"defaultReasoningEffort": "medium",
				"supportedReasoningEfforts": []map[string]any{
					{"reasoningEffort": "low", "description": "Low"},
					{"reasoningEffort": "medium", "description": "Medium"},
					{"reasoningEffort": "high", "description": "High"},
					{"reasoningEffort": "xhigh", "description": "Extra high"},
				},
				"inputModalities": []string{"text", "image"},
			})
		}
	}
	return data, slugs
}

func extractSandboxString(raw any) string {
	switch v := raw.(type) {
	case string:
		return v
	case map[string]any:
		if t, ok := v["type"].(string); ok {
			return t
		}
	}
	return ""
}

func extractApprovalString(raw any) string {
	switch v := raw.(type) {
	case string:
		return v
	case map[string]any:
		b, _ := json.Marshal(v)
		return string(b)
	}
	return ""
}

func extractCollabModeString(raw any) string {
	switch v := raw.(type) {
	case string:
		return v
	case map[string]any:
		if m, ok := v["mode"].(string); ok {
			return m
		}
	}
	return ""
}

func extractBridgeAndCatalogFromConfigMap(cfg map[string]any) (bridgeURL string, catalogPath string, provider string, envKey string, bearerToken string) {
	if cfg == nil {
		return "", "", "", "", ""
	}
	if p, ok := cfg["model_provider"].(string); ok {
		provider = p
	}
	if c, ok := cfg["model_catalog_json"].(string); ok {
		catalogPath = c
	}
	if u, ok := cfg["model_providers.openrouter.base_url"].(string); ok {
		bridgeURL = u
	}
	if ek, ok := cfg["model_providers.openrouter.env_key"].(string); ok {
		envKey = ek
	}
	if bt, ok := cfg["model_providers.openrouter.experimental_bearer_token"].(string); ok {
		bearerToken = bt
	}
	if rawInline, ok := cfg["model_providers.openrouter"].(string); ok {
		tmp := map[string]string{}
		parseConfigKV("model_providers.openrouter="+rawInline, tmp)
		if bridgeURL == "" {
			bridgeURL = tmp["model_providers.openrouter.base_url"]
		}
		if envKey == "" {
			envKey = tmp["model_providers.openrouter.env_key"]
		}
		if bearerToken == "" {
			bearerToken = tmp["model_providers.openrouter.experimental_bearer_token"]
		}
	}
	if mp, ok := cfg["model_providers"].(map[string]any); ok {
		if orMap, ok := mp["openrouter"].(map[string]any); ok {
			if u, ok := orMap["base_url"].(string); ok && bridgeURL == "" {
				bridgeURL = u
			}
			if ek, ok := orMap["env_key"].(string); ok && envKey == "" {
				envKey = ek
			}
			if bt, ok := orMap["experimental_bearer_token"].(string); ok && bearerToken == "" {
				bearerToken = bt
			}
		}
	}
	return bridgeURL, catalogPath, provider, envKey, bearerToken
}

func threadWireObject(th *storedThread, includeTurns bool) map[string]any {
	turns := []storedTurn{}
	if includeTurns && len(th.Turns) > 0 {
		turns = th.Turns
	}
	return map[string]any{
		"id":            th.ID,
		"sessionId":     th.SessionID,
		"forkedFromId":  th.ForkedFromID,
		"cliVersion":    th.CLIVersion,
		"createdAt":     th.CreatedAt,
		"updatedAt":     th.UpdatedAt,
		"cwd":           th.Cwd,
		"ephemeral":     th.Ephemeral,
		"model":         th.Model,
		"modelProvider": th.ModelProvider,
		"name":          th.Name,
		"preview":       th.Preview,
		"projectId":     nil,
		"source":        th.Source,
		"status":        map[string]any{"type": "idle"},
		"turns":         turns,
	}
}

func (s *appServer) handleRequest(env rpcEnvelope) {
	switch env.Method {
	case "initialize":
		s.sendResult(env.ID, map[string]any{
			"userAgent":      "codex-cli/0.172.0",
			"codexHome":      s.codexHome,
			"platformFamily": "unix",
			"platformOs":     "linux",
		})

	case "account/read":
		requiresAuth := s.modelProvider != "openrouter" && os.Getenv("OPENAI_API_KEY") == ""
		s.sendResult(env.ID, map[string]any{
			"account":            nil,
			"requiresOpenaiAuth": requiresAuth,
		})

	case "model/list":
		models, _ := s.readCatalogModels("")
		s.sendResult(env.ID, map[string]any{
			"data":       models,
			"nextCursor": nil,
		})

	case "collaborationMode/list":
		s.sendResult(env.ID, map[string]any{
			"data": []map[string]any{
				{"name": "Default", "mode": "default"},
				{"name": "Plan", "mode": "plan"},
			},
		})

	case "thread/start":
		var p struct {
			Cwd            string         `json:"cwd"`
			Model          string         `json:"model"`
			ModelProvider  string         `json:"modelProvider"`
			ApprovalPolicy any            `json:"approvalPolicy"`
			Sandbox        any            `json:"sandbox"`
			Config         map[string]any `json:"config"`
			Ephemeral      bool           `json:"ephemeral"`
		}
		_ = json.Unmarshal(env.Params, &p)
		cwd := p.Cwd
		if cwd == "" {
			cwd, _ = os.Getwd()
		}
		cfgBridge, cfgCatalog, cfgProvider, cfgEnvKey, cfgBearer := extractBridgeAndCatalogFromConfigMap(p.Config)
		now := time.Now().Unix()
		tid := "thr-" + randHex(6)
		model := firstNonEmpty(p.Model, s.defaultModel)
		provider := firstNonEmpty(p.ModelProvider, cfgProvider, s.modelProvider)
		approval := firstNonEmpty(extractApprovalString(p.ApprovalPolicy), "never")
		sandbox := firstNonEmpty(extractSandboxString(p.Sandbox), "workspace-write")

		th := &storedThread{
			ID:             tid,
			SessionID:      tid,
			CLIVersion:     "0.172.0",
			CreatedAt:      now,
			UpdatedAt:      now,
			Cwd:            cwd,
			Ephemeral:      p.Ephemeral,
			Model:          model,
			ModelProvider:  provider,
			ApprovalPolicy: approval,
			Sandbox:        sandbox,
			Name:           "Codex Thread",
			Preview:        "",
			Source:         "cli",
			Turns:          []storedTurn{},
			Config:         p.Config,
		}
		s.stateMu.Lock()
		if cfgBridge != "" {
			s.bridgeBaseURL = cfgBridge
		}
		if cfgEnvKey != "" {
			s.bridgeEnvKey = cfgEnvKey
		}
		if cfgBearer != "" {
			s.bridgeBearerToken = cfgBearer
		}
		if cfgCatalog != "" {
			s.modelCatalogPath = cfgCatalog
		}
		s.threads[tid] = th
		s.saveThreadLocked(th)
		wireTh := threadWireObject(th, true)
		s.stateMu.Unlock()

		s.sendNotification("thread/started", map[string]any{"thread": wireTh})
		s.sendResult(env.ID, map[string]any{
			"approvalPolicy":    approval,
			"approvalsReviewer": "user",
			"cwd":               cwd,
			"model":             model,
			"modelProvider":     provider,
			"reasoningEffort":   firstNonEmpty(th.ReasoningEffort, "medium"),
			"sandbox":           map[string]any{"type": sandbox},
			"thread":            wireTh,
		})

	case "thread/resume":
		var p struct {
			ThreadID       string         `json:"threadId"`
			Cwd            string         `json:"cwd"`
			Model          string         `json:"model"`
			ModelProvider  string         `json:"modelProvider"`
			ApprovalPolicy any            `json:"approvalPolicy"`
			Sandbox        any            `json:"sandbox"`
			Config         map[string]any `json:"config"`
		}
		_ = json.Unmarshal(env.Params, &p)
		cfgBridge, cfgCatalog, cfgProvider, cfgEnvKey, cfgBearer := extractBridgeAndCatalogFromConfigMap(p.Config)

		s.stateMu.Lock()
		if cfgBridge != "" {
			s.bridgeBaseURL = cfgBridge
		}
		if cfgEnvKey != "" {
			s.bridgeEnvKey = cfgEnvKey
		}
		if cfgBearer != "" {
			s.bridgeBearerToken = cfgBearer
		}
		if cfgCatalog != "" {
			s.modelCatalogPath = cfgCatalog
		}
		s.loadThreadsFromDisk()
		th, ok := s.threads[p.ThreadID]
		if !ok {
			s.stateMu.Unlock()
			s.sendError(env.ID, -32600, "thread not found: "+p.ThreadID)
			return
		}
		th.Resumed = true
		if p.Cwd != "" {
			th.Cwd = p.Cwd
		}
		if p.Model != "" {
			th.Model = p.Model
		}
		if p.ModelProvider != "" || cfgProvider != "" {
			th.ModelProvider = firstNonEmpty(p.ModelProvider, cfgProvider)
		}
		if ap := extractApprovalString(p.ApprovalPolicy); ap != "" {
			th.ApprovalPolicy = ap
		}
		if sb := extractSandboxString(p.Sandbox); sb != "" {
			th.Sandbox = sb
		}
		th.UpdatedAt = time.Now().Unix()
		s.saveThreadLocked(th)
		wireTh := threadWireObject(th, true)
		approval := firstNonEmpty(th.ApprovalPolicy, "never")
		sandbox := firstNonEmpty(th.Sandbox, "workspace-write")
		cwd := th.Cwd
		model := th.Model
		provider := th.ModelProvider
		s.stateMu.Unlock()

		s.sendResult(env.ID, map[string]any{
			"approvalPolicy":    approval,
			"approvalsReviewer": "user",
			"cwd":               cwd,
			"model":             model,
			"modelProvider":     provider,
			"sandbox":           map[string]any{"type": sandbox},
			"thread":            wireTh,
		})

	case "thread/read":
		var p struct {
			ThreadID     string `json:"threadId"`
			IncludeTurns *bool  `json:"includeTurns"`
		}
		_ = json.Unmarshal(env.Params, &p)
		inc := true
		if p.IncludeTurns != nil {
			inc = *p.IncludeTurns
		}
		s.stateMu.Lock()
		s.loadThreadsFromDisk()
		th, ok := s.threads[p.ThreadID]
		if !ok {
			s.stateMu.Unlock()
			s.sendError(env.ID, -32602, "thread not found: "+p.ThreadID)
			return
		}
		wireTh := threadWireObject(th, inc)
		s.stateMu.Unlock()
		s.sendResult(env.ID, map[string]any{"thread": wireTh})

	case "thread/list":
		var p struct {
			Cwd string `json:"cwd"`
		}
		_ = json.Unmarshal(env.Params, &p)
		s.stateMu.Lock()
		s.loadThreadsFromDisk()
		var list []map[string]any
		var ids []string
		for id := range s.threads {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			th := s.threads[id]
			if th.Ephemeral {
				continue
			}
			if p.Cwd != "" && th.Cwd != p.Cwd {
				continue
			}
			list = append(list, threadWireObject(th, false))
		}
		s.stateMu.Unlock()
		if list == nil {
			list = []map[string]any{}
		}
		s.sendResult(env.ID, map[string]any{
			"data":       list,
			"nextCursor": nil,
		})

	case "thread/turns/list":
		var p struct {
			ThreadID string `json:"threadId"`
		}
		_ = json.Unmarshal(env.Params, &p)
		s.stateMu.Lock()
		s.loadThreadsFromDisk()
		th, ok := s.threads[p.ThreadID]
		var turns []storedTurn
		if ok {
			turns = append(turns, th.Turns...)
		}
		s.stateMu.Unlock()
		if turns == nil {
			turns = []storedTurn{}
		}
		s.sendResult(env.ID, map[string]any{
			"data":       turns,
			"nextCursor": nil,
		})

	case "thread/fork":
		var p struct {
			ThreadID      string `json:"threadId"`
			Cwd           string `json:"cwd"`
			Model         string `json:"model"`
			ModelProvider string `json:"modelProvider"`
			BeforeTurnID  string `json:"beforeTurnId"`
			LastTurnID    string `json:"lastTurnId"`
		}
		_ = json.Unmarshal(env.Params, &p)
		s.stateMu.Lock()
		s.loadThreadsFromDisk()
		src, ok := s.threads[p.ThreadID]
		if !ok {
			s.stateMu.Unlock()
			s.sendError(env.ID, -32602, "source thread not found: "+p.ThreadID)
			return
		}
		now := time.Now().Unix()
		newID := "thr-fork-" + randHex(5)
		var copiedTurns []storedTurn
		for _, t := range src.Turns {
			if p.BeforeTurnID != "" && t.ID == p.BeforeTurnID {
				break
			}
			copiedTurns = append(copiedTurns, t)
			if p.LastTurnID != "" && t.ID == p.LastTurnID {
				break
			}
		}
		forkTh := &storedThread{
			ID:              newID,
			SessionID:       newID,
			ForkedFromID:    src.ID,
			CLIVersion:      "0.172.0",
			CreatedAt:       now,
			UpdatedAt:       now,
			Cwd:             firstNonEmpty(p.Cwd, src.Cwd),
			Model:           firstNonEmpty(p.Model, src.Model),
			ModelProvider:   firstNonEmpty(p.ModelProvider, src.ModelProvider),
			ReasoningEffort: src.ReasoningEffort,
			ApprovalPolicy:  src.ApprovalPolicy,
			Sandbox:         src.Sandbox,
			Name:            src.Name + " (fork)",
			Preview:         src.Preview,
			Source:          "cli",
			Turns:           copiedTurns,
		}
		forkTh.persisted = true
		s.threads[newID] = forkTh
		s.saveThreadLocked(forkTh)
		wireTh := threadWireObject(forkTh, true)
		s.stateMu.Unlock()

		s.sendNotification("thread/started", map[string]any{"thread": wireTh})
		s.sendResult(env.ID, map[string]any{
			"approvalPolicy":    firstNonEmpty(forkTh.ApprovalPolicy, "never"),
			"approvalsReviewer": "user",
			"cwd":               forkTh.Cwd,
			"model":             forkTh.Model,
			"modelProvider":     forkTh.ModelProvider,
			"sandbox":           map[string]any{"type": firstNonEmpty(forkTh.Sandbox, "workspace-write")},
			"thread":            wireTh,
		})

	case "thread/revert", "thread/rollback":
		var p struct {
			ThreadID     string `json:"threadId"`
			BeforeTurnID string `json:"beforeTurnId"`
			TurnID       string `json:"turnId"`
			NumTurns     int    `json:"numTurns"`
		}
		_ = json.Unmarshal(env.Params, &p)
		s.stateMu.Lock()
		s.loadThreadsFromDisk()
		th, ok := s.threads[p.ThreadID]
		if !ok {
			s.stateMu.Unlock()
			s.sendError(env.ID, -32602, "thread not found: "+p.ThreadID)
			return
		}
		targetTurn := firstNonEmpty(p.BeforeTurnID, p.TurnID)
		if targetTurn != "" {
			idx := -1
			for i, t := range th.Turns {
				if t.ID == targetTurn {
					idx = i
					break
				}
			}
			if idx >= 0 {
				th.Turns = th.Turns[:idx]
			} else if len(th.Turns) > 0 {
				th.Turns = th.Turns[:len(th.Turns)-1]
			}
		} else if p.NumTurns > 0 && p.NumTurns <= len(th.Turns) {
			th.Turns = th.Turns[:len(th.Turns)-p.NumTurns]
		} else if len(th.Turns) > 0 {
			th.Turns = th.Turns[:len(th.Turns)-1]
		}
		th.UpdatedAt = time.Now().Unix()
		s.saveThreadLocked(th)
		wireTh := threadWireObject(th, true)
		s.stateMu.Unlock()

		s.sendNotification("thread/reverted", map[string]any{"threadId": th.ID})
		s.sendResult(env.ID, map[string]any{"thread": wireTh})

	case "thread/compact/start":
		var p struct {
			ThreadID string `json:"threadId"`
		}
		_ = json.Unmarshal(env.Params, &p)
		turnID := "turn-compact-" + randHex(4)
		s.sendNotification("item/completed", map[string]any{
			"threadId":      p.ThreadID,
			"turnId":        turnID,
			"completedAtMs": time.Now().UnixMilli(),
			"item": map[string]any{
				"id":   "item-compact-" + randHex(4),
				"type": "contextCompaction",
			},
		})
		s.sendNotification("thread/compacted", map[string]any{
			"threadId": p.ThreadID,
			"turnId":   turnID,
		})
		s.sendResult(env.ID, map[string]any{})

	case "thread/settings/update":
		var p struct {
			ThreadID          string `json:"threadId"`
			Model             string `json:"model"`
			ModelProvider     string `json:"modelProvider"`
			Effort            string `json:"effort"`
			ReasoningEffort   string `json:"reasoningEffort"`
			ApprovalPolicy    any    `json:"approvalPolicy"`
			Sandbox           any    `json:"sandbox"`
			SandboxPolicy     any    `json:"sandboxPolicy"`
			CollaborationMode any    `json:"collaborationMode"`
		}
		_ = json.Unmarshal(env.Params, &p)
		s.stateMu.Lock()
		if th, ok := s.threads[p.ThreadID]; ok {
			if p.Model != "" {
				th.Model = p.Model
			}
			if p.ModelProvider != "" {
				th.ModelProvider = p.ModelProvider
			}
			if eff := firstNonEmpty(p.Effort, p.ReasoningEffort); eff != "" {
				th.ReasoningEffort = eff
			}
			if ap := extractApprovalString(p.ApprovalPolicy); ap != "" {
				th.ApprovalPolicy = ap
			}
			if sb := firstNonEmpty(extractSandboxString(p.SandboxPolicy), extractSandboxString(p.Sandbox)); sb != "" {
				th.Sandbox = sb
			}
			if cm := extractCollabModeString(p.CollaborationMode); cm != "" {
				th.CollabMode = cm
			}
			th.UpdatedAt = time.Now().Unix()
			s.saveThreadLocked(th)
		}
		s.stateMu.Unlock()
		s.sendResult(env.ID, map[string]any{})

	case "thread/name/set":
		var p struct {
			ThreadID string `json:"threadId"`
			Name     string `json:"name"`
		}
		_ = json.Unmarshal(env.Params, &p)
		s.stateMu.Lock()
		s.loadThreadsFromDisk()
		th, ok := s.threads[p.ThreadID]
		if !ok {
			s.stateMu.Unlock()
			s.sendError(env.ID, -32600, "thread not found: "+p.ThreadID)
			return
		}
		th.Name = p.Name
		th.UpdatedAt = time.Now().Unix()
		th.persisted = true
		s.saveThreadLocked(th)
		s.stateMu.Unlock()
		s.sendNotification("thread/name/updated", map[string]any{"threadId": th.ID, "threadName": th.Name})
		s.sendResult(env.ID, map[string]any{})

	case "thread/archive":
		var p struct {
			ThreadID string `json:"threadId"`
		}
		_ = json.Unmarshal(env.Params, &p)
		s.stateMu.Lock()
		s.loadThreadsFromDisk()
		if th, ok := s.threads[p.ThreadID]; ok {
			th.UpdatedAt = time.Now().Unix()
			th.persisted = true
			s.saveThreadLocked(th)
		}
		s.stateMu.Unlock()
		s.sendResult(env.ID, map[string]any{})

	case "turn/steer":
		var p struct {
			ThreadID       string `json:"threadId"`
			ExpectedTurnID string `json:"expectedTurnId"`
			Input          []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"input"`
		}
		_ = json.Unmarshal(env.Params, &p)
		var parts []string
		for _, in := range p.Input {
			if in.Text != "" {
				parts = append(parts, in.Text)
			}
		}
		steerText := strings.Join(parts, "\n")

		s.stateMu.Lock()
		at := s.active[p.ThreadID]
		s.stateMu.Unlock()
		if at == nil || (p.ExpectedTurnID != "" && at.turnID != p.ExpectedTurnID) {
			s.sendError(env.ID, -32000, "activeTurnNotSteerable")
			return
		}
		select {
		case at.steerCh <- steerText:
		default:
		}
		s.sendResult(env.ID, map[string]any{"turnId": at.turnID})

	case "turn/interrupt":
		var p struct {
			ThreadID string `json:"threadId"`
			TurnID   string `json:"turnId"`
		}
		_ = json.Unmarshal(env.Params, &p)
		s.stateMu.Lock()
		at := s.active[p.ThreadID]
		s.stateMu.Unlock()
		if at != nil {
			at.cancel()
		}
		s.sendResult(env.ID, map[string]any{})

	case "turn/start":
		s.handleTurnStart(env)

	default:
		s.sendResult(env.ID, map[string]any{})
	}
}

func (s *appServer) handleTurnStart(env rpcEnvelope) {
	var p struct {
		ThreadID          string         `json:"threadId"`
		Cwd               string         `json:"cwd"`
		Model             string         `json:"model"`
		ModelProvider     string         `json:"modelProvider"`
		Effort            string         `json:"effort"`
		ReasoningEffort   string         `json:"reasoningEffort"`
		ApprovalPolicy    any            `json:"approvalPolicy"`
		SandboxPolicy     any            `json:"sandboxPolicy"`
		Sandbox           any            `json:"sandbox"`
		CollaborationMode any            `json:"collaborationMode"`
		Config            map[string]any `json:"config"`
		Input             []struct {
			Type string `json:"type"`
			Text string `json:"text"`
			Path string `json:"path"`
			URL  string `json:"url"`
		} `json:"input"`
	}
	_ = json.Unmarshal(env.Params, &p)

	var textParts []string
	for _, in := range p.Input {
		if in.Text != "" {
			textParts = append(textParts, in.Text)
		}
	}
	promptText := strings.Join(textParts, "\n")

	cfgBridge, cfgCatalog, cfgProvider, cfgEnvKey, cfgBearer := extractBridgeAndCatalogFromConfigMap(p.Config)

	s.stateMu.Lock()
	if cfgBridge != "" {
		s.bridgeBaseURL = cfgBridge
	}
	if cfgEnvKey != "" {
		s.bridgeEnvKey = cfgEnvKey
	}
	if cfgBearer != "" {
		s.bridgeBearerToken = cfgBearer
	}
	if cfgCatalog != "" {
		s.modelCatalogPath = cfgCatalog
	}
	s.loadThreadsFromDisk()
	th, ok := s.threads[p.ThreadID]
	if !ok {
		s.stateMu.Unlock()
		s.sendError(env.ID, -32600, "thread not found: "+p.ThreadID)
		return
	}
	if p.Cwd != "" {
		th.Cwd = p.Cwd
	}
	if p.Model != "" {
		th.Model = p.Model
	}
	if p.ModelProvider != "" || cfgProvider != "" {
		th.ModelProvider = firstNonEmpty(p.ModelProvider, cfgProvider)
	}
	eff := firstNonEmpty(p.Effort, p.ReasoningEffort)
	if eff != "" {
		th.ReasoningEffort = eff
	}
	if ap := extractApprovalString(p.ApprovalPolicy); ap != "" {
		th.ApprovalPolicy = ap
	}
	if sb := firstNonEmpty(extractSandboxString(p.SandboxPolicy), extractSandboxString(p.Sandbox)); sb != "" {
		th.Sandbox = sb
	}
	if cm := extractCollabModeString(p.CollaborationMode); cm != "" {
		th.CollabMode = cm
	}
	if p.Config != nil {
		th.Config = p.Config
	}
	th.UpdatedAt = time.Now().Unix()
	th.persisted = true
	s.saveThreadLocked(th)

	turnID := fmt.Sprintf("turn-%d-%s", len(th.Turns)+1, randHex(4))
	at := &activeTurn{
		turnID:   turnID,
		threadID: th.ID,
		cancelCh: make(chan struct{}),
		steerCh:  make(chan string, 8),
	}
	s.active[th.ID] = at

	snapThread := *th
	bridgeURL := s.bridgeBaseURL
	catalogPath := s.modelCatalogPath
	s.stateMu.Unlock()

	turnObj := map[string]any{
		"id":     turnID,
		"status": "inProgress",
		"items":  []any{},
	}
	s.sendResult(env.ID, map[string]any{"turn": turnObj})
	s.sendNotification("turn/started", map[string]any{
		"threadId": snapThread.ID,
		"turn":     turnObj,
	})

	go s.executeTurn(at, snapThread, promptText, bridgeURL, catalogPath)
}

var (
	editDirRe         = regexp.MustCompile(`\[edit:([^:\]]+):([^\]]*)\]`)
	cmdDirRe          = regexp.MustCompile(`\[cmd:([^\]]+)\]`)
	approveCmdDirRe   = regexp.MustCompile(`\[approve-cmd:([^\]]+)\]`)
	approvePatchDirRe = regexp.MustCompile(`\[approve-patch:([^:\]]+):([^\]]*)\]`)
	slowDirRe         = regexp.MustCompile(`\[slow:(\d+)\]`)
)

func unescapeDirectiveContent(s string) string {
	s = strings.ReplaceAll(s, `\n`, "\n")
	s = strings.ReplaceAll(s, `\t`, "\t")
	return s
}

func extractDecision(raw json.RawMessage) string {
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return "decline"
	}
	if d, ok := m["decision"].(string); ok && d != "" {
		return d
	}
	if o, ok := m["outcome"].(string); ok && o != "" {
		return o
	}
	if nested, ok := m["outcome"].(map[string]any); ok {
		if opt, ok := nested["optionId"].(string); ok && opt != "" {
			return opt
		}
	}
	return "decline"
}

func isApproveDecision(dec string) bool {
	switch dec {
	case "accept", "acceptForSession", "allow", "allow_once", "allow_always", "approved":
		return true
	default:
		return false
	}
}

func extractQuestionAnswerSummary(raw json.RawMessage) string {
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return string(raw)
	}
	if ansMap, ok := m["answers"].(map[string]any); ok {
		var keys []string
		for k := range ansMap {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var out []string
		for _, k := range keys {
			v := ansMap[k]
			switch tv := v.(type) {
			case string:
				out = append(out, k+"="+tv)
			case map[string]any:
				if arr, ok := tv["answers"].([]any); ok {
					var strs []string
					for _, elem := range arr {
						strs = append(strs, fmt.Sprint(elem))
					}
					out = append(out, k+"="+strings.Join(strs, ","))
				} else {
					b, _ := json.Marshal(tv)
					out = append(out, k+"="+string(b))
				}
			case []any:
				var strs []string
				for _, elem := range tv {
					strs = append(strs, fmt.Sprint(elem))
				}
				out = append(out, k+"="+strings.Join(strs, ","))
			default:
				out = append(out, fmt.Sprintf("%s=%v", k, tv))
			}
		}
		if len(out) > 0 {
			return strings.Join(out, ";")
		}
	}
	if msg, ok := m["message"].(string); ok && msg != "" {
		return msg
	}
	b, _ := json.Marshal(m)
	return string(b)
}

func unifiedFileDiff(relPath, oldContent, newContent string, existed bool) string {
	var buf strings.Builder
	fmt.Fprintf(&buf, "diff --git a/%s b/%s\n", relPath, relPath)
	if !existed {
		buf.WriteString("new file mode 100644\n")
		buf.WriteString("--- /dev/null\n")
	} else {
		fmt.Fprintf(&buf, "--- a/%s\n", relPath)
	}
	fmt.Fprintf(&buf, "+++ b/%s\n", relPath)
	oldLines := splitLinesTrimTrailing(oldContent)
	newLines := splitLinesTrimTrailing(newContent)
	if !existed {
		fmt.Fprintf(&buf, "@@ -0,0 +1,%d @@\n", len(newLines))
		for _, l := range newLines {
			buf.WriteString("+" + l + "\n")
		}
	} else {
		fmt.Fprintf(&buf, "@@ -1,%d +1,%d @@\n", len(oldLines), len(newLines))
		for _, l := range oldLines {
			buf.WriteString("-" + l + "\n")
		}
		for _, l := range newLines {
			buf.WriteString("+" + l + "\n")
		}
	}
	return buf.String()
}

func splitLinesTrimTrailing(s string) []string {
	if s == "" {
		return nil
	}
	s = strings.TrimSuffix(s, "\n")
	return strings.Split(s, "\n")
}

func applySimplePatchInDir(cwd, patchText string) (relPath string, diffOut string) {
	lines := strings.Split(patchText, "\n")
	var curFile string
	var contentLines []string
	flush := func() {
		if curFile == "" {
			return
		}
		fullPath := filepath.Join(cwd, curFile)
		oldBytes, err := os.ReadFile(fullPath)
		existed := err == nil
		newContent := strings.Join(contentLines, "\n") + "\n"
		_ = os.MkdirAll(filepath.Dir(fullPath), 0o755)
		_ = os.WriteFile(fullPath, []byte(newContent), 0o644)
		relPath = curFile
		diffOut = unifiedFileDiff(curFile, string(oldBytes), newContent, existed)
		curFile = ""
		contentLines = nil
	}
	for _, line := range lines {
		line = strings.TrimRight(line, "\r")
		switch {
		case strings.HasPrefix(line, "*** Add File: "):
			flush()
			curFile = strings.TrimSpace(strings.TrimPrefix(line, "*** Add File: "))
		case strings.HasPrefix(line, "*** Update File: "):
			flush()
			curFile = strings.TrimSpace(strings.TrimPrefix(line, "*** Update File: "))
		case strings.HasPrefix(line, "*** End Patch"):
			flush()
		case curFile != "" && strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++"):
			contentLines = append(contentLines, line[1:])
		}
	}
	flush()
	return relPath, diffOut
}

func (s *appServer) executeTurn(at *activeTurn, th storedThread, promptText, bridgeURL, catalogPath string) {
	defer func() {
		s.stateMu.Lock()
		if cur := s.active[th.ID]; cur == at {
			delete(s.active, th.ID)
		}
		s.stateMu.Unlock()
	}()

	turnCtx, cancelTurnCtx := context.WithCancel(context.Background())
	go func() {
		select {
		case <-at.cancelCh:
			cancelTurnCtx()
		case <-turnCtx.Done():
		}
	}()
	defer cancelTurnCtx()

	var completedItems []map[string]any
	userItem := map[string]any{
		"id":   "u-" + randHex(4),
		"type": "userMessage",
		"content": []map[string]any{
			{"type": "text", "text": promptText},
		},
	}
	completedItems = append(completedItems, userItem)

	directiveText := promptText
	if idx := strings.LastIndex(promptText, "\n\nQuestion: "); idx >= 0 {
		directiveText = promptText[idx+len("\n\nQuestion: "):]
	}
	allowMutating := !th.Ephemeral

	if allowMutating && strings.Contains(directiveText, "[crash]") {
		msgID := "msg-" + randHex(4)
		s.sendNotification("item/started", map[string]any{
			"threadId":    th.ID,
			"turnId":      at.turnID,
			"startedAtMs": time.Now().UnixMilli(),
			"item":        map[string]any{"id": msgID, "type": "agentMessage", "text": ""},
		})
		s.sendNotification("item/agentMessage/delta", map[string]any{
			"threadId": th.ID,
			"turnId":   at.turnID,
			"itemId":   msgID,
			"delta":    "crashing now...",
		})
		time.Sleep(25 * time.Millisecond)
		os.Exit(2)
	}

	reasonID := "rsn-" + randHex(4)
	s.sendNotification("item/started", map[string]any{
		"threadId":    th.ID,
		"turnId":      at.turnID,
		"startedAtMs": time.Now().UnixMilli(),
		"item": map[string]any{
			"id":      reasonID,
			"type":    "reasoning",
			"summary": []string{},
			"content": []string{},
		},
	})
	s.sendNotification("item/reasoning/summaryTextDelta", map[string]any{
		"threadId":     th.ID,
		"turnId":       at.turnID,
		"itemId":       reasonID,
		"summaryIndex": 0,
		"delta":        "Analyzing request ",
	})
	s.sendNotification("item/reasoning/textDelta", map[string]any{
		"threadId":     th.ID,
		"turnId":       at.turnID,
		"itemId":       reasonID,
		"contentIndex": 0,
		"delta":        "and preparing actions.",
	})
	reasonDone := map[string]any{
		"id":      reasonID,
		"type":    "reasoning",
		"summary": []string{"Analyzing request"},
		"content": []string{"Analyzing request and preparing actions."},
	}
	s.sendNotification("item/completed", map[string]any{
		"threadId":      th.ID,
		"turnId":        at.turnID,
		"completedAtMs": time.Now().UnixMilli(),
		"item":          reasonDone,
	})
	completedItems = append(completedItems, reasonDone)

	var assistantExtraNotes []string

	if strings.Contains(directiveText, "[plan]") {
		planSteps := []map[string]any{
			{"step": "Inspect workspace", "status": "completed"},
			{"step": "Implement requested changes", "status": "inProgress"},
		}
		s.sendNotification("turn/plan/updated", map[string]any{
			"threadId":    th.ID,
			"turnId":      at.turnID,
			"explanation": "Execution plan updated",
			"plan":        planSteps,
		})
		planItem := map[string]any{
			"id":   "plan-" + randHex(4),
			"type": "plan",
			"text": "1. Inspect workspace\n2. Implement requested changes",
		}
		s.sendNotification("item/started", map[string]any{
			"threadId":    th.ID,
			"turnId":      at.turnID,
			"startedAtMs": time.Now().UnixMilli(),
			"item":        planItem,
		})
		s.sendNotification("item/completed", map[string]any{
			"threadId":      th.ID,
			"turnId":        at.turnID,
			"completedAtMs": time.Now().UnixMilli(),
			"item":          planItem,
		})
		completedItems = append(completedItems, planItem)
	}

	if strings.Contains(directiveText, "[subagent]") {
		subItem := map[string]any{
			"id":                "sub-" + randHex(4),
			"type":              "collabAgentToolCall",
			"tool":              "spawnAgent",
			"status":            "completed",
			"senderThreadId":    th.ID,
			"receiverThreadIds": []string{"sub-thr-1"},
			"prompt":            "Verify helper module",
			"agentsStates":      map[string]any{},
		}
		s.sendNotification("item/started", map[string]any{
			"threadId":    th.ID,
			"turnId":      at.turnID,
			"startedAtMs": time.Now().UnixMilli(),
			"item":        subItem,
		})
		s.sendNotification("item/completed", map[string]any{
			"threadId":      th.ID,
			"turnId":        at.turnID,
			"completedAtMs": time.Now().UnixMilli(),
			"item":          subItem,
		})
		completedItems = append(completedItems, subItem)
	}

	var approveCmdMatches [][]string
	if allowMutating {
		approveCmdMatches = approveCmdDirRe.FindAllStringSubmatch(directiveText, -1)
	}
	for _, m := range approveCmdMatches {
		shellCmd := m[1]
		itemID := "cmd-" + randHex(4)
		s.sendNotification("item/started", map[string]any{
			"threadId":    th.ID,
			"turnId":      at.turnID,
			"startedAtMs": time.Now().UnixMilli(),
			"item": map[string]any{
				"id":             itemID,
				"type":           "commandExecution",
				"command":        shellCmd,
				"cwd":            th.Cwd,
				"status":         "inProgress",
				"commandActions": []any{},
			},
		})
		respRaw, err := s.callClient(turnCtx, "item/commandExecution/requestApproval", map[string]any{
			"threadId":    th.ID,
			"turnId":      at.turnID,
			"itemId":      itemID,
			"startedAtMs": time.Now().UnixMilli(),
			"command":     shellCmd,
			"cwd":         th.Cwd,
			"reason":      "Needs shell command approval",
		})
		if err != nil || turnCtx.Err() != nil {
			s.finishInterrupted(th.ID, at.turnID, completedItems)
			return
		}
		dec := extractDecision(respRaw)
		assistantExtraNotes = append(assistantExtraNotes, "APPROVAL_DECISION:"+dec)
		if isApproveDecision(dec) {
			outStr, exitCode := runShellInDir(turnCtx, th.Cwd, shellCmd)
			if outStr != "" {
				s.sendNotification("item/commandExecution/outputDelta", map[string]any{
					"threadId": th.ID,
					"turnId":   at.turnID,
					"itemId":   itemID,
					"delta":    outStr,
				})
			}
			st := "completed"
			if exitCode != 0 {
				st = "failed"
			}
			doneItem := map[string]any{
				"id":               itemID,
				"type":             "commandExecution",
				"command":          shellCmd,
				"cwd":              th.Cwd,
				"status":           st,
				"exitCode":         exitCode,
				"aggregatedOutput": outStr,
				"commandActions":   []any{},
			}
			s.sendNotification("item/completed", map[string]any{
				"threadId":      th.ID,
				"turnId":        at.turnID,
				"completedAtMs": time.Now().UnixMilli(),
				"item":          doneItem,
			})
			completedItems = append(completedItems, doneItem)
		} else {
			doneItem := map[string]any{
				"id":               itemID,
				"type":             "commandExecution",
				"command":          shellCmd,
				"cwd":              th.Cwd,
				"status":           "declined",
				"exitCode":         1,
				"aggregatedOutput": "",
				"commandActions":   []any{},
			}
			s.sendNotification("item/completed", map[string]any{
				"threadId":      th.ID,
				"turnId":        at.turnID,
				"completedAtMs": time.Now().UnixMilli(),
				"item":          doneItem,
			})
			completedItems = append(completedItems, doneItem)
		}
	}

	var approvePatchMatches [][]string
	if allowMutating {
		approvePatchMatches = approvePatchDirRe.FindAllStringSubmatch(directiveText, -1)
	}
	for _, m := range approvePatchMatches {
		relPath := m[1]
		newContent := unescapeDirectiveContent(m[2])
		itemID := "fc-" + randHex(4)
		previewDiff := unifiedFileDiff(relPath, "", newContent, false)
		s.sendNotification("item/started", map[string]any{
			"threadId":    th.ID,
			"turnId":      at.turnID,
			"startedAtMs": time.Now().UnixMilli(),
			"item": map[string]any{
				"id":     itemID,
				"type":   "fileChange",
				"status": "inProgress",
				"changes": []map[string]any{
					{
						"path": relPath,
						"kind": map[string]any{"type": "add"},
						"diff": previewDiff,
					},
				},
			},
		})
		respRaw, err := s.callClient(turnCtx, "item/fileChange/requestApproval", map[string]any{
			"threadId":    th.ID,
			"turnId":      at.turnID,
			"itemId":      itemID,
			"startedAtMs": time.Now().UnixMilli(),
			"reason":      "Needs file patch approval for " + relPath,
			"grantRoot":   th.Cwd,
		})
		if err != nil || turnCtx.Err() != nil {
			s.finishInterrupted(th.ID, at.turnID, completedItems)
			return
		}
		dec := extractDecision(respRaw)
		assistantExtraNotes = append(assistantExtraNotes, "PATCH_DECISION:"+dec)
		if isApproveDecision(dec) {
			fullPath := filepath.Join(th.Cwd, relPath)
			oldBytes, readErr := os.ReadFile(fullPath)
			existed := readErr == nil
			_ = os.MkdirAll(filepath.Dir(fullPath), 0o755)
			_ = os.WriteFile(fullPath, []byte(newContent), 0o644)
			kindType := "add"
			if existed {
				kindType = "update"
			}
			diffStr := unifiedFileDiff(relPath, string(oldBytes), newContent, existed)
			doneItem := map[string]any{
				"id":     itemID,
				"type":   "fileChange",
				"status": "completed",
				"changes": []map[string]any{
					{
						"path": relPath,
						"kind": map[string]any{"type": kindType},
						"diff": diffStr,
					},
				},
			}
			s.sendNotification("item/completed", map[string]any{
				"threadId":      th.ID,
				"turnId":        at.turnID,
				"completedAtMs": time.Now().UnixMilli(),
				"item":          doneItem,
			})
			s.sendNotification("turn/diff/updated", map[string]any{
				"threadId": th.ID,
				"turnId":   at.turnID,
				"diff":     diffStr,
			})
			completedItems = append(completedItems, doneItem)
		} else {
			doneItem := map[string]any{
				"id":      itemID,
				"type":    "fileChange",
				"status":  "declined",
				"changes": []any{},
			}
			s.sendNotification("item/completed", map[string]any{
				"threadId":      th.ID,
				"turnId":        at.turnID,
				"completedAtMs": time.Now().UnixMilli(),
				"item":          doneItem,
			})
			completedItems = append(completedItems, doneItem)
		}
	}

	if allowMutating && strings.Contains(directiveText, "[question]") {
		qItemID := "q-" + randHex(4)
		respRaw, err := s.callClient(turnCtx, "item/tool/requestUserInput", map[string]any{
			"threadId":   th.ID,
			"turnId":     at.turnID,
			"itemId":     qItemID,
			"isBlocking": true,
			"questions": []map[string]any{
				{
					"id":       "q_env",
					"header":   "Target",
					"question": "Which target?",
					"isOther":  true,
					"options": []map[string]any{
						{"label": "Staging", "description": "Deploy to staging"},
						{"label": "Prod", "description": "Deploy to prod"},
					},
				},
			},
		})
		if err != nil || turnCtx.Err() != nil {
			s.finishInterrupted(th.ID, at.turnID, completedItems)
			return
		}
		ansSummary := extractQuestionAnswerSummary(respRaw)
		assistantExtraNotes = append(assistantExtraNotes, "QUESTION_ANSWER:"+ansSummary)
	}

	var cmdMatches [][]string
	if allowMutating {
		cmdMatches = cmdDirRe.FindAllStringSubmatch(directiveText, -1)
	}
	for _, m := range cmdMatches {
		shellCmd := m[1]
		itemID := "cmd-" + randHex(4)
		s.sendNotification("item/started", map[string]any{
			"threadId":    th.ID,
			"turnId":      at.turnID,
			"startedAtMs": time.Now().UnixMilli(),
			"item": map[string]any{
				"id":             itemID,
				"type":           "commandExecution",
				"command":        shellCmd,
				"cwd":            th.Cwd,
				"status":         "inProgress",
				"commandActions": []any{},
			},
		})
		outStr, exitCode := runShellInDir(turnCtx, th.Cwd, shellCmd)
		if turnCtx.Err() != nil {
			s.finishInterrupted(th.ID, at.turnID, completedItems)
			return
		}
		if outStr != "" {
			s.sendNotification("item/commandExecution/outputDelta", map[string]any{
				"threadId": th.ID,
				"turnId":   at.turnID,
				"itemId":   itemID,
				"delta":    outStr,
			})
		}
		st := "completed"
		if exitCode != 0 {
			st = "failed"
		}
		doneItem := map[string]any{
			"id":               itemID,
			"type":             "commandExecution",
			"command":          shellCmd,
			"cwd":              th.Cwd,
			"status":           st,
			"exitCode":         exitCode,
			"aggregatedOutput": outStr,
			"commandActions":   []any{},
		}
		s.sendNotification("item/completed", map[string]any{
			"threadId":      th.ID,
			"turnId":        at.turnID,
			"completedAtMs": time.Now().UnixMilli(),
			"item":          doneItem,
		})
		completedItems = append(completedItems, doneItem)
	}

	var editMatches [][]string
	if allowMutating {
		editMatches = editDirRe.FindAllStringSubmatch(directiveText, -1)
	}
	for _, m := range editMatches {
		relPath := m[1]
		newContent := unescapeDirectiveContent(m[2])
		fullPath := filepath.Join(th.Cwd, relPath)
		oldBytes, readErr := os.ReadFile(fullPath)
		existed := readErr == nil
		_ = os.MkdirAll(filepath.Dir(fullPath), 0o755)
		_ = os.WriteFile(fullPath, []byte(newContent), 0o644)
		kindType := "add"
		if existed {
			kindType = "update"
		}
		diffStr := unifiedFileDiff(relPath, string(oldBytes), newContent, existed)
		itemID := "fc-" + randHex(4)
		startItem := map[string]any{
			"id":     itemID,
			"type":   "fileChange",
			"status": "inProgress",
			"changes": []map[string]any{
				{
					"path": relPath,
					"kind": map[string]any{"type": kindType},
					"diff": diffStr,
				},
			},
		}
		s.sendNotification("item/started", map[string]any{
			"threadId":    th.ID,
			"turnId":      at.turnID,
			"startedAtMs": time.Now().UnixMilli(),
			"item":        startItem,
		})
		doneItem := map[string]any{
			"id":     itemID,
			"type":   "fileChange",
			"status": "completed",
			"changes": []map[string]any{
				{
					"path": relPath,
					"kind": map[string]any{"type": kindType},
					"diff": diffStr,
				},
			},
		}
		s.sendNotification("item/completed", map[string]any{
			"threadId":      th.ID,
			"turnId":        at.turnID,
			"completedAtMs": time.Now().UnixMilli(),
			"item":          doneItem,
		})
		s.sendNotification("turn/diff/updated", map[string]any{
			"threadId": th.ID,
			"turnId":   at.turnID,
			"diff":     diffStr,
		})
		completedItems = append(completedItems, doneItem)
	}

	if strings.Contains(directiveText, "[bridge-probe]") ||
		strings.Contains(directiveText, "[bridge-402]") ||
		strings.Contains(directiveText, "[bridge-402-unaffordable]") ||
		strings.Contains(directiveText, "[bridge-401]") ||
		strings.Contains(directiveText, "[bridge-patch]") {
		bridgeText, bridgeItems, err := s.callLoopbackBridge(turnCtx, th, at.turnID, directiveText, bridgeURL)
		if err != nil {
			if turnCtx.Err() != nil {
				s.finishInterrupted(th.ID, at.turnID, completedItems)
				return
			}
			s.sendNotification("error", map[string]any{
				"threadId":  th.ID,
				"turnId":    at.turnID,
				"willRetry": false,
				"error":     map[string]any{"message": err.Error()},
			})
			s.sendNotification("turn/completed", map[string]any{
				"threadId": th.ID,
				"turn": map[string]any{
					"id":     at.turnID,
					"status": "failed",
					"error":  map[string]any{"message": err.Error()},
					"items":  completedItems,
				},
			})
			return
		}
		completedItems = append(completedItems, bridgeItems...)
		if bridgeText != "" {
			assistantExtraNotes = append(assistantExtraNotes, bridgeText)
		}
	}

	if strings.Contains(directiveText, "[inspect]") {
		_, slugs := s.readCatalogModels(catalogPath)
		bareModel := strings.TrimPrefix(th.Model, "openrouter/")
		hasModel := slices.Contains(slugs, th.Model) || slices.Contains(slugs, bareModel)
		resolvedCatalogPath := firstNonEmpty(catalogPath, s.modelCatalogPath)
		if resolvedCatalogPath == "" && s.codexHome != "" {
			tmpCfg := map[string]string{}
			parseTOMLFile(filepath.Join(s.codexHome, "config.toml"), tmpCfg)
			resolvedCatalogPath = tmpCfg["model_catalog_json"]
		}
		resolvedBridge := bridgeURL
		if resolvedBridge == "" && s.codexHome != "" {
			tmpCfg := map[string]string{}
			parseTOMLFile(filepath.Join(s.codexHome, "config.toml"), tmpCfg)
			resolvedBridge = tmpCfg["model_providers.openrouter.base_url"]
		}
		inspectPayload := map[string]any{
			"codexHome":              s.codexHome,
			"cliArgs":                s.cliArgs,
			"threadId":               th.ID,
			"resumed":                th.Resumed,
			"model":                  th.Model,
			"modelProvider":          th.ModelProvider,
			"effort":                 firstNonEmpty(th.ReasoningEffort, "medium"),
			"approvalPolicy":         th.ApprovalPolicy,
			"sandbox":                th.Sandbox,
			"collaborationMode":      th.CollabMode,
			"bridgeBaseUrl":          resolvedBridge,
			"modelCatalogPath":       resolvedCatalogPath,
			"catalogSlugs":           slugs,
			"catalogHasModel":        hasModel,
			"envHasRawOpenRouterKey": os.Getenv("OPENROUTER_API_KEY") != "",
		}
		b, _ := json.Marshal(inspectPayload)
		assistantExtraNotes = append(assistantExtraNotes, "INSPECT_JSON:"+string(b))
	}

	msgID := "msg-" + randHex(4)
	s.sendNotification("item/started", map[string]any{
		"threadId":    th.ID,
		"turnId":      at.turnID,
		"startedAtMs": time.Now().UnixMilli(),
		"item": map[string]any{
			"id":   msgID,
			"type": "agentMessage",
			"text": "",
		},
	})

	var fullAssistant strings.Builder
	emitDelta := func(chunk string) {
		if chunk == "" {
			return
		}
		fullAssistant.WriteString(chunk)
		s.sendNotification("item/agentMessage/delta", map[string]any{
			"threadId": th.ID,
			"turnId":   at.turnID,
			"itemId":   msgID,
			"delta":    chunk,
		})
	}

	if sm := slowDirRe.FindStringSubmatch(directiveText); len(sm) == 2 {
		totalMs, _ := strconv.Atoi(sm[1])
		if totalMs < 80 {
			totalMs = 80
		}
		steps := 8
		emitDelta("chunk-1 ")
		stepDur := time.Duration(totalMs/(steps-1)) * time.Millisecond
		for i := 2; i <= steps; i++ {
			select {
			case <-at.cancelCh:
				s.finishInterrupted(th.ID, at.turnID, completedItems)
				return
			case steerTxt := <-at.steerCh:
				emitDelta(fmt.Sprintf("[STEERED:%s] ", steerTxt))
			case <-time.After(stepDur):
				emitDelta(fmt.Sprintf("chunk-%d ", i))
			}
			for {
				select {
				case steerTxt := <-at.steerCh:
					emitDelta(fmt.Sprintf("[STEERED:%s] ", steerTxt))
				default:
					goto drained
				}
			}
		drained:
		}
	}

	select {
	case <-at.cancelCh:
		s.finishInterrupted(th.ID, at.turnID, completedItems)
		return
	default:
	}

	for {
		select {
		case steerTxt := <-at.steerCh:
			emitDelta(fmt.Sprintf("[STEERED:%s] ", steerTxt))
		default:
			goto doneSteer
		}
	}
doneSteer:

	if th.Ephemeral {
		emitDelta("Completed codex aside for: ")
		emitDelta(directiveText)
	} else {
		emitDelta("Completed codex turn for: ")
		emitDelta(promptText)
	}
	if len(assistantExtraNotes) > 0 {
		emitDelta("\n" + strings.Join(assistantExtraNotes, "\n"))
	}

	agentItem := map[string]any{
		"id":   msgID,
		"type": "agentMessage",
		"text": fullAssistant.String(),
	}
	s.sendNotification("item/completed", map[string]any{
		"threadId":      th.ID,
		"turnId":        at.turnID,
		"completedAtMs": time.Now().UnixMilli(),
		"item":          agentItem,
	})
	completedItems = append(completedItems, agentItem)

	s.sendNotification("thread/tokenUsage/updated", map[string]any{
		"threadId": th.ID,
		"turnId":   at.turnID,
		"tokenUsage": map[string]any{
			"modelContextWindow": 1048576,
			"last": map[string]any{
				"inputTokens":           1500,
				"cachedInputTokens":     800,
				"cacheWriteInputTokens": 0,
				"outputTokens":          90,
				"reasoningOutputTokens": 35,
				"totalTokens":           1590,
			},
			"total": map[string]any{
				"inputTokens":           1500,
				"cachedInputTokens":     800,
				"cacheWriteInputTokens": 0,
				"outputTokens":          90,
				"reasoningOutputTokens": 35,
				"totalTokens":           1590,
			},
		},
	})

	s.stateMu.Lock()
	if cur, ok := s.threads[th.ID]; ok {
		cur.Turns = append(cur.Turns, storedTurn{
			ID:        at.turnID,
			Status:    "completed",
			Items:     completedItems,
			CreatedAt: time.Now().Unix(),
		})
		cur.Preview = promptText
		cur.UpdatedAt = time.Now().Unix()
		s.saveThreadLocked(cur)
	}
	s.stateMu.Unlock()

	s.sendNotification("turn/completed", map[string]any{
		"threadId": th.ID,
		"turn": map[string]any{
			"id":     at.turnID,
			"status": "completed",
			"items":  completedItems,
		},
	})
}

func (s *appServer) finishInterrupted(threadID, turnID string, items []map[string]any) {
	s.sendNotification("turn/completed", map[string]any{
		"threadId": threadID,
		"turn": map[string]any{
			"id":     turnID,
			"status": "interrupted",
			"items":  items,
		},
	})
}

func runShellInDir(ctx context.Context, cwd, shellCmd string) (string, int) {
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cctx, "sh", "-c", shellCmd)
	if cwd != "" {
		cmd.Dir = cwd
	}
	out, err := cmd.CombinedOutput()
	exitCode := 0
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			exitCode = ee.ExitCode()
		} else {
			exitCode = 1
		}
	}
	return string(out), exitCode
}

func (s *appServer) resolveBridgeBearerToken(th storedThread) string {
	_, _, _, thEnvKey, thBearer := extractBridgeAndCatalogFromConfigMap(th.Config)

	s.stateMu.Lock()
	srvBearer := s.bridgeBearerToken
	srvEnvKey := s.bridgeEnvKey
	cfgBearer := s.configOverrides["model_providers.openrouter.experimental_bearer_token"]
	cfgEnvKey := s.configOverrides["model_providers.openrouter.env_key"]
	if th.ModelProvider != "" {
		if cfgBearer == "" {
			cfgBearer = s.configOverrides["model_providers."+th.ModelProvider+".experimental_bearer_token"]
		}
		if cfgEnvKey == "" {
			cfgEnvKey = s.configOverrides["model_providers."+th.ModelProvider+".env_key"]
		}
	}
	s.stateMu.Unlock()

	var tomlBearer, tomlEnvKey string
	if s.codexHome != "" {
		tmpCfg := map[string]string{}
		parseTOMLFile(filepath.Join(s.codexHome, "config.toml"), tmpCfg)
		tomlBearer = tmpCfg["model_providers.openrouter.experimental_bearer_token"]
		tomlEnvKey = tmpCfg["model_providers.openrouter.env_key"]
		if th.ModelProvider != "" {
			if tomlBearer == "" {
				tomlBearer = tmpCfg["model_providers."+th.ModelProvider+".experimental_bearer_token"]
			}
			if tomlEnvKey == "" {
				tomlEnvKey = tmpCfg["model_providers."+th.ModelProvider+".env_key"]
			}
		}
	}

	if explicit := firstNonEmpty(thBearer, srvBearer, cfgBearer, tomlBearer); explicit != "" {
		return explicit
	}
	if envKey := firstNonEmpty(thEnvKey, srvEnvKey, cfgEnvKey, tomlEnvKey); envKey != "" {
		if v := strings.TrimSpace(os.Getenv(envKey)); v != "" {
			return v
		}
	}
	return strings.TrimSpace(os.Getenv("AGENTC_BRIDGE_TOKEN"))
}

func (s *appServer) callLoopbackBridge(ctx context.Context, th storedThread, turnID, promptText, bridgeURL string) (string, []map[string]any, error) {
	resolvedURL := bridgeURL
	if resolvedURL == "" && s.codexHome != "" {
		tmpCfg := map[string]string{}
		parseTOMLFile(filepath.Join(s.codexHome, "config.toml"), tmpCfg)
		resolvedURL = tmpCfg["model_providers.openrouter.base_url"]
	}
	if resolvedURL == "" {
		return "", nil, errors.New("no loopback OpenRouter bridge base_url configured for fakecodex")
	}
	endpoint := strings.TrimRight(resolvedURL, "/")
	if !strings.HasSuffix(endpoint, "/responses") {
		endpoint += "/responses"
	}

	wireModel := th.Model
	reqPayload := map[string]any{
		"model":             wireModel,
		"stream":            true,
		"max_output_tokens": 65536,
		"tools": []map[string]any{
			{
				"type":        "custom",
				"name":        "apply_patch",
				"description": "Patch files in workspace",
			},
		},
		"input": []map[string]any{
			{
				"role": "user",
				"content": []map[string]any{
					{"type": "input_text", "text": promptText},
				},
			},
		},
	}
	bodyBytes, _ := json.Marshal(reqPayload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return "", nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	if tok := s.resolveBridgeBearerToken(th); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}

	hc := &http.Client{
		Timeout:   20 * time.Second,
		Transport: &http.Transport{Proxy: nil},
	}
	resp, err := hc.Do(req)
	if err != nil {
		return "", nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		rawErr, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		return "", nil, fmt.Errorf("bridge HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(rawErr)))
	}

	var notes []string
	var extraItems []map[string]any
	var textAcc strings.Builder

	handleOutputItem := func(item map[string]any) {
		if item == nil {
			return
		}
		typ, _ := item["type"].(string)
		name, _ := item["name"].(string)
		if (typ == "custom_tool_call" || typ == "function_call") && name == "apply_patch" {
			notes = append(notes, "BRIDGED_TOOL_TYPE:"+typ)
			var patchStr string
			if inp, ok := item["input"].(string); ok {
				patchStr = inp
			} else if argsStr, ok := item["arguments"].(string); ok {
				var parsedArgs struct {
					Patch string `json:"patch"`
				}
				if json.Unmarshal([]byte(argsStr), &parsedArgs) == nil && parsedArgs.Patch != "" {
					patchStr = parsedArgs.Patch
				} else {
					patchStr = argsStr
				}
			}
			if patchStr != "" {
				relPath, diffStr := applySimplePatchInDir(th.Cwd, patchStr)
				if relPath != "" {
					fcItem := map[string]any{
						"id":     "fc-br-" + randHex(4),
						"type":   "fileChange",
						"status": "completed",
						"changes": []map[string]any{
							{
								"path": relPath,
								"kind": map[string]any{"type": "add"},
								"diff": diffStr,
							},
						},
					}
					s.sendNotification("item/started", map[string]any{
						"threadId":    th.ID,
						"turnId":      turnID,
						"startedAtMs": time.Now().UnixMilli(),
						"item":        fcItem,
					})
					s.sendNotification("item/completed", map[string]any{
						"threadId":      th.ID,
						"turnId":        turnID,
						"completedAtMs": time.Now().UnixMilli(),
						"item":          fcItem,
					})
					s.sendNotification("turn/diff/updated", map[string]any{
						"threadId": th.ID,
						"turnId":   turnID,
						"diff":     diffStr,
					})
					extraItems = append(extraItems, fcItem)
				}
			}
		}
	}

	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	handledItems := map[string]bool{}
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		dataStr := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if dataStr == "" || dataStr == "[DONE]" {
			continue
		}
		var ev map[string]any
		if json.Unmarshal([]byte(dataStr), &ev) != nil {
			continue
		}
		evType, _ := ev["type"].(string)
		switch evType {
		case "response.output_text.delta":
			if d, ok := ev["delta"].(string); ok {
				textAcc.WriteString(d)
			}
		case "response.output_item.done":
			if item, ok := ev["item"].(map[string]any); ok {
				itemKey := fmt.Sprintf("%v:%v", item["id"], item["call_id"])
				if !handledItems[itemKey] {
					handledItems[itemKey] = true
					handleOutputItem(item)
				}
			}
		case "response.completed":
			if respObj, ok := ev["response"].(map[string]any); ok {
				if outArr, ok := respObj["output"].([]any); ok {
					for _, rawIt := range outArr {
						if it, ok := rawIt.(map[string]any); ok {
							itemKey := fmt.Sprintf("%v:%v", it["id"], it["call_id"])
							if !handledItems[itemKey] {
								handledItems[itemKey] = true
								handleOutputItem(it)
							}
						}
					}
				}
			}
		case "error", "response.failed":
			errMsg := dataStr
			if eObj, ok := ev["error"].(map[string]any); ok {
				if m, ok := eObj["message"].(string); ok && m != "" {
					errMsg = m
				}
			}
			return "", nil, errors.New(errMsg)
		}
	}
	if textAcc.Len() > 0 {
		notes = append(notes, "BRIDGE_TEXT:"+textAcc.String())
	}
	return strings.Join(notes, "\n"), extraItems, nil
}
