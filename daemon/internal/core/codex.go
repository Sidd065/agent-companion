package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"time"

	"agentc/internal/acp"
	"agentc/internal/checkpoints"
	"agentc/internal/codex"
	"agentc/internal/providers"
)

const codexFreshThread = "-"

func (s *Session) isCodexSafe() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.isCodex()
}

func (s *Session) codexThreadIDLocked() string {
	if s.meta.CodexThreadID == codexFreshThread {
		return ""
	}
	if tid := strings.TrimSpace(s.meta.CodexThreadID); tid != "" {
		return tid
	}
	return strings.TrimSpace(s.meta.ID)
}

func (s *Session) codexThreadID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.codexThreadIDLocked()
}

func (m *Manager) resolveCodexThreadID(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return ""
	}
	if s := m.Get(id); s != nil && s.isCodexSafe() {
		return s.codexThreadID()
	}
	return id
}

func (m *Manager) codexBin() string {
	configured := m.cfg.CodexPath
	if m.Providers != nil {
		if ov, ok := m.Providers.Get("codex"); ok && strings.TrimSpace(ov.Command) != "" {
			configured = ov.Command
		}
	}
	return codex.ResolveBinary(configured, m.cfg.HomeDir)
}

// CodexBin returns the resolved codex CLI binary path.
func (m *Manager) CodexBin() string { return m.codexBin() }

func isUnderTmp(p string) bool {
	if p == "" {
		return true
	}
	clean := filepath.Clean(p)
	if clean == "/tmp" || strings.HasPrefix(clean, "/tmp/") {
		return true
	}
	sysTmp := filepath.Clean(os.TempDir())
	if sysTmp != "" && sysTmp != "/" && (clean == sysTmp || strings.HasPrefix(clean, sysTmp+"/")) {
		return true
	}
	return false
}

func nonTmpUserRoot(cfgHome string) string {
	if cfgHome != "" && !isUnderTmp(cfgHome) {
		return filepath.Join(cfgHome, ".cache", "agentc-test-state")
	}
	if h, err := os.UserHomeDir(); err == nil && h != "" && !isUnderTmp(h) {
		return filepath.Join(h, ".cache", "agentc-test-state")
	}
	if u, err := user.Current(); err == nil && u.HomeDir != "" && !isUnderTmp(u.HomeDir) {
		return filepath.Join(u.HomeDir, ".cache", "agentc-test-state")
	}
	return filepath.Join("/var/tmp", fmt.Sprintf("agentc-state-%d", os.Getuid()))
}

// CodexStateDir returns a directory guaranteed to be outside /tmp for CODEX_HOME
// and codex-openrouter-catalog.json. If cfg.StateDir is already outside /tmp, it
// uses cfg.StateDir directly; otherwise it lazily allocates a non-/tmp directory
// cleaned up on Manager.Shutdown().
func (m *Manager) CodexStateDir() string {
	m.codexMu.Lock()
	defer m.codexMu.Unlock()
	if m.codexStateDir != "" {
		return m.codexStateDir
	}
	if m.cfg.StateDir != "" && !isUnderTmp(m.cfg.StateDir) {
		m.codexStateDir = m.cfg.StateDir
		return m.codexStateDir
	}
	root := nonTmpUserRoot(m.cfg.HomeDir)
	dir := filepath.Join(root, "mgr-"+randHex(6))
	_ = os.MkdirAll(dir, 0o700)
	m.codexStateDir = dir
	m.codexStateOwned = true
	return m.codexStateDir
}

// CodexHome returns the managed CODEX_HOME directory (<CodexStateDir>/codex-home) outside /tmp.
func (m *Manager) CodexHome() string {
	home := filepath.Join(m.CodexStateDir(), "codex-home")
	_ = codex.EnsureCodexHome(home, m.cfg.HomeDir)
	return home
}

// CodexHomes returns search directories for Codex rollout files (managed CODEX_HOME + ~/.codex).
func (m *Manager) CodexHomes() []string {
	primary := m.CodexHome()
	out := []string{primary}
	if m.cfg.HomeDir != "" {
		userCodex := filepath.Join(m.cfg.HomeDir, ".codex")
		if filepath.Clean(userCodex) != filepath.Clean(primary) && !isUnderTmp(userCodex) {
			out = append(out, userCodex)
		}
	}
	return out
}

// CodexCatalogPath returns <CodexStateDir>/codex-openrouter-catalog.json outside /tmp.
func (m *Manager) CodexCatalogPath() string {
	return filepath.Join(m.CodexStateDir(), "codex-openrouter-catalog.json")
}

func readTrimmedKeyFile(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		line = strings.TrimPrefix(line, "OPENROUTER_API_KEY=")
		line = strings.Trim(line, `"'`)
		if line != "" {
			return strings.TrimSpace(line)
		}
	}
	return ""
}

// OpenRouterAPIKey resolves the active OpenRouter API key in memory without logging it.
func (m *Manager) OpenRouterAPIKey() string {
	if m.Providers != nil {
		if pc, ok := m.Providers.Get("codex"); ok && pc.Env != nil {
			if v := strings.TrimSpace(pc.Env["OPENROUTER_API_KEY"]); v != "" && v != providers.RedactedSecret {
				return v
			}
		}
	}
	if v := strings.TrimSpace(m.cfg.OpenRouterAPIKey); v != "" && v != providers.RedactedSecret {
		return v
	}
	if m.cfg.Env != nil {
		if v := strings.TrimSpace(m.cfg.Env["OPENROUTER_API_KEY"]); v != "" && v != providers.RedactedSecret {
			return v
		}
	}
	if v := strings.TrimSpace(os.Getenv("OPENROUTER_API_KEY")); v != "" && v != providers.RedactedSecret {
		return v
	}
	if f := strings.TrimSpace(m.cfg.OpenRouterAPIKeyFile); f != "" {
		if v := readTrimmedKeyFile(expandHome(f, m.cfg.HomeDir)); v != "" {
			return v
		}
	}
	if f := strings.TrimSpace(os.Getenv("OPENROUTER_API_KEY_FILE")); f != "" {
		if v := readTrimmedKeyFile(expandHome(f, m.cfg.HomeDir)); v != "" {
			return v
		}
	}
	return ""
}

// OpenRouterConfigured reports whether an OpenRouter API key is available to the daemon.
func (m *Manager) OpenRouterConfigured() bool {
	return m.OpenRouterAPIKey() != ""
}

// CodexBridge returns the shared loopback OpenRouter Responses proxy, keeping its key synced.
func (m *Manager) CodexBridge() (*codex.Bridge, error) {
	key := m.OpenRouterAPIKey()
	m.codexMu.Lock()
	defer m.codexMu.Unlock()
	if m.codexBridge != nil {
		m.codexBridge.SetAPIKey(key)
		return m.codexBridge, nil
	}
	br, err := codex.NewBridge(key, m.cfg.OpenRouterBaseURL, 0)
	if err != nil {
		return nil, err
	}
	m.codexBridge = br
	return br, nil
}

// CodexCatalog returns the shared OpenRouter/Codex catalog manager.
func (m *Manager) CodexCatalog() *codex.Catalog {
	catPath := m.CodexCatalogPath()
	m.codexMu.Lock()
	defer m.codexMu.Unlock()
	if m.codexCatalog == nil {
		m.codexCatalog = codex.NewCatalog(catPath, m.cfg.OpenRouterBaseURL)
	}
	return m.codexCatalog
}

func (m *Manager) shutdownCodex() {
	m.codexMu.Lock()
	br := m.codexBridge
	m.codexBridge = nil
	ownedDir := ""
	if m.codexStateOwned {
		ownedDir = m.codexStateDir
		m.codexStateDir = ""
		m.codexStateOwned = false
	}
	m.codexMu.Unlock()

	if br != nil {
		_ = br.Close()
	}
	if ownedDir != "" && !isUnderTmp(ownedDir) && strings.Contains(ownedDir, "agentc-test-state") {
		_ = os.RemoveAll(ownedDir)
	}
}

// OnProviderUpdated refreshes cached OpenRouter credentials, catalog, and agent profiles.
func (m *Manager) OnProviderUpdated(id string) {
	m.codexMu.Lock()
	br := m.codexBridge
	cat := m.codexCatalog
	m.codexMu.Unlock()
	if br != nil {
		br.SetAPIKey(m.OpenRouterAPIKey())
	}
	if cat != nil {
		cat.Invalidate()
	}
	m.InvalidateAgentsCache()
}

// ProviderHealthEnv builds the environment slice passed to providers.CheckHealth (never sent over RPC).
func (m *Manager) ProviderHealthEnv(id string) []string {
	env := m.agyEnv()
	if m.Providers != nil {
		if pc, ok := m.Providers.Get(id); ok {
			env = providers.MergeEnv(env, pc.Env)
		}
	}
	if id == "codex" {
		extra := map[string]string{
			"CODEX_HOME": m.CodexHome(),
		}
		if key := m.OpenRouterAPIKey(); key != "" {
			extra["OPENROUTER_API_KEY"] = key
		}
		env = providers.MergeEnv(env, extra)
	}
	return env
}

func (m *Manager) codexCustomModelInputs() ([]codex.CustomModelInput, []providers.CustomModel, string) {
	var customIn []codex.CustomModelInput
	customOut := []providers.CustomModel{}
	defaultModel := ""
	if m.Providers != nil {
		if pc, ok := m.Providers.Get("codex"); ok {
			defaultModel = strings.TrimSpace(pc.DefaultModel)
			for _, cm := range pc.CustomModels {
				slug := strings.TrimSpace(cm.Slug)
				if slug == "" {
					continue
				}
				name := firstNonEmpty(strings.TrimSpace(cm.Name), slug)
				customIn = append(customIn, codex.CustomModelInput{Slug: slug, Name: name})
				customOut = append(customOut, providers.CustomModel{Slug: slug, Name: name})
			}
		}
	}
	return customIn, customOut, defaultModel
}

// ProviderModels implements the Protocol v2 `providers.models` RPC.
func (m *Manager) ProviderModels(ctx context.Context, provider string, refresh bool) (providers.ProviderModelsResult, error) {
	prov := strings.ToLower(strings.TrimSpace(provider))
	if prov == "" {
		prov = "codex"
	}

	if prov != "codex" {
		res := providers.ProviderModelsResult{
			Provider:             prov,
			OpenRouterConfigured: m.OpenRouterConfigured(),
			NativeAuthConfigured: false,
			Models:               []providers.ProviderModelInfo{},
			CustomModels:         []providers.CustomModel{},
			AvailableModes:       []providers.ProviderModeInfo{},
			ReasoningEfforts:     []string{"low", "medium", "high"},
		}
		if m.Providers != nil {
			if pc, ok := m.Providers.Get(prov); ok {
				res.DefaultModelID = pc.DefaultModel
				if len(pc.CustomModels) > 0 {
					res.CustomModels = append([]providers.CustomModel(nil), pc.CustomModels...)
				}
			}
		}
		return res, nil
	}

	apiKey := m.OpenRouterAPIKey()
	hasOR := apiKey != ""
	codexEnv := m.ProviderHealthEnv("codex")
	hasNative := codex.HasNativeOpenAIAuth(m.CodexHome(), m.cfg.HomeDir, codexEnv)
	customIn, customOut, cfgDefault := m.codexCustomModelInputs()

	var nativeModels []codex.CatalogModel
	if hasNative {
		if nm, err := codex.NativeModelsRPC(ctx, m.codexControlConfig(false)); err == nil && len(nm) > 0 {
			nativeModels = nm
		} else {
			nativeModels = codex.CuratedNativeCodexModels()
		}
	}

	cat := m.CodexCatalog()
	catModels, _ := cat.FetchModels(ctx, apiKey, refresh, customIn, nativeModels, hasNative)

	modelsOut := make([]providers.ProviderModelInfo, 0, len(catModels))
	for _, cm := range catModels {
		modelsOut = append(modelsOut, providers.ProviderModelInfo{
			ModelID:           cm.ModelID,
			Name:              cm.Name,
			Description:       cm.Description,
			Provider:          cm.Provider,
			ContextWindow:     cm.ContextWindow,
			SupportsReasoning: cm.SupportsReasoning,
			SupportsTools:     cm.SupportsTools,
			SupportsImages:    cm.SupportsImages,
			IsCustom:          cm.IsCustom,
		})
	}

	modes := codex.AvailableModes()
	modesOut := make([]providers.ProviderModeInfo, 0, len(modes))
	for _, md := range modes {
		modesOut = append(modesOut, providers.ProviderModeInfo{ID: md.ID, Name: md.Name})
	}

	return providers.ProviderModelsResult{
		Provider:             "codex",
		DefaultModelID:       codex.DefaultModelID(cfgDefault, hasOR, hasNative),
		OpenRouterConfigured: hasOR,
		NativeAuthConfigured: hasNative,
		Models:               modelsOut,
		CustomModels:         customOut,
		AvailableModes:       modesOut,
		ReasoningEfforts:     codex.ReasoningEfforts(),
	}, nil
}

func (m *Manager) codexHomeForThread(threadID string) string {
	primary := m.CodexHome()
	tid := strings.TrimSpace(threadID)
	if tid == "" || tid == codexFreshThread {
		return primary
	}
	if _, _, ok := codex.FindRolloutPath(tid, primary); ok {
		return primary
	}
	for _, h := range m.CodexHomes() {
		if h == "" || filepath.Clean(h) == filepath.Clean(primary) || isUnderTmp(h) {
			continue
		}
		if _, _, ok := codex.FindRolloutPath(tid, h); ok {
			_ = codex.EnsureCodexHome(h, m.cfg.HomeDir)
			return h
		}
	}
	return primary
}

func (m *Manager) resolveCodexRouting(rawModel, forcedProvider string, codexHome ...string) (useOpenRouter bool, wireModel, displayModel string) {
	home := ""
	if len(codexHome) > 0 {
		home = strings.TrimSpace(codexHome[0])
	}
	if home == "" {
		home = m.CodexHome()
	}
	_, _, cfgDefault := m.codexCustomModelInputs()
	hasOR := m.OpenRouterConfigured()
	hasNative := codex.HasNativeOpenAIAuth(home, m.cfg.HomeDir, m.ProviderHealthEnv("codex"))
	return codex.ResolveProviderAndModel(rawModel, cfgDefault, forcedProvider, hasOR, hasNative)
}

func (m *Manager) codexThreadUseOpenRouter(threadID string) bool {
	tid := strings.TrimSpace(threadID)
	home := m.codexHomeForThread(tid)
	var rawModel, forcedProvider string
	if tid != "" {
		if s := m.Get(tid); s != nil && s.isCodexSafe() {
			forcedProvider = s.Provider()
			rawModel = strings.TrimSpace(s.Summary().Model)
		} else {
			for _, s := range m.all() {
				if s.isCodexSafe() && s.codexThreadID() == tid {
					forcedProvider = s.Provider()
					rawModel = strings.TrimSpace(s.Summary().Model)
					break
				}
			}
		}
		if forcedProvider == "" && rawModel == "" {
			if rs, ok := codex.ReadRolloutSummary(tid, home); ok {
				forcedProvider = rs.ModelProvider
				rawModel = rs.Model
			}
		}
	}
	useOR, _, _ := m.resolveCodexRouting(rawModel, forcedProvider, home)
	return useOR
}

func (m *Manager) codexControlConfigForThread(useOpenRouter bool, threadID string) codex.ProcConfig {
	cfg := m.codexControlConfig(useOpenRouter)
	cfg.CodexHome = m.codexHomeForThread(threadID)
	return cfg
}

func parseCodexExtraArgsRouting(extraArgs []string) (provider, model string) {
	unquote := func(v string) string {
		return strings.Trim(strings.TrimSpace(v), `"'`)
	}
	parseKV := func(kv string) {
		eq := strings.IndexByte(kv, '=')
		if eq <= 0 {
			return
		}
		k := strings.TrimSpace(kv[:eq])
		v := unquote(kv[eq+1:])
		switch k {
		case "model_provider":
			if v != "" {
				provider = v
			}
		case "model":
			if v != "" {
				model = v
			}
		}
	}
	for i := 0; i < len(extraArgs); i++ {
		a := strings.TrimSpace(extraArgs[i])
		switch {
		case (a == "-c" || a == "--config") && i+1 < len(extraArgs):
			parseKV(extraArgs[i+1])
			i++
		case strings.HasPrefix(a, "-c="):
			parseKV(strings.TrimPrefix(a, "-c="))
		case strings.HasPrefix(a, "--config="):
			parseKV(strings.TrimPrefix(a, "--config="))
		case (a == "-m" || a == "--model") && i+1 < len(extraArgs):
			if v := unquote(extraArgs[i+1]); v != "" {
				model = v
			}
			i++
		case strings.HasPrefix(a, "-m="):
			if v := unquote(strings.TrimPrefix(a, "-m=")); v != "" {
				model = v
			}
		case strings.HasPrefix(a, "--model="):
			if v := unquote(strings.TrimPrefix(a, "--model=")); v != "" {
				model = v
			}
		}
	}
	return provider, model
}

func (m *Manager) codexControlConfig(useOpenRouter bool) codex.ProcConfig {
	env := m.agyEnv()
	var extraArgs []string
	if m.Providers != nil {
		if pc, ok := m.Providers.Get("codex"); ok {
			env = providers.MergeEnv(env, pc.Env)
			extraArgs = append(extraArgs, pc.Args...)
		}
	}
	var bridgeURL, bridgeTok, catalogPath string
	if useOpenRouter && m.OpenRouterConfigured() {
		if br, err := m.CodexBridge(); err == nil {
			bridgeURL = br.BaseURL()
			bridgeTok = br.BridgeToken()
		}
		cat := m.CodexCatalog()
		customIn, _, cfgDefault := m.codexCustomModelInputs()
		cat.SetCustomModels(customIn)
		defSlug := codex.DefaultModelID(cfgDefault, true, false)
		if _, path, err := cat.EnsureModel(defSlug); err == nil && path != "" {
			catalogPath = path
		} else {
			catalogPath = cat.CatalogPath()
		}
	} else {
		useOpenRouter = false
	}
	return codex.ProcConfig{
		Bin:           m.codexBin(),
		Cwd:           m.cfg.HomeDir,
		CodexHome:     m.CodexHome(),
		UserHome:      m.cfg.HomeDir,
		Env:           env,
		UseOpenRouter: useOpenRouter,
		BridgeBaseURL: bridgeURL,
		BridgeToken:   bridgeTok,
		CatalogPath:   catalogPath,
		ExtraArgs:     extraArgs,
	}
}

func (m *Manager) startCodexAgent(s *Session) (*codex.Agent, error) {
	s.mu.Lock()
	cwd := s.meta.Cwd
	addDirs := append([]string(nil), s.meta.AddDirs...)
	skip := s.meta.SkipPerms
	approval := s.meta.ApprovalPolicy
	sandboxMode := s.meta.SandboxMode
	if sandboxMode == "" && s.meta.Sandbox {
		sandboxMode = "workspace-write"
	}
	providerPref := s.meta.Provider
	wantModel := s.meta.WantModel
	wantThought := s.meta.WantThought
	wantMode := s.meta.WantMode
	threadID := s.codexThreadIDLocked()
	extra := append([]string(nil), s.meta.ExtraArgs...)
	hasItems := len(s.tr.Items) > 0 && !s.meta.NeedsReload
	s.mu.Unlock()

	bin := m.codexBin()
	env := m.agyEnv()
	if m.Providers != nil {
		if ov, ok := m.Providers.Get("codex"); ok {
			if ov.Command != "" {
				bin = codex.ResolveBinary(ov.Command, m.cfg.HomeDir)
			}
			env = providers.MergeEnv(env, ov.Env)
			extra = append(append([]string(nil), ov.Args...), extra...)
		}
	}

	br, err := m.CodexBridge()
	if err != nil {
		return nil, err
	}
	cat := m.CodexCatalog()
	customIn, _, cfgDefault := m.codexCustomModelInputs()
	codexHome := m.codexHomeForThread(threadID)
	hasOR := m.OpenRouterConfigured()
	hasNative := codex.HasNativeOpenAIAuth(codexHome, m.cfg.HomeDir, m.ProviderHealthEnv("codex"))

	return codex.Start(codex.Options{
		Bin:             bin,
		Env:             env,
		Cwd:             cwd,
		AddDirs:         addDirs,
		CodexHome:       codexHome,
		UserHome:        m.cfg.HomeDir,
		SkipPermissions: skip,
		ApprovalPolicy:  approval,
		SandboxMode:     sandboxMode,
		Provider:        providerPref,
		DefaultModel:    cfgDefault,
		Model:           wantModel,
		Effort:          wantThought,
		Mode:            wantMode,
		ExtraArgs:       extra,
		ExcludeTurns:    hasItems,
		Bridge:          br,
		Catalog:         cat,
		CustomModels:    customIn,
		HasOpenRouter:   hasOR,
		HasNativeAuth:   hasNative,
		Handler:         procHandler{s: s},
	}), nil
}

// CodexTermLaunch builds the command and tmux environment variables for `codex` TUI terminals.
func (m *Manager) CodexTermLaunch(convID string, skip bool, extraArgs []string) (bin string, args []string, extraEnv map[string]string) {
	bin = m.codexBin()
	resumeID := m.resolveCodexThreadID(convID)
	var sessProv, sessModel, rolloutProv, rolloutModel string
	if s := m.Get(strings.TrimSpace(convID)); s != nil && s.isCodexSafe() {
		sessProv = s.Provider()
		sessModel = strings.TrimSpace(s.Summary().Model)
		s.mu.Lock()
		sessHasHistory := s.meta.Turns > 0 || len(s.tr.Items) > 0
		s.mu.Unlock()
		if resumeID != "" && !sessHasHistory && !codex.HasThread(resumeID, m.CodexHomes()...) {
			resumeID = ""
		}
	}
	codexHome := m.codexHomeForThread(resumeID)
	extraEnv = map[string]string{}

	var combinedExtraArgs []string
	if m.Providers != nil {
		if pc, ok := m.Providers.Get("codex"); ok {
			for _, a := range pc.Args {
				t := strings.TrimSpace(a)
				if t == "" || t == "acp" || t == "app-server" {
					continue
				}
				combinedExtraArgs = append(combinedExtraArgs, a)
			}
			for k, v := range pc.Env {
				k = strings.TrimSpace(k)
				if k == "" || v == providers.RedactedSecret {
					continue
				}
				if strings.EqualFold(k, "OPENROUTER_API_KEY") ||
					strings.EqualFold(k, "OPENROUTER_API_KEY_FILE") ||
					strings.EqualFold(k, "AGENTC_BRIDGE_TOKEN") ||
					strings.EqualFold(k, "CODEX_HOME") {
					continue
				}
				extraEnv[k] = v
			}
		}
	}
	combinedExtraArgs = append(combinedExtraArgs, extraArgs...)
	extraEnv["CODEX_HOME"] = codexHome

	if filepath.IsAbs(bin) {
		binDir := filepath.Dir(bin)
		basePath := strings.TrimSpace(extraEnv["PATH"])
		if basePath == "" {
			for _, kv := range m.agyEnv() {
				if strings.HasPrefix(kv, "PATH=") {
					basePath = kv[len("PATH="):]
					break
				}
			}
		}
		if basePath == "" {
			basePath = os.Getenv("PATH")
		}
		sep := string(os.PathListSeparator)
		if basePath == "" {
			extraEnv["PATH"] = binDir
		} else if basePath != binDir && !strings.HasPrefix(basePath, binDir+sep) {
			extraEnv["PATH"] = binDir + sep + basePath
		} else {
			extraEnv["PATH"] = basePath
		}
	}

	argProv, argModel := parseCodexExtraArgsRouting(combinedExtraArgs)
	if resumeID != "" {
		if rs, ok := codex.ReadRolloutSummary(resumeID, codexHome); ok {
			rolloutProv = rs.ModelProvider
			rolloutModel = rs.Model
		}
	}
	forcedProv := firstNonEmpty(argProv, sessProv, rolloutProv)
	rawModel := firstNonEmpty(argModel, sessModel, rolloutModel)
	customIn, _, cfgDefault := m.codexCustomModelInputs()
	useOR, wireModel, _ := m.resolveCodexRouting(rawModel, forcedProv, codexHome)

	if useOR && m.OpenRouterConfigured() {
		if br, err := m.CodexBridge(); err == nil {
			cat := m.CodexCatalog()
			cat.SetCustomModels(customIn)
			defSlug := codex.DefaultModelID(cfgDefault, true, false)
			if rawModel != "" {
				_, _, _ = cat.EnsureModel(rawModel)
			}
			if wireModel != "" {
				_, _, _ = cat.EnsureModel(wireModel)
			}
			_, catPath, _ := cat.EnsureModel(defSlug)
			args = append(args, codex.OpenRouterConfigOverrides(br.BaseURL(), catPath)...)
			extraEnv["AGENTC_BRIDGE_TOKEN"] = br.BridgeToken()
		}
	}

	if argModel == "" && wireModel != "" && ((useOR && m.OpenRouterConfigured()) || rawModel != "" || strings.TrimSpace(cfgDefault) != "") {
		args = append(args, "-c", fmt.Sprintf("model=%q", wireModel))
	}

	hasBypass := false
	for _, a := range combinedExtraArgs {
		if a == "--dangerously-bypass-approvals-and-sandbox" || a == "--full-auto" {
			hasBypass = true
		}
	}
	if skip && !hasBypass {
		args = append(args, "--dangerously-bypass-approvals-and-sandbox")
	}
	if resumeID != "" {
		args = append(args, "resume", resumeID)
	}
	for _, a := range combinedExtraArgs {
		if strings.TrimSpace(a) != "" {
			args = append(args, a)
		}
	}
	return bin, args, extraEnv
}

// ---- Codex History, Open, Fork, RevertPreview, Rewind, Rename, Archive, Delete, Aside ----

type codexLocalAnnotation struct {
	Title    string `json:"title,omitempty"`
	Archived *bool  `json:"archived,omitempty"`
	Pinned   *bool  `json:"pinned,omitempty"`
}

func (m *Manager) codexAnnotationsFile() string {
	return filepath.Join(m.cfg.StateDir, "codex-annotations.json")
}

func (m *Manager) loadCodexAnnotations() map[string]codexLocalAnnotation {
	b, err := os.ReadFile(m.codexAnnotationsFile())
	if err != nil {
		return map[string]codexLocalAnnotation{}
	}
	var out map[string]codexLocalAnnotation
	if json.Unmarshal(b, &out) != nil || out == nil {
		return map[string]codexLocalAnnotation{}
	}
	return out
}

func (m *Manager) saveCodexAnnotations(ann map[string]codexLocalAnnotation) error {
	b, err := json.MarshalIndent(ann, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(m.codexAnnotationsFile(), b)
}

func (m *Manager) isCodexID(id string) bool {
	if s := m.Get(id); s != nil {
		return s.isCodexSafe()
	}
	for _, s := range m.all() {
		if s.isCodexSafe() && s.codexThreadID() == id {
			return true
		}
	}
	return codex.HasThread(id, m.CodexHomes()...)
}

func (m *Manager) codexHistory() []HistEntry {
	anns := m.loadCodexAnnotations()
	aliased := map[string]bool{}
	for _, s := range m.all() {
		if s.isCodexSafe() {
			if tid := s.codexThreadID(); tid != "" && tid != s.ID() {
				aliased[tid] = true
			}
		}
	}
	summaries := codex.ScanRolloutThreads(m.CodexHomes()...)
	out := make([]HistEntry, 0, len(summaries))
	for _, rs := range summaries {
		if aliased[rs.ID] {
			continue
		}
		e := HistEntry{
			ID:        rs.ID,
			Agent:     "codex",
			Title:     firstNonEmpty(rs.Title, "Codex session"),
			Cwd:       rs.Cwd,
			UpdatedAt: rs.UpdatedAt,
			Archived:  rs.Archived,
			Steps:     rs.Steps,
		}
		if a, ok := anns[rs.ID]; ok {
			if a.Title != "" {
				e.Title = a.Title
			}
			if a.Archived != nil {
				e.Archived = *a.Archived
			}
			if a.Pinned != nil {
				e.Pinned = *a.Pinned
			}
		}
		out = append(out, e)
	}
	return out
}

func (m *Manager) findCodexHist(id string) *HistEntry {
	for _, e := range m.codexHistory() {
		if e.ID == id {
			cp := e
			return &cp
		}
	}
	return nil
}

func (m *Manager) openCodex(ctx context.Context, h *HistEntry, flags *Flags) (*Session, error) {
	cwd := h.Cwd
	if st, err := os.Stat(cwd); cwd == "" || err != nil || !st.IsDir() {
		cwd = m.cfg.HomeDir
	}
	threadHome := m.codexHomeForThread(h.ID)
	rs, _ := codex.ReadRolloutSummary(h.ID, threadHome)
	rolloutProv := strings.ToLower(strings.TrimSpace(rs.ModelProvider))
	hasNative := codex.HasNativeOpenAIAuth(threadHome, m.cfg.HomeDir, m.ProviderHealthEnv("codex"))
	_, _, cfgDefault := m.codexCustomModelInputs()
	defModel := codex.DefaultModelID(cfgDefault, m.OpenRouterConfigured(), hasNative)
	if rs.Model != "" {
		defModel = rs.Model
	} else if rolloutProv == "openai" || rolloutProv == "native" {
		defModel = codex.DefaultModelID(cfgDefault, false, true)
	}

	meta := SessionMeta{
		ID:            h.ID,
		CodexThreadID: h.ID,
		Agent:         "codex",
		Cwd:           cwd,
		SkipPerms:     m.cfg.DefaultSkip,
		Provider:      rolloutProv,
		Title:         firstNonEmpty(h.Title, "Codex session"),
		CreatedAt:     h.UpdatedAt,
		UpdatedAt:     h.UpdatedAt,
		Origin:        "history",
		Archived:      h.Archived,
		Pinned:        h.Pinned,
		WantModel:     defModel,
		NeedsReload:   true,
	}
	if flags != nil {
		f := *flags
		f.Model, f.Mode, f.Thought, f.Effort, f.AgentName = "", "", "", "", ""
		m.applyFlags(&meta, &f)
	}
	s := newSession(m, meta)

	useOR, _, _ := m.resolveCodexRouting(meta.WantModel, meta.Provider, threadHome)
	// Hydrate transcript from thread/read if rollout exists so opening sleeping session is immediate.
	if thr, err := codex.ReadThreadRPC(ctx, m.codexControlConfigForThread(useOR, h.ID), h.ID, true); err == nil && thr != nil {
		s.mu.Lock()
		s.replaying = true
		s.mu.Unlock()
		codex.ReplayThreadUpdates(thr, cwd, func(u map[string]any) {
			if raw, jerr := json.Marshal(u); jerr == nil {
				s.onUpdate(raw)
			}
		})
		s.mu.Lock()
		s.replaying = false
		s.meta.NeedsReload = false
		if thr.Name != nil && *thr.Name != "" {
			s.meta.Title = *thr.Name
		}
		if thr.ModelProvider != "" && (flags == nil || flags.Provider == "") && s.meta.Provider == "" {
			s.meta.Provider = strings.ToLower(strings.TrimSpace(thr.ModelProvider))
		}
		if thr.Model != nil && *thr.Model != "" {
			s.meta.WantModel = *thr.Model
			s.ensureAvailableModelLocked(*thr.Model)
		}
		if thr.CreatedAt > 0 {
			s.meta.CreatedAt = thr.CreatedAt * 1000
		}
		if thr.UpdatedAt > 0 {
			s.meta.UpdatedAt = thr.UpdatedAt * 1000
		}
		s.mu.Unlock()
	}

	m.mu.Lock()
	if existing := m.sessions[h.ID]; existing != nil {
		m.mu.Unlock()
		return existing, nil
	}
	m.sessions[h.ID] = s
	m.mu.Unlock()
	m.saveSession(s)
	m.emitSummary(s)
	return s, nil
}

// codexResolveCut maps a transcript itemID to (turnZeroBasedIndex, prefillText, cutItemIdx, isFirstMessage, error).
// turnZeroBasedIndex is the 0-indexed turn strictly BEFORE which to cut (so turnZeroBasedIndex == 0 means before turn 1).
func (s *Session) codexResolveCut(itemID string) (turnIdx int, prefill string, cutItemIdx int, isFirst bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if itemID == "" {
		return -1, "", len(s.tr.Items), false, nil
	}

	pos := -1
	for i, it := range s.tr.Items {
		if it.ID == itemID {
			pos = i
			break
		}
	}
	if pos < 0 {
		return -1, "", -1, false, errors.New("message not found in transcript")
	}

	targetPos := -1
	if s.tr.Items[pos].Kind == "user" {
		targetPos = pos
	} else {
		for i := pos + 1; i < len(s.tr.Items); i++ {
			if it := s.tr.Items[i]; it.Kind == "user" && it.State != "queued" && it.State != "failed" {
				targetPos = i
				break
			}
		}
	}

	if targetPos < 0 {
		// Cutting after the final turn -> full conversation.
		return -1, "", len(s.tr.Items), false, nil
	}

	userOrd := 0
	for i := 0; i < targetPos; i++ {
		it := s.tr.Items[i]
		if it.Kind == "user" && it.State != "queued" && it.State != "failed" {
			userOrd++
		}
	}
	prefill = s.tr.Items[targetPos].Text
	if userOrd == 0 {
		return 0, prefill, 0, true, nil
	}
	return userOrd, prefill, targetPos, false, nil
}

func (m *Manager) codexResolveBeforeTurnID(ctx context.Context, threadID string, turnIdx int) string {
	if turnIdx < 0 || threadID == "" {
		return ""
	}
	useOR := m.codexThreadUseOpenRouter(threadID)
	thr, err := codex.ReadThreadRPC(ctx, m.codexControlConfigForThread(useOR, threadID), threadID, true)
	if err != nil || thr == nil || turnIdx >= len(thr.Turns) {
		return ""
	}
	return thr.Turns[turnIdx].ID
}

func (m *Manager) forkCodex(ctx context.Context, src *Session, itemID string) (*Session, string, error) {
	turnIdx, prefill, cutItemIdx, isFirst, err := src.codexResolveCut(itemID)
	if err != nil {
		return nil, prefill, err
	}
	if isFirst {
		return nil, prefill, errors.New("this is the first message; start a new session instead")
	}

	src.mu.Lock()
	base := src.meta
	isCodex := src.isCodex()
	srcThreadID := ""
	if isCodex {
		srcThreadID = src.codexThreadIDLocked()
	}
	srcCheckpoints := append([]checkpoints.TurnCheckpoint(nil), src.meta.Checkpoints...)
	proc := src.proc
	var clonedItems []*Item
	limit := cutItemIdx
	if limit < 0 || limit > len(src.tr.Items) {
		limit = len(src.tr.Items)
	}
	for _, it := range src.tr.Items[:limit] {
		if it.Kind == "user" && it.State == "queued" {
			continue
		}
		c := cloneItem(it)
		if c.Kind == "perm" && c.State == "pending" {
			c.State = "cancelled"
		}
		clonedItems = append(clonedItems, c)
	}
	src.mu.Unlock()

	beforeTurnID := ""
	if isCodex && turnIdx >= 0 && srcThreadID != "" {
		beforeTurnID = m.codexResolveBeforeTurnID(ctx, srcThreadID, turnIdx)
	}

	var newID, codexTID, seedHandoff string
	if isCodex {
		if ca, ok := proc.(*codex.Agent); ok && ca.Alive() && srcThreadID != "" {
			var forked codex.Thread
			if err := ca.ACP().Call(ctx, "_agentc/fork", map[string]any{
				"beforeTurnId": beforeTurnID,
				"cwd":          base.Cwd,
			}, &forked); err == nil && forked.ID != "" {
				newID = forked.ID
				codexTID = forked.ID
			}
		}
	}
	if isCodex && newID == "" && srcThreadID != "" && codex.HasThread(srcThreadID, m.CodexHomes()...) {
		srcHome := m.codexHomeForThread(srcThreadID)
		useOR, wireModel, _ := m.resolveCodexRouting(base.WantModel, base.Provider, srcHome)
		if forked, ferr := codex.ForkThreadRPC(ctx, m.codexControlConfigForThread(useOR, srcThreadID), srcThreadID, beforeTurnID, base.Cwd, wireModel); ferr == nil && forked != nil && forked.ID != "" {
			newID = forked.ID
			codexTID = forked.ID
		}
	}
	if newID == "" {
		// Fallback when session has not yet materialized a rollout file on disk.
		newID = fmt.Sprintf("019c0000-%s-7000-8000-%s", randHex(2), randHex(6))
		codexTID = codexFreshThread
		if len(clonedItems) > 0 {
			seedHandoff = BuildPortableHandoff(base.Title, clonedItems, srcCheckpoints)
		}
	}

	title := "Fork: " + firstNonEmpty(base.Title, "conversation")
	ts := now()
	var modelsCopy *acp.Models
	if base.Models != nil {
		mc := *base.Models
		mc.AvailableModels = append([]acp.Model(nil), base.Models.AvailableModels...)
		modelsCopy = &mc
	}
	var modesCopy *acp.Modes
	if base.Modes != nil {
		mdc := *base.Modes
		mdc.AvailableModes = append([]acp.Mode(nil), base.Modes.AvailableModes...)
		modesCopy = &mdc
	}
	meta := SessionMeta{
		ID:             newID,
		CodexThreadID:  codexTID,
		SeedHandoff:    seedHandoff,
		Agent:          firstNonEmpty(base.Agent, "codex"),
		Cwd:            base.Cwd,
		AddDirs:        append([]string(nil), base.AddDirs...),
		SkipPerms:      base.SkipPerms,
		Sandbox:        base.Sandbox,
		SandboxMode:    base.SandboxMode,
		ApprovalPolicy: base.ApprovalPolicy,
		Provider:       base.Provider,
		AgentName:      base.AgentName,
		NoTelemetry:    base.NoTelemetry,
		ExtraArgs:      append([]string(nil), base.ExtraArgs...),
		Title:          title,
		TitleLocked:    true,
		CreatedAt:      ts,
		UpdatedAt:      ts,
		Origin:         "fork",
		ForkedFrom:     base.ID,
		ProjectID:      base.ProjectID,
		WorktreePath:   base.WorktreePath,
		Branch:         base.Branch,
		WantModel:      base.WantModel,
		WantMode:       base.WantMode,
		WantThought:    base.WantThought,
		Models:         modelsCopy,
		Modes:          modesCopy,
		ConfigOptions:  append([]acp.ConfigOption(nil), base.ConfigOptions...),
		Commands:       append([]acp.Command(nil), base.Commands...),
		Turns:          base.Turns,
	}
	if turnIdx > 0 && turnIdx < meta.Turns {
		meta.Turns = turnIdx
	}

	ns := newSession(m, meta)
	ns.tr.LoadItems(clonedItems, int64(len(clonedItems)))
	m.mu.Lock()
	m.sessions[newID] = ns
	m.mu.Unlock()
	m.saveSession(ns)
	m.invalidateHistory()
	m.emitSummary(ns)
	return ns, prefill, nil
}

func (m *Manager) revertPreviewCheckpoints(ctx context.Context, s *Session, itemID string) (json.RawMessage, int, error) {
	turnIdx, _, _, isFirst, err := s.codexResolveCut(itemID)
	if err != nil {
		return nil, 0, err
	}

	s.mu.Lock()
	cwd := s.meta.Cwd
	turns := s.meta.Turns
	cps := append([]checkpoints.TurnCheckpoint(nil), s.meta.Checkpoints...)
	s.mu.Unlock()

	if turnIdx < 0 && !isFirst {
		raw, _ := json.Marshal(map[string]any{"canRewind": true, "filesChanged": []string{}, "files": []any{}})
		return raw, turns, nil
	}

	targetTurn := turnIdx + 1 // 1-indexed turn whose PreRef represents state right before it
	if isFirst {
		targetTurn = 1
	}

	if len(cps) == 0 {
		raw, _ := json.Marshal(map[string]any{"canRewind": true, "filesChanged": []string{}, "files": []any{}})
		return raw, max(0, targetTurn-1), nil
	}

	var fromRef string
	if targetTurn >= 1 {
		for _, cp := range cps {
			if cp.TurnIndex >= targetTurn {
				fromRef = cp.PreRef
				break
			}
		}
	}
	if fromRef == "" {
		raw, _ := json.Marshal(map[string]any{"canRewind": true, "filesChanged": []string{}, "files": []any{}})
		return raw, max(0, targetTurn-1), nil
	}
	toRef := cps[len(cps)-1].PostRef
	diff, derr := checkpoints.Diff(ctx, cwd, "revert-preview", fromRef, toRef)
	if derr != nil {
		raw, _ := json.Marshal(map[string]any{"canRewind": true, "filesChanged": []string{}, "files": []any{}})
		return raw, max(0, targetTurn-1), nil
	}
	names := make([]string, 0, len(diff.Files))
	for _, f := range diff.Files {
		names = append(names, f.Path)
	}
	raw, _ := json.Marshal(map[string]any{
		"canRewind":    true,
		"filesChanged": names,
		"files":        diff.Files,
		"totalAdd":     diff.TotalAdd,
		"totalDel":     diff.TotalDel,
	})
	return raw, max(0, targetTurn-1), nil
}

func (m *Manager) rewindCodex(ctx context.Context, s *Session, itemID string, conversationOnly bool) (string, error) {
	turnIdx, prefill, cutItemIdx, isFirst, err := s.codexResolveCut(itemID)
	if err != nil {
		return prefill, err
	}
	if turnIdx < 0 && !isFirst {
		// Nothing after itemID to rewind.
		return prefill, nil
	}

	s.mu.Lock()
	id := s.meta.ID
	isCodex := s.isCodex()
	threadID := ""
	if isCodex {
		threadID = s.codexThreadIDLocked()
	}
	cwd := s.meta.Cwd
	wantModel := s.meta.WantModel
	providerPref := s.meta.Provider
	cps := append([]checkpoints.TurnCheckpoint(nil), s.meta.Checkpoints...)
	proc := s.proc
	s.mu.Unlock()

	targetTurn := turnIdx + 1
	if isFirst {
		targetTurn = 1
	}

	filesRestored := 0
	if !conversationOnly && len(cps) > 0 {
		var targetPreRef string
		for _, cp := range cps {
			if cp.TurnIndex >= targetTurn {
				targetPreRef = cp.PreRef
				filesRestored += len(cp.Files)
				break
			}
		}
		if targetPreRef != "" {
			if _, rerr := checkpoints.Revert(ctx, cwd, id, targetPreRef); rerr != nil {
				return prefill, fmt.Errorf("restore files: %w", rerr)
			}
			if m.Git != nil {
				m.Git.Invalidate(cwd)
			}
		}
	}

	if !isCodex {
		s.Hibernate()
	} else {
		useOR, _, _ := m.resolveCodexRouting(wantModel, providerPref, m.codexHomeForThread(threadID))
		if isFirst {
			s.Hibernate()
			if threadID != "" {
				_ = codex.DeleteThreadRPC(ctx, m.codexControlConfigForThread(useOR, threadID), threadID, m.CodexHomes()...)
			}
			if id != "" && id != threadID {
				_ = codex.DeleteThreadRPC(ctx, m.codexControlConfigForThread(useOR, id), id, m.CodexHomes()...)
			}
		} else if threadID != "" {
			beforeTurnID := m.codexResolveBeforeTurnID(ctx, threadID, turnIdx)
			if beforeTurnID != "" {
				if ca, ok := proc.(*codex.Agent); ok && ca.Alive() {
					var reverted codex.Thread
					_ = ca.ACP().Call(ctx, "_agentc/revert", map[string]any{"beforeTurnId": beforeTurnID}, &reverted)
				} else if codex.HasThread(threadID, m.CodexHomes()...) {
					_, _ = codex.RevertThreadRPC(ctx, m.codexControlConfigForThread(useOR, threadID), threadID, beforeTurnID)
				}
			}
		}
	}

	s.mu.Lock()
	if cutItemIdx >= 0 && cutItemIdx <= len(s.tr.Items) {
		keep := make([]*Item, 0, cutItemIdx)
		for _, it := range s.tr.Items[:cutItemIdx] {
			keep = append(keep, it)
		}
		s.queueOpsLocked(s.tr.ResetWith(keep))
	}
	var keptCP []checkpoints.TurnCheckpoint
	for _, cp := range s.meta.Checkpoints {
		if cp.TurnIndex < targetTurn {
			keptCP = append(keptCP, cp)
		}
	}
	s.meta.Checkpoints = keptCP
	if isFirst {
		s.meta.Turns = 0
		s.meta.CodexThreadID = codexFreshThread
		s.meta.SeedHandoff = ""
	} else {
		if targetTurn-1 >= 0 && targetTurn-1 < s.meta.Turns {
			s.meta.Turns = targetTurn - 1
		}
		if !isCodex {
			s.meta.CodexThreadID = codexFreshThread
			if len(s.tr.Items) > 0 {
				s.meta.SeedHandoff = BuildPortableHandoff(s.meta.Title, s.tr.Items, keptCP)
			} else {
				s.meta.SeedHandoff = ""
			}
		}
	}
	s.meta.NeedsReload = false
	s.meta.Attention = ""
	note := "Rewound conversation"
	if !conversationOnly {
		note += rewindFilesNote(filesRestored)
	}
	s.queueOpsLocked(s.tr.AddNotice("info", note+"."))
	s.mu.Unlock()

	if isFirst {
		m.invalidateHistory()
	}
	s.emit()
	m.saveSession(s)
	return prefill, nil
}

func (m *Manager) renameCodex(ctx context.Context, id, title string) error {
	targetID := m.resolveCodexThreadID(id)
	anns := m.loadCodexAnnotations()
	cur := anns[id]
	cur.Title = title
	anns[id] = cur
	if targetID != "" && targetID != id {
		curT := anns[targetID]
		curT.Title = title
		anns[targetID] = curT
	}
	_ = m.saveCodexAnnotations(anns)

	if s := m.Get(id); s != nil {
		s.mu.Lock()
		proc := s.proc
		s.mu.Unlock()
		if ca, ok := proc.(*codex.Agent); ok && ca.Alive() {
			_ = ca.ACP().Call(ctx, "_agentc/setTitle", map[string]any{"title": title}, nil)
			return nil
		}
	}
	if targetID != "" && codex.HasThread(targetID, m.CodexHomes()...) {
		useOR := m.codexThreadUseOpenRouter(targetID)
		_ = codex.RenameThreadRPC(ctx, m.codexControlConfigForThread(useOR, targetID), targetID, title)
	}
	return nil
}

func (m *Manager) setFlagCodex(ctx context.Context, id, flag string, on bool) error {
	targetID := m.resolveCodexThreadID(id)
	anns := m.loadCodexAnnotations()
	cur := anns[id]
	val := on
	if flag == "archived" {
		cur.Archived = &val
		if targetID != "" && codex.HasThread(targetID, m.CodexHomes()...) {
			useOR := m.codexThreadUseOpenRouter(targetID)
			_ = codex.ArchiveThreadRPC(ctx, m.codexControlConfigForThread(useOR, targetID), targetID, on)
		}
	} else if flag == "pinned" {
		cur.Pinned = &val
	}
	anns[id] = cur
	if targetID != "" && targetID != id {
		curT := anns[targetID]
		if flag == "archived" {
			curT.Archived = &val
		} else if flag == "pinned" {
			curT.Pinned = &val
		}
		anns[targetID] = curT
	}
	return m.saveCodexAnnotations(anns)
}

func (m *Manager) deleteCodex(ctx context.Context, id string) error {
	targetID := m.resolveCodexThreadID(id)
	var cwd string
	if s := m.Get(id); s != nil {
		s.Hibernate()
		s.stopTimers()
		s.mu.Lock()
		cwd = s.meta.Cwd
		s.removed = true
		s.mu.Unlock()
		m.mu.Lock()
		delete(m.sessions, id)
		m.mu.Unlock()
		_ = os.Remove(m.sessionFile(id))
		_ = os.RemoveAll(m.attachmentDir(id))
	}
	if cwd != "" {
		checkpoints.Cleanup(ctx, cwd, id)
	}
	anns := m.loadCodexAnnotations()
	changedAnn := false
	for _, k := range []string{id, targetID} {
		if k != "" {
			if _, ok := anns[k]; ok {
				delete(anns, k)
				changedAnn = true
			}
		}
	}
	if changedAnn {
		_ = m.saveCodexAnnotations(anns)
	}
	delCfg := codex.ProcConfig{CodexHome: m.CodexHome(), UserHome: m.cfg.HomeDir}
	for _, tid := range []string{targetID, id} {
		if tid != "" {
			_ = codex.DeleteThreadRPC(ctx, delCfg, tid, m.CodexHomes()...)
		}
	}
	m.invalidateHistory()
	m.hub.Broadcast("session.removed", map[string]any{"id": id})
	return nil
}

func (m *Manager) asideCodex(ctx context.Context, s *Session, question string) (string, error) {
	s.mu.Lock()
	cwd := s.meta.Cwd
	title := s.meta.Title
	threadID := s.codexThreadIDLocked()
	model := s.meta.WantModel
	if s.meta.Models != nil && s.meta.Models.CurrentModelID != "" {
		model = s.meta.Models.CurrentModelID
	}
	items := append([]*Item(nil), s.tr.Items...)
	cps := append([]checkpoints.TurnCheckpoint(nil), s.meta.Checkpoints...)
	s.mu.Unlock()

	bin := m.codexBin()
	env := m.agyEnv()
	var extra []string
	if m.Providers != nil {
		if ov, ok := m.Providers.Get("codex"); ok {
			if strings.TrimSpace(ov.Command) != "" {
				bin = codex.ResolveBinary(ov.Command, m.cfg.HomeDir)
			}
			env = providers.MergeEnv(env, ov.Env)
			extra = append(extra, ov.Args...)
		}
	}

	ctxSummary := BuildPortableHandoff(title, items, cps)
	fullPrompt := agyAsideGuard + ctxSummary + "\n\nQuestion: " + question

	br, err := m.CodexBridge()
	if err != nil {
		return "", err
	}
	cat := m.CodexCatalog()
	customIn, _, cfgDefault := m.codexCustomModelInputs()
	codexHome := m.codexHomeForThread(threadID)
	providerPref := s.Provider()
	useOR, _, _ := m.resolveCodexRouting(model, providerPref, codexHome)
	hasOR := m.OpenRouterConfigured()
	if !useOR {
		hasOR = false
	}
	hasNative := codex.HasNativeOpenAIAuth(codexHome, m.cfg.HomeDir, m.ProviderHealthEnv("codex"))

	h := &asideHandler{}
	ag := codex.Start(codex.Options{
		Bin:            bin,
		Env:            env,
		Cwd:            cwd,
		CodexHome:      codexHome,
		UserHome:       m.cfg.HomeDir,
		ApprovalPolicy: "never",
		SandboxMode:    "read-only",
		Provider:       providerPref,
		DefaultModel:   cfgDefault,
		Model:          model,
		Effort:         "low",
		Mode:           "plan",
		ExtraArgs:      extra,
		Ephemeral:      true,
		Bridge:         br,
		Catalog:        cat,
		CustomModels:   customIn,
		HasOpenRouter:  hasOR,
		HasNativeAuth:  hasNative,
		Handler:        h,
	})
	defer ag.Close(2 * time.Second)

	var setup acp.SessionSetup
	if err := ag.ACP().Call(ctx, "session/new", map[string]any{"cwd": cwd, "mcpServers": []any{}}, &setup); err != nil {
		return "", err
	}
	var res acp.PromptResult
	err = ag.ACP().Call(ctx, "session/prompt", map[string]any{
		"sessionId": setup.SessionID,
		"prompt":    []acp.ContentBlock{{Type: "text", Text: fullPrompt}},
	}, &res)
	ans := strings.TrimSpace(h.answer())
	if err != nil && ans == "" {
		return "", err
	}
	if ans == "" {
		return "", errors.New("Codex returned no answer")
	}
	return ans, nil
}

// Compact triggers conversation context compaction on the session.
func (s *Session) Compact(ctx context.Context) error {
	if s.isCodexSafe() {
		if err := s.ensureLive(ctx, ""); err != nil {
			return err
		}
		conn, _ := s.liveConn()
		if conn == nil {
			return errors.New("Codex is not running")
		}
		var res acp.PromptResult
		return conn.Call(ctx, "_agentc/compact", map[string]any{}, &res)
	}
	_, err := s.Prompt(PromptInput{Text: "/compact"})
	return err
}
