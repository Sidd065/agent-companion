// Package acp implements a minimal JSON-RPC 2.0 client for the Agent Client
// Protocol over newline-delimited stdio, as spoken by `agy acp`.
package acp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"sync"
	"sync/atomic"
)

// Message is a raw JSON-RPC 2.0 envelope.
type Message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

// RPCError is a JSON-RPC error object.
type RPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *RPCError) Error() string {
	if len(e.Data) > 0 && string(e.Data) != "null" {
		return fmt.Sprintf("%s (code %d): %s", e.Message, e.Code, string(e.Data))
	}
	return fmt.Sprintf("%s (code %d)", e.Message, e.Code)
}

// Handler receives agent-initiated traffic. Both methods are called from the
// reader goroutine in arrival order and must not block on Conn.Call.
type Handler interface {
	HandleNotification(method string, params json.RawMessage)
	// HandleRequest must eventually call c.Reply / c.ReplyError for id.
	HandleRequest(c *Conn, id json.RawMessage, method string, params json.RawMessage)
}

// ErrClosed is returned for calls on a closed connection.
var ErrClosed = errors.New("acp: connection closed")

// Conn is a bidirectional JSON-RPC connection.
type Conn struct {
	w       io.Writer
	wmu     sync.Mutex
	nextID  atomic.Int64
	mu      sync.Mutex
	pending map[string]chan *Message
	done    chan struct{}
	err     error
	handler Handler
	// Trace, when set, receives every raw line in either direction.
	Trace func(dir string, line []byte)
}

// NewConn starts reading from r. Close r (e.g. by killing the process) to stop.
func NewConn(r io.Reader, w io.Writer, h Handler) *Conn {
	c := &Conn{w: w, pending: map[string]chan *Message{}, done: make(chan struct{}), handler: h}
	go c.readLoop(r)
	return c
}

// Done is closed when the read side ends.
func (c *Conn) Done() <-chan struct{} { return c.done }

// Err returns the reason the connection ended (after Done is closed).
func (c *Conn) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

func (c *Conn) readLoop(r io.Reader) {
	br := bufio.NewReaderSize(r, 1<<20)
	var loopErr error
	for {
		line, err := br.ReadBytes('\n')
		if len(line) > 0 {
			c.dispatch(line)
		}
		if err != nil {
			loopErr = err
			break
		}
	}
	c.mu.Lock()
	if loopErr == io.EOF {
		loopErr = ErrClosed
	}
	c.err = loopErr
	pend := c.pending
	c.pending = map[string]chan *Message{}
	c.mu.Unlock()
	for _, ch := range pend {
		close(ch)
	}
	close(c.done)
}

func (c *Conn) dispatch(line []byte) {
	// Trim trailing whitespace/newline.
	for len(line) > 0 && (line[len(line)-1] == '\n' || line[len(line)-1] == '\r' || line[len(line)-1] == ' ') {
		line = line[:len(line)-1]
	}
	if len(line) == 0 {
		return
	}
	if c.Trace != nil {
		c.Trace("<<", line)
	}
	var m Message
	if err := json.Unmarshal(line, &m); err != nil {
		return // agy only writes JSON on stdout; ignore anything else
	}
	switch {
	case m.Method != "" && len(m.ID) > 0 && string(m.ID) != "null":
		if c.handler != nil {
			c.handler.HandleRequest(c, m.ID, m.Method, m.Params)
		} else {
			_ = c.ReplyError(m.ID, -32601, "method not found")
		}
	case m.Method != "":
		if c.handler != nil {
			c.handler.HandleNotification(m.Method, m.Params)
		}
	case len(m.ID) > 0:
		key := idKey(m.ID)
		c.mu.Lock()
		ch := c.pending[key]
		delete(c.pending, key)
		c.mu.Unlock()
		if ch != nil {
			ch <- &m
			close(ch)
		}
	}
}

func idKey(id json.RawMessage) string {
	var n int64
	if err := json.Unmarshal(id, &n); err == nil {
		return strconv.FormatInt(n, 10)
	}
	var s string
	if err := json.Unmarshal(id, &s); err == nil {
		return "s:" + s
	}
	return string(id)
}

func (c *Conn) write(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	c.wmu.Lock()
	defer c.wmu.Unlock()
	select {
	case <-c.done:
		return ErrClosed
	default:
	}
	if c.Trace != nil {
		c.Trace(">>", b[:len(b)-1])
	}
	_, err = c.w.Write(b)
	return err
}

// Call sends a request and waits for its response, decoding the result into out (may be nil).
func (c *Conn) Call(ctx context.Context, method string, params any, out any) error {
	raw, err := c.CallRaw(ctx, method, params)
	if err != nil {
		return err
	}
	if out != nil && len(raw) > 0 && string(raw) != "null" {
		return json.Unmarshal(raw, out)
	}
	return nil
}

// CallRaw sends a request and returns the raw result.
func (c *Conn) CallRaw(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := c.nextID.Add(1)
	key := strconv.FormatInt(id, 10)
	ch := make(chan *Message, 1)
	c.mu.Lock()
	if c.err != nil {
		c.mu.Unlock()
		return nil, ErrClosed
	}
	c.pending[key] = ch
	c.mu.Unlock()
	if params == nil {
		params = struct{}{}
	}
	if err := c.write(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}); err != nil {
		c.mu.Lock()
		delete(c.pending, key)
		c.mu.Unlock()
		return nil, err
	}
	select {
	case m, ok := <-ch:
		if !ok || m == nil {
			return nil, ErrClosed
		}
		if m.Error != nil {
			return nil, m.Error
		}
		return m.Result, nil
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, key)
		c.mu.Unlock()
		return nil, ctx.Err()
	}
}

// Notify sends a notification.
func (c *Conn) Notify(method string, params any) error {
	if params == nil {
		params = struct{}{}
	}
	return c.write(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
}

// Reply answers an agent-initiated request.
func (c *Conn) Reply(id json.RawMessage, result any) error {
	if result == nil {
		result = json.RawMessage("null")
	}
	return c.write(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}

// ReplyError answers an agent-initiated request with an error.
func (c *Conn) ReplyError(id json.RawMessage, code int, msg string) error {
	return c.write(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": code, "message": msg}})
}
