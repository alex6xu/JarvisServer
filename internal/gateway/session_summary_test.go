package gateway

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alex6xu/jarvisserver/internal/agentcore"
	"github.com/alex6xu/jarvisserver/internal/session"
)

func summaryFixture(tb testing.TB) (*GatewayStore, session.SessionHeader) {
	tb.Helper()
	s, err := OpenGatewayStore(filepath.Join(tb.TempDir(), "fixture.db"))
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() { s.Close() })
	h := session.SessionHeader{ID: "summary", AccountID: 1, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	entries := []session.Entry{{ID: "first", Message: agentcore.UserMessage{RoleField: agentcore.RoleUser, Content: agentcore.ContentList{agentcore.NewTextContent("  fixture title  ")}}}}
	for i := 0; i < 3300; i++ {
		entries = append(entries, session.Entry{ID: fmt.Sprint(i), Message: agentcore.AssistantMessage{RoleField: agentcore.RoleAssistant, Content: agentcore.ContentList{agentcore.TextContent{Text: strings.Repeat("x", 5600), Type: "text"}}}})
	}
	if err := s.SaveEntries(h, entries); err != nil {
		tb.Fatal(err)
	}
	return s, h
}

func BenchmarkSessionListSummary(b *testing.B) {
	s, h := summaryFixture(b)
	for _, tc := range []struct {
		name  string
		store SessionRepository
	}{
		{"legacy", struct{ SessionRepository }{s}},
		{"projected", s},
	} {
		b.Run(tc.name, func(b *testing.B) {
			svc := &Service{Store: tc.store, Runs: NewRunManager()}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				m := svc.sessionMeta(h)
				if m.MessageCount != 3301 || m.Title != "fixture title" {
					b.Fatal(m.MessageCount, m.Title)
				}
			}
		})
	}
}

func TestSessionSummaryMatchesRestoreAndUpdates(t *testing.T) {
	s := newTestGatewayStore(t)
	h := session.SessionHeader{ID: "one", AccountID: 1, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	user := func(text string) agentcore.Message {
		return agentcore.UserMessage{RoleField: agentcore.RoleUser, Content: agentcore.ContentList{agentcore.NewTextContent(text)}}
	}
	cases := []agentcore.MessageList{
		{},
		{user(" \n "), agentcore.AssistantMessage{RoleField: agentcore.RoleAssistant}, user(strings.Repeat("中文", 40)), agentcore.ToolResultMessage{RoleField: agentcore.RoleToolResult}, agentcore.CompactionMessage{RoleField: agentcore.RoleCompaction, Summary: "ignored"}},
		{user("edited title")},
	}
	for _, messages := range cases {
		if err := s.Save(h, messages); err != nil {
			t.Fatal(err)
		}
		_, entries, err := s.LoadEntries(h.ID)
		if err != nil {
			t.Fatal(err)
		}
		restored := entriesToRestored(entries, "")
		n, title, err := s.SessionSummary(h.ID)
		if err != nil {
			t.Fatal(err)
		}
		if n != len(restored) || title != sessionTitle(restored) {
			t.Fatalf("got %d %q want %d %q", n, title, len(restored), sessionTitle(restored))
		}
	}
	svc := &Service{Store: s, Runs: NewRunManager()}
	other := h
	other.ID = "other"
	other.AccountID = 2
	if err := s.Save(other, agentcore.MessageList{user("private other account")}); err != nil {
		t.Fatal(err)
	}
	result, err := svc.listSessionsForAccount(1)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Sessions) != 1 || result.Sessions[0].ID != "one" || result.Sessions[0].Title != "edited title" {
		t.Fatalf("ownership regression: %+v", result)
	}
	entry, err := s.AppendSessionEntry(h, "", user("append"))
	if err != nil {
		t.Fatal(err)
	}
	n, _, err := s.SessionSummary(h.ID)
	if err != nil || n != 2 {
		t.Fatalf("append count=%d err=%v", n, err)
	}
	entry.Message = user("updated")
	if err := s.UpdateSessionEntry(h, entry); err != nil {
		t.Fatal(err)
	}
	n, _, err = s.SessionSummary(h.ID)
	if err != nil || n != 2 {
		t.Fatalf("update count=%d err=%v", n, err)
	}
}
