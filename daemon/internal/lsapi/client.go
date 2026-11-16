// Package lsapi calls the agy language server's internal Connect (JSON over
// HTTP/1.1) API. These RPCs are undocumented; callers must treat failures as
// "not supported by this agy version".
package lsapi

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Client talks to one language server instance.
type Client struct {
	Port int
	HTTP *http.Client
}

// New returns a client for the language server on localhost:port.
func New(port int) *Client {
	return &Client{Port: port, HTTP: &http.Client{Timeout: 120 * time.Second}}
}

// Error is a Connect error response.
type Error struct {
	Status  int
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return fmt.Sprintf("language server error %d %s", e.Status, e.Code)
}

// Call invokes LanguageServerService/<method> with req, decoding into resp (may be nil).
func (c *Client) Call(ctx context.Context, method string, req any, resp any) error {
	raw, err := c.CallRaw(ctx, method, req)
	if err != nil {
		return err
	}
	if resp != nil && len(raw) > 0 {
		return json.Unmarshal(raw, resp)
	}
	return nil
}

// CallRaw invokes a method and returns the raw JSON response body.
func (c *Client) CallRaw(ctx context.Context, method string, req any) (json.RawMessage, error) {
	if req == nil {
		req = map[string]any{}
	}
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	url := fmt.Sprintf("http://localhost:%d/exa.language_server_pb.LanguageServerService/%s", c.Port, method)
	hr, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	hr.Header.Set("Content-Type", "application/json")
	hr.Header.Set("Connect-Protocol-Version", "1")
	res, err := c.HTTP.Do(hr)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	b, err := io.ReadAll(io.LimitReader(res.Body, 256<<20))
	if err != nil {
		return nil, err
	}
	if res.StatusCode != http.StatusOK {
		e := &Error{Status: res.StatusCode}
		_ = json.Unmarshal(b, e)
		if e.Message == "" {
			e.Message = fmt.Sprintf("%s: HTTP %d %s", method, res.StatusCode, bytes.TrimSpace(b))
		}
		return nil, e
	}
	return b, nil
}

// Step is a trimmed trajectory step.
type Step struct {
	Type      string          `json:"type"`
	Status    string          `json:"status"`
	UserInput json.RawMessage `json:"userInput,omitempty"`
}

// Steps returns the trajectory steps of a conversation.
func (c *Client) Steps(ctx context.Context, cascadeID string) ([]Step, error) {
	var r struct {
		Steps []Step `json:"steps"`
	}
	if err := c.Call(ctx, "GetCascadeTrajectorySteps", map[string]any{"cascadeId": cascadeID}, &r); err != nil {
		return nil, err
	}
	return r.Steps, nil
}

// UserInputStepIndices returns the indices of USER_INPUT steps, in order.
func UserInputStepIndices(steps []Step) []int {
	var out []int
	for i, s := range steps {
		if s.Type == "CORTEX_STEP_TYPE_USER_INPUT" {
			out = append(out, i)
		}
	}
	return out
}

// Stream invokes a server-streaming method (Connect protocol, JSON codec) and
// calls onMsg for every message until the stream ends, ctx is done or onMsg
// returns an error.
func (c *Client) Stream(ctx context.Context, method string, req any, onMsg func(json.RawMessage) error) error {
	if req == nil {
		req = map[string]any{}
	}
	body, err := json.Marshal(req)
	if err != nil {
		return err
	}
	env := make([]byte, 5+len(body))
	binary.BigEndian.PutUint32(env[1:5], uint32(len(body)))
	copy(env[5:], body)
	url := fmt.Sprintf("http://localhost:%d/exa.language_server_pb.LanguageServerService/%s", c.Port, method)
	hr, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(env))
	if err != nil {
		return err
	}
	hr.Header.Set("Content-Type", "application/connect+json")
	hr.Header.Set("Connect-Protocol-Version", "1")
	// Streams are long-lived: no client timeout, ctx bounds them.
	res, err := (&http.Client{Transport: c.HTTP.Transport}).Do(hr)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(res.Body, 64<<10))
		e := &Error{Status: res.StatusCode}
		_ = json.Unmarshal(b, e)
		if e.Message == "" {
			e.Message = fmt.Sprintf("%s: HTTP %d %s", method, res.StatusCode, bytes.TrimSpace(b))
		}
		return e
	}
	hdr := make([]byte, 5)
	for {
		if _, err := io.ReadFull(res.Body, hdr); err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				return nil
			}
			return err
		}
		n := binary.BigEndian.Uint32(hdr[1:])
		if n > 64<<20 {
			return fmt.Errorf("%s: stream message too large (%d bytes)", method, n)
		}
		msg := make([]byte, n)
		if _, err := io.ReadFull(res.Body, msg); err != nil {
			return err
		}
		if hdr[0]&0x02 != 0 { // end of stream: {"error":{...}}
			var end struct {
				Error *Error `json:"error"`
			}
			if json.Unmarshal(msg, &end) == nil && end.Error != nil && (end.Error.Code != "" || end.Error.Message != "") {
				return end.Error
			}
			return nil
		}
		if err := onMsg(msg); err != nil {
			return err
		}
	}
}
