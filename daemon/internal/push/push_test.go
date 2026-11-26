package push

import (
	"path/filepath"
	"testing"
)

func TestNotificationCategoryFilteringAndPersistence(t *testing.T) {
	// Default filter: input, done, halted ON; minor progress OFF.
	for _, kind := range []string{"permission", "question", "plan", "term_blocked", "done", "term_done", "aside", "error", "term_failed", "schedule_error", "host_url", "test"} {
		if !AllowsEvents(nil, kind) {
			t.Fatalf("default filter rejected %q", kind)
		}
	}
	for _, kind := range []string{"progress", "schedule_started"} {
		if AllowsEvents(nil, kind) {
			t.Fatalf("default filter should suppress minor progress kind %q", kind)
		}
	}

	// Custom filter: only input + halted (no done, no progress).
	p := New(filepath.Join(t.TempDir(), "sa.json"), filepath.Join(t.TempDir(), "devices.json"))
	p.Register(Device{Token: "tok1", DeviceID: "dev1", HostKey: "h1", Device: "Pixel", Events: []string{"input", "halted"}})
	// Re-registering without Events preserves existing preferences.
	p.Register(Device{Token: "tok1", DeviceID: "dev1", HostKey: "h1", Device: "Pixel"})
	d := p.devices["tok1"]
	if d == nil || !d.Allows("question") || !d.Allows("error") || d.Allows("done") || d.Allows("progress") || !d.Allows("host_url") {
		t.Fatalf("unexpected device filter behavior: %+v", d)
	}

	// Explicit empty list mutes all optional categories while still passing host_url.
	p.Register(Device{Token: "tok1", DeviceID: "dev1", HostKey: "h1", Device: "Pixel", Events: []string{}})
	d = p.devices["tok1"]
	if d.Allows("done") || d.Allows("question") || d.Allows("error") || !d.Allows("host_url") {
		t.Fatalf("expected muted optional notifications, got %+v", d.Events)
	}
}
