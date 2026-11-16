package acp

import "encoding/json"

// ContentBlock is an ACP content block (text, image, resource_link, resource, audio).
type ContentBlock struct {
	Type     string          `json:"type"`
	Text     string          `json:"text,omitempty"`
	MimeType string          `json:"mimeType,omitempty"`
	Data     string          `json:"data,omitempty"`
	URI      string          `json:"uri,omitempty"`
	Name     string          `json:"name,omitempty"`
	Title    string          `json:"title,omitempty"`
	Size     int64           `json:"size,omitempty"`
	Resource json.RawMessage `json:"resource,omitempty"`
}

// Model is one entry of availableModels.
type Model struct {
	ModelID     string `json:"modelId"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// Models is the models block of session/new|load|resume results.
type Models struct {
	AvailableModels []Model `json:"availableModels"`
	CurrentModelID  string  `json:"currentModelId"`
}

// Mode is one session mode.
type Mode struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// Modes is the modes block of session results.
type Modes struct {
	AvailableModes []Mode `json:"availableModes"`
	CurrentModeID  string `json:"currentModeId"`
}

// ConfigOptionValue is one selectable value.
type ConfigOptionValue struct {
	Value       string `json:"value"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// ConfigOption is a session config option (model, thought_level, mode).
type ConfigOption struct {
	ID           string              `json:"id"`
	Name         string              `json:"name"`
	Category     string              `json:"category,omitempty"`
	Type         string              `json:"type"`
	CurrentValue string              `json:"currentValue"`
	Options      []ConfigOptionValue `json:"options"`
}

// SessionSetup is the result of session/new, session/load and session/resume.
type SessionSetup struct {
	SessionID     string         `json:"sessionId"`
	Models        *Models        `json:"models,omitempty"`
	Modes         *Modes         `json:"modes,omitempty"`
	ConfigOptions []ConfigOption `json:"configOptions,omitempty"`
}

// Command is an available slash command.
type Command struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Input       json.RawMessage `json:"input,omitempty"`
}

// Usage is token usage reported on a prompt result.
type Usage struct {
	InputTokens       int64 `json:"inputTokens"`
	OutputTokens      int64 `json:"outputTokens"`
	CachedReadTokens  int64 `json:"cachedReadTokens"`
	CachedWriteTokens int64 `json:"cachedWriteTokens"`
	ThoughtTokens     int64 `json:"thoughtTokens"`
	TotalTokens       int64 `json:"totalTokens"`
	// CostUSD is set by agents that report spend (Claude Code).
	CostUSD float64 `json:"costUsd,omitempty"`
}

// PromptResult is the result of session/prompt.
type PromptResult struct {
	StopReason string `json:"stopReason"`
	Usage      *Usage `json:"usage,omitempty"`
}

// SessionInfo is one row of session/list.
type SessionInfo struct {
	SessionID string `json:"sessionId"`
	Cwd       string `json:"cwd"`
	Title     string `json:"title"`
	UpdatedAt string `json:"updatedAt"`
}

// PermissionOption is one choice in session/request_permission.
type PermissionOption struct {
	OptionID string `json:"optionId"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
}

// PermissionRequest is the params of session/request_permission.
type PermissionRequest struct {
	SessionID string             `json:"sessionId"`
	ToolCall  json.RawMessage    `json:"toolCall"`
	Options   []PermissionOption `json:"options"`
}

// SessionNotification is the params of session/update.
type SessionNotification struct {
	SessionID string          `json:"sessionId"`
	Update    json.RawMessage `json:"update"`
}

// UpdateHeader peeks the discriminator of a session update.
type UpdateHeader struct {
	SessionUpdate string `json:"sessionUpdate"`
}
