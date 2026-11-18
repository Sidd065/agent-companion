package core

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"agentc/internal/acp"
)

// Item is one entry of a session transcript, as rendered by the app.
type Item struct {
	ID     string          `json:"id"`
	Kind   string          `json:"kind"` // user|agent|thought|tool|plan|perm|notice|turn
	Seq    int64           `json:"seq"`
	Ts     int64           `json:"ts"`
	Text   string          `json:"text,omitempty"`
	Blocks []Block         `json:"blocks,omitempty"`
	State  string          `json:"state,omitempty"` // user: queued|sent|failed ; perm: pending|resolved|cancelled
	Tool   *Tool           `json:"tool,omitempty"`
	Plan   json.RawMessage `json:"plan,omitempty"`
	Perm   *Perm           `json:"perm,omitempty"`
	Level  string          `json:"level,omitempty"`
	Turn   *Turn           `json:"turn,omitempty"`
	Trunc  bool            `json:"trunc,omitempty"`
	// Ref is the agent's own id for a user message (Claude message uuid),
	// used to fork and rewind at that message.
	Ref string `json:"ref,omitempty"`
}

// Block is a non-text part of a user message.
type Block struct {
	Type string `json:"type"` // image|link|resource|audio
	Mime string `json:"mime,omitempty"`
	URI  string `json:"uri,omitempty"`
	Name string `json:"name,omitempty"`
	Text string `json:"text,omitempty"`
}

// Tool is a tool call.
type Tool struct {
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

// Perm is a permission request card.
type Perm struct {
	ReqID    string                 `json:"reqId"`
	CallID   string                 `json:"callId,omitempty"`
	Title    string                 `json:"title,omitempty"`
	ToolKind string                 `json:"toolKind,omitempty"`
	Options  []acp.PermissionOption `json:"options"`
	Chosen   string                 `json:"chosen,omitempty"`
	// Claude Code extras: the tool, the kind of request (tool|question|plan),
	// its input for questions and plans, why it was asked, and the answer.
	Tool        string          `json:"tool,omitempty"`
	Kind        string          `json:"kind,omitempty"`
	Input       json.RawMessage `json:"input,omitempty"`
	Reason      string          `json:"reason,omitempty"`
	BlockedPath string          `json:"blockedPath,omitempty"`
	Subagent    bool            `json:"subagent,omitempty"`
	Answer      json.RawMessage `json:"answer,omitempty"`
}

// Turn marks the end of a prompt turn.
type Turn struct {
	StopReason string     `json:"stopReason"`
	Usage      *acp.Usage `json:"usage,omitempty"`
	DurationMs int64      `json:"durationMs"`
	Error      string     `json:"error,omitempty"`
}

// Op is a transcript mutation streamed to subscribers.
type Op struct {
	Seq   int64   `json:"seq"`
	Op    string  `json:"op"` // add|set|append|reset|remove
	ID    string  `json:"id,omitempty"`
	Item  *Item   `json:"item,omitempty"`
	Text  string  `json:"text,omitempty"`
	Items []*Item `json:"items,omitempty"`
}

const opRing = 4000

// Transcript holds a session's items and recent ops. Not safe for concurrent
// use: the owning Session serializes access.
type Transcript struct {
	Items    []*Item
	index    map[string]int
	Seq      int64
	ops      []Op
	open     *Item // agent/thought item receiving chunks
	userOpen *Item // user item receiving chunks
	planID   string
	counter  int
	Dirty    bool
}

// NewTranscript returns an empty transcript.
func NewTranscript() *Transcript {
	return &Transcript{index: map[string]int{}}
}

// LoadItems restores persisted items.
func (t *Transcript) LoadItems(items []*Item, seq int64) {
	t.Items = items
	t.index = map[string]int{}
	for i, it := range items {
		t.index[it.ID] = i
		if n := idNum(it.ID); n > t.counter {
			t.counter = n
		}
	}
	t.Seq = seq
	t.ops = nil
	t.open, t.userOpen = nil, nil
}

func idNum(id string) int {
	var n int
	for i := 0; i < len(id); i++ {
		if id[i] >= '0' && id[i] <= '9' {
			fmt.Sscanf(id[i:], "%d", &n)
			return n
		}
	}
	return 0
}

func (t *Transcript) newID(prefix string) string {
	t.counter++
	return fmt.Sprintf("%s%d", prefix, t.counter)
}

func cloneItem(it *Item) *Item {
	c := *it
	if it.Blocks != nil {
		c.Blocks = append([]Block(nil), it.Blocks...)
	}
	if it.Tool != nil {
		tl := *it.Tool
		c.Tool = &tl
	}
	if it.Perm != nil {
		p := *it.Perm
		p.Options = append([]acp.PermissionOption(nil), it.Perm.Options...)
		c.Perm = &p
	}
	if it.Turn != nil {
		tu := *it.Turn
		c.Turn = &tu
	}
	return &c
}

func (t *Transcript) push(op Op) Op {
	t.Seq++
	op.Seq = t.Seq
	if op.Item != nil {
		op.Item.Seq = t.Seq
		// Ops are replayed later; snapshot the item so later mutations
		// (e.g. streamed text appends) are not double-applied.
		op.Item = cloneItem(op.Item)
	}
	t.ops = append(t.ops, op)
	if len(t.ops) > opRing {
		t.ops = append([]Op(nil), t.ops[len(t.ops)-opRing/2:]...)
	}
	t.Dirty = true
	return op
}

func (t *Transcript) add(it *Item) Op {
	t.index[it.ID] = len(t.Items)
	t.Items = append(t.Items, it)
	return t.push(Op{Op: "add", ID: it.ID, Item: it})
}

func (t *Transcript) set(it *Item) Op {
	return t.push(Op{Op: "set", ID: it.ID, Item: it})
}

// Get returns an item by id.
func (t *Transcript) Get(id string) *Item {
	if i, ok := t.index[id]; ok && i < len(t.Items) {
		return t.Items[i]
	}
	return nil
}

// OpsSince returns ops after seq, or ok=false when they are no longer buffered.
func (t *Transcript) OpsSince(seq int64) ([]Op, bool) {
	if seq == t.Seq {
		return nil, true
	}
	if seq > t.Seq || len(t.ops) == 0 || t.ops[0].Seq > seq+1 {
		return nil, false
	}
	var out []Op
	for _, op := range t.ops {
		if op.Seq > seq {
			out = append(out, op)
		}
	}
	return out, true
}

// Reset clears all items (e.g. before a history replay).
func (t *Transcript) Reset() Op {
	t.Items = nil
	t.index = map[string]int{}
	t.open, t.userOpen = nil, nil
	t.planID = ""
	return t.push(Op{Op: "reset", Items: []*Item{}})
}

// ResetWith replaces all items and emits a reset op.
func (t *Transcript) ResetWith(items []*Item) Op {
	t.Items = items
	t.index = map[string]int{}
	for i, it := range items {
		t.index[it.ID] = i
	}
	t.open, t.userOpen = nil, nil
	snap := make([]*Item, len(items))
	for i, it := range items {
		snap[i] = cloneItem(it)
	}
	return t.push(Op{Op: "reset", Items: snap})
}

func (t *Transcript) closeText() {
	t.open = nil
	t.userOpen = nil
}

func now() int64 { return time.Now().UnixMilli() }

// AddUser appends a locally-sent user message.
func (t *Transcript) AddUser(text string, blocks []Block, state string) (*Item, Op) {
	t.closeText()
	t.planID = ""
	it := &Item{ID: t.newID("u"), Kind: "user", Ts: now(), Text: text, Blocks: blocks, State: state}
	return it, t.add(it)
}

// SetUserState updates a user item's delivery state.
func (t *Transcript) SetUserState(id, state string) (Op, bool) {
	it := t.Get(id)
	if it == nil || it.State == state {
		return Op{}, false
	}
	it.State = state
	return t.set(it), true
}

// UpdateUserText edits a queued user message.
func (t *Transcript) UpdateUserText(id, text string) (Op, bool) {
	it := t.Get(id)
	if it == nil {
		return Op{}, false
	}
	it.Text = text
	return t.set(it), true
}

// Remove deletes an item.
func (t *Transcript) Remove(id string) (Op, bool) {
	i, ok := t.index[id]
	if !ok {
		return Op{}, false
	}
	t.Items = append(t.Items[:i], t.Items[i+1:]...)
	t.index = map[string]int{}
	for j, it := range t.Items {
		t.index[it.ID] = j
	}
	if t.open != nil && t.open.ID == id {
		t.open = nil
	}
	return t.push(Op{Op: "remove", ID: id}), true
}

func normText(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// UserChunkRef is UserChunk for agents that identify messages (ref) and
// replay their original time (ts, ms).
func (t *Transcript) UserChunkRef(b acp.ContentBlock, blockFor func(acp.ContentBlock) *Block, ref string, ts int64) []Op {
	if ref == "" {
		return t.UserChunk(b, blockFor)
	}
	if t.userOpen != nil && t.userOpen.Ref != ref {
		t.userOpen = nil
	}
	if t.userOpen == nil {
		for _, it := range t.Items {
			if it.Kind == "user" && it.Ref == ref {
				t.open, t.planID = nil, ""
				it.State = ""
				t.userOpen = it
				if b.Type == "text" && it.Text == "" {
					it.Text = b.Text
				} else if b.Type != "text" {
					if nb := blockFor(b); nb != nil && !hasBlock(it.Blocks, *nb) {
						it.Blocks = append(it.Blocks, *nb)
					}
				}
				return []Op{t.set(it)}
			}
		}
		t.open, t.planID = nil, ""
		it := &Item{ID: t.newID("u"), Kind: "user", Ts: now(), Ref: ref}
		if ts > 0 {
			it.Ts = ts
		}
		if b.Type == "text" {
			it.Text = b.Text
		} else if nb := blockFor(b); nb != nil {
			it.Blocks = []Block{*nb}
		}
		t.userOpen = it
		return []Op{t.add(it)}
	}
	return t.UserChunk(b, blockFor)
}

// UserChunk handles a user_message_chunk (echo of a sent prompt or history replay).
func (t *Transcript) UserChunk(b acp.ContentBlock, blockFor func(acp.ContentBlock) *Block) []Op {
	t.open = nil
	t.planID = ""
	if t.userOpen != nil {
		it := t.userOpen
		if b.Type == "text" {
			if !strings.Contains(normText(it.Text), normText(b.Text)) {
				if it.Text != "" {
					it.Text += "\n"
				}
				it.Text += b.Text
			}
		} else if nb := blockFor(b); nb != nil && !hasBlock(it.Blocks, *nb) {
			it.Blocks = append(it.Blocks, *nb)
		}
		return []Op{t.set(it)}
	}
	if b.Type == "text" {
		want := normText(b.Text)
		for _, it := range t.Items {
			if it.Kind == "user" && (it.State == "sent" || it.State == "delivered") {
				have := normText(it.Text)
				if have == want || strings.HasSuffix(want, have) || strings.HasPrefix(want, have) {
					it.State = ""
					t.userOpen = it
					return []Op{t.set(it)}
				}
			}
		}
		it := &Item{ID: t.newID("u"), Kind: "user", Ts: now(), Text: b.Text}
		t.userOpen = it
		return []Op{t.add(it)}
	}
	it := &Item{ID: t.newID("u"), Kind: "user", Ts: now()}
	if nb := blockFor(b); nb != nil {
		it.Blocks = []Block{*nb}
	}
	t.userOpen = it
	return []Op{t.add(it)}
}

func hasBlock(bs []Block, b Block) bool {
	for _, x := range bs {
		if x.Type == b.Type && x.URI != "" && x.URI == b.URI {
			return true
		}
	}
	return false
}

// TextChunk appends agent or thought text.
func (t *Transcript) TextChunk(kind, text string) []Op {
	t.userOpen = nil
	if t.open != nil && t.open.Kind == kind {
		t.open.Text += text
		return []Op{t.push(Op{Op: "append", ID: t.open.ID, Text: text})}
	}
	prefix := "a"
	if kind == "thought" {
		prefix = "t"
	}
	it := &Item{ID: t.newID(prefix), Kind: kind, Ts: now(), Text: text}
	t.open = it
	return []Op{t.add(it)}
}

type toolUpdate struct {
	ToolCallID string          `json:"toolCallId"`
	Name       string          `json:"name"`
	Kind       string          `json:"kind"`
	Title      string          `json:"title"`
	Status     string          `json:"status"`
	Locations  json.RawMessage `json:"locations"`
	RawInput   json.RawMessage `json:"rawInput"`
	Args       json.RawMessage `json:"args"`
	RawOutput  json.RawMessage `json:"rawOutput"`
	Content    json.RawMessage `json:"content"`
}

func present(r json.RawMessage) bool { return len(r) > 0 && string(r) != "null" }

// ToolCall handles tool_call and tool_call_update. Returns ops and the item.
func (t *Transcript) ToolCall(raw json.RawMessage, isNew bool) ([]Op, *Item) {
	var u toolUpdate
	if err := json.Unmarshal(raw, &u); err != nil || u.ToolCallID == "" {
		return nil, nil
	}
	id := "tc:" + u.ToolCallID
	it := t.Get(id)
	created := false
	if it == nil {
		t.closeText()
		it = &Item{ID: id, Kind: "tool", Ts: now(), Tool: &Tool{CallID: u.ToolCallID, StartTs: now()}}
		created = true
	}
	tl := it.Tool
	if u.Name != "" {
		tl.Name = u.Name
	}
	if u.Kind != "" {
		tl.Kind = u.Kind
	}
	if u.Title != "" {
		tl.Title = u.Title
	}
	if u.Status != "" {
		tl.Status = u.Status
		if (u.Status == "completed" || u.Status == "failed") && tl.EndTs == 0 {
			tl.EndTs = now()
		}
	}
	if present(u.Locations) {
		tl.Locations = u.Locations
	}
	if present(u.RawInput) {
		tl.Input = u.RawInput
	} else if present(u.Args) {
		tl.Input = u.Args
	}
	if present(u.RawOutput) {
		tl.Output = u.RawOutput
	}
	if present(u.Content) {
		tl.Content = u.Content
	}
	if created {
		return []Op{t.add(it)}, it
	}
	return []Op{t.set(it)}, it
}

// PlanUpdate handles an ACP plan update.
func (t *Transcript) PlanUpdate(entries json.RawMessage) []Op {
	t.closeText()
	if t.planID != "" {
		if it := t.Get(t.planID); it != nil {
			it.Plan = entries
			return []Op{t.set(it)}
		}
	}
	it := &Item{ID: t.newID("pl"), Kind: "plan", Ts: now(), Plan: entries}
	t.planID = it.ID
	return []Op{t.add(it)}
}

// AddPerm adds a pending permission card.
func (t *Transcript) AddPerm(p *Perm) (*Item, Op) {
	t.closeText()
	it := &Item{ID: t.newID("p"), Kind: "perm", Ts: now(), Perm: p, State: "pending"}
	return it, t.add(it)
}

// ResolvePerm marks a permission card answered.
func (t *Transcript) ResolvePerm(reqID, state, chosen string) (Op, bool) {
	for _, it := range t.Items {
		if it.Kind == "perm" && it.Perm != nil && it.Perm.ReqID == reqID {
			it.State = state
			it.Perm.Chosen = chosen
			return t.set(it), true
		}
	}
	return Op{}, false
}

// AddNotice appends an informational/system row.
func (t *Transcript) AddNotice(level, text string) Op {
	t.closeText()
	it := &Item{ID: t.newID("n"), Kind: "notice", Ts: now(), Level: level, Text: text}
	return t.add(it)
}

// AddTurn appends a turn-end marker.
func (t *Transcript) AddTurn(turn *Turn) Op {
	t.closeText()
	it := &Item{ID: t.newID("e"), Kind: "turn", Ts: now(), Turn: turn}
	return t.add(it)
}

// --- transport truncation ---

const maxStr = 12 * 1024

// ForTransport returns a copy of the item with oversized fields truncated.
func ForTransport(it *Item) *Item {
	if it == nil || it.Tool == nil {
		if it != nil && len(it.Text) > 256*1024 {
			c := *it
			c.Text = it.Text[:256*1024] + "\n…[truncated]"
			c.Trunc = true
			return &c
		}
		return it
	}
	size := len(it.Tool.Input) + len(it.Tool.Output) + len(it.Tool.Content)
	if size <= 3*maxStr {
		return it
	}
	c := *it
	tl := *it.Tool
	var tr bool
	tl.Input, tr = truncJSON(tl.Input, maxStr)
	c.Trunc = c.Trunc || tr
	tl.Output, tr = truncJSON(tl.Output, maxStr)
	c.Trunc = c.Trunc || tr
	tl.Content, tr = truncJSON(tl.Content, 4*maxStr)
	c.Trunc = c.Trunc || tr
	c.Tool = &tl
	return &c
}

func truncJSON(raw json.RawMessage, limit int) (json.RawMessage, bool) {
	if len(raw) <= limit {
		return raw, false
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return raw, false
	}
	changed := false
	v = truncVal(v, limit, &changed)
	b, err := json.Marshal(v)
	if err != nil {
		return raw, false
	}
	return b, changed
}

func truncVal(v any, limit int, changed *bool) any {
	switch x := v.(type) {
	case string:
		if len(x) > limit {
			*changed = true
			head := x[:limit*3/4]
			tail := x[len(x)-limit/4:]
			return head + fmt.Sprintf("\n…[%d bytes truncated]…\n", len(x)-len(head)-len(tail)) + tail
		}
		return x
	case []any:
		for i := range x {
			x[i] = truncVal(x[i], limit, changed)
		}
		return x
	case map[string]any:
		for k := range x {
			x[k] = truncVal(x[k], limit, changed)
		}
		return x
	}
	return v
}

// TransportItems returns truncated copies of all items.
func (t *Transcript) TransportItems() []*Item {
	out := make([]*Item, len(t.Items))
	for i, it := range t.Items {
		out[i] = ForTransport(it)
	}
	return out
}

// TransportOps returns ops with truncated items.
func TransportOps(ops []Op) []Op {
	out := make([]Op, len(ops))
	for i, op := range ops {
		if op.Item != nil {
			op.Item = ForTransport(op.Item)
		}
		if len(op.Items) > 0 {
			items := make([]*Item, len(op.Items))
			for j, it := range op.Items {
				items[j] = ForTransport(it)
			}
			op.Items = items
		}
		out[i] = op
	}
	return out
}
