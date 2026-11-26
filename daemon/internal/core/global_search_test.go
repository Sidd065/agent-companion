package core

import (
	"context"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestGlobalSearchUTF8CaseExpansionNoPanic(t *testing.T) {
	root := nonTmpTestDir(t, "global-search-utf8")
	mgr := NewManager(Config{
		StateDir:  root,
		ConfigDir: root,
		HomeDir:   root,
	}, NewHub())
	defer mgr.Shutdown()

	cases := []struct {
		sid    string
		text   string
		query  string
		needle string
	}{
		{
			sid:    "sess-expand-2b-to-3b",
			text:   strings.Repeat("\u023a", 100) + " PANICNEEDLE " + strings.Repeat("\u023a", 40),
			query:  "panicneedle",
			needle: "panicneedle",
		},
		{
			sid:    "sess-turkish-dotted-i",
			text:   strings.Repeat("\u0130", 100) + " TURKISHNEEDLE " + strings.Repeat("\u0130", 40),
			query:  "turkishneedle",
			needle: "turkishneedle",
		},
		{
			sid:    "sess-emoji-cjk",
			text:   strings.Repeat("\U0001F680\u65E5\u672C\u8A9E", 25) + " UTF8TARGET " + strings.Repeat("\U0001F525\u4E2D\u6587", 25),
			query:  "utf8target",
			needle: "utf8target",
		},
	}

	for i, tc := range cases {
		s := newSession(mgr, SessionMeta{
			ID:        tc.sid,
			Agent:     "codex",
			Cwd:       root,
			Title:     "UTF8 Search Session",
			UpdatedAt: int64(1700000000 + i),
		})
		s.tr.LoadItems([]*Item{
			{ID: "m1", Kind: "user", Text: tc.text, Ts: int64(1700000000 + i)},
		}, 1)
		mgr.mu.Lock()
		mgr.sessions[tc.sid] = s
		mgr.mu.Unlock()
	}

	for _, tc := range cases {
		hits := mgr.GlobalSearch(context.Background(), tc.query, "", 20)
		if len(hits) != 1 {
			t.Fatalf("query %q: expected 1 hit, got %d (%+v)", tc.query, len(hits), hits)
		}
		hit := hits[0]
		if hit.SessionID != tc.sid {
			t.Fatalf("query %q: expected session %s, got %s", tc.query, tc.sid, hit.SessionID)
		}
		if !utf8.ValidString(hit.Snippet) {
			t.Fatalf("query %q: snippet is invalid UTF-8: %q", tc.query, hit.Snippet)
		}
		if strings.ContainsRune(hit.Snippet, utf8.RuneError) {
			t.Fatalf("query %q: snippet contains U+FFFD corruption: %q", tc.query, hit.Snippet)
		}
		if !strings.Contains(strings.ToLower(hit.Snippet), tc.needle) {
			t.Fatalf("query %q: snippet %q missing matched needle %q", tc.query, hit.Snippet, tc.needle)
		}
	}
}

func TestGlobalSearchQuotedPhraseAndDottedIFolding(t *testing.T) {
	root := nonTmpTestDir(t, "global-search-phrases")
	mgr := NewManager(Config{StateDir: root, ConfigDir: root, HomeDir: root}, NewHub())
	defer mgr.Shutdown()

	s1 := newSession(mgr, SessionMeta{ID: "s-phrase-match", Agent: "agy", Cwd: root, Title: "Cloudflare static tunnel setup", UpdatedAt: 100})
	s1.tr.LoadItems([]*Item{{ID: "1", Kind: "user", Text: "Configure static tunnel on jdlf.cc for İSTANBUL gateway", Ts: 100}}, 1)

	s2 := newSession(mgr, SessionMeta{ID: "s-phrase-split", Agent: "agy", Cwd: root, Title: "Other session", UpdatedAt: 90})
	s2.tr.LoadItems([]*Item{{ID: "1", Kind: "user", Text: "Configure a dynamic tunnel and later a static IP", Ts: 90}}, 1)

	mgr.mu.Lock()
	mgr.sessions[s1.meta.ID] = s1
	mgr.sessions[s2.meta.ID] = s2
	mgr.mu.Unlock()

	// Exact quoted phrase `"static tunnel"` matches s1, NOT s2.
	hits := mgr.GlobalSearch(context.Background(), `"static tunnel"`, "", 20)
	for _, h := range hits {
		if h.SessionID == "s-phrase-split" {
			t.Fatalf("quoted phrase matched split tokens in s2: %+v", hits)
		}
	}
	if len(hits) == 0 || hits[0].SessionID != "s-phrase-match" {
		t.Fatalf("quoted phrase missed s1: %+v", hits)
	}

	// ASCII "istanbul" matches "İSTANBUL" cleanly via dotted-İ folding.
	hitsI := mgr.GlobalSearch(context.Background(), "istanbul", "", 20)
	if len(hitsI) != 1 || hitsI[0].SessionID != "s-phrase-match" || !utf8.ValidString(hitsI[0].Snippet) {
		t.Fatalf("dotted-İ query 'istanbul' failed: %+v", hitsI)
	}
}
