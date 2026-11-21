package acpproc

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"agentc/internal/acp"
)

type testHandler struct {
	mu      sync.Mutex
	updates []string
	perms   int
}

func (h *testHandler) HandleNotification(method string, params json.RawMessage) {
	if method == "session/update" {
		h.mu.Lock()
		h.updates = append(h.updates, string(params))
		h.mu.Unlock()
	}
}

func (h *testHandler) HandleRequest(c *acp.Conn, id json.RawMessage, method string, _ json.RawMessage) {
	if method == "session/request_permission" {
		h.mu.Lock()
		h.perms++
		h.mu.Unlock()
		_ = c.Reply(id, map[string]any{"outcome": map[string]any{"outcome": "selected", "optionId": "allow"}})
		return
	}
	_ = c.ReplyError(id, -32601, "unsupported")
}

func TestGenericACPWithFakeACP(t *testing.T) {
	tmp := t.TempDir()
	bin := filepath.Join(tmp, "fakeacp")
	build := exec.Command("go", "build", "-o", bin, "../../cmd/fakeacp")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build fakeacp: %v\n%s", err, string(out))
	}

	h := &testHandler{}
	ctx := context.Background()
	p, err := Start(ctx, Options{
		Command: bin,
		Cwd:     tmp,
		LogDir:  tmp,
		Env:     append(os.Environ(), "FAKEACP_STATE_DIR="+filepath.Join(tmp, "state")),
		Handler: h,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close(2 * time.Second)

	var setup acp.SessionSetup
	if err := p.ACP().Call(ctx, "session/new", map[string]any{"cwd": tmp}, &setup); err != nil {
		t.Fatal(err)
	}
	if setup.Models == nil || len(setup.Models.AvailableModels) < 3 {
		t.Fatalf("unexpected models: %+v", setup.Models)
	}

	var res acp.PromptResult
	if err := p.ACP().Call(ctx, "session/prompt", map[string]any{
		"sessionId": setup.SessionID,
		"prompt":    []acp.ContentBlock{{Type: "text", Text: "[perm] [edit:foo.txt:hello world\\n]"}},
	}, &res); err != nil {
		t.Fatal(err)
	}
	if h.perms != 1 {
		t.Fatalf("expected 1 permission prompt, got %d", h.perms)
	}
	b, err := os.ReadFile(filepath.Join(tmp, "foo.txt"))
	if err != nil || string(b) != "hello world\n" {
		t.Fatalf("expected edited file, got %q err=%v", string(b), err)
	}
	joined := strings.Join(h.updates, "\n")
	if !strings.Contains(joined, "Permission approved") {
		t.Fatalf("missing approval output: %s", joined)
	}
}
