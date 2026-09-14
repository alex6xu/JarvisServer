package gateway

import (
	"context"
	"encoding/json"
	"github.com/alex6xu/jarvisserver/internal/agentcore"
	"github.com/alex6xu/jarvisserver/internal/session"
	"github.com/zeromicro/go-zero/rest/pathvar"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestGetSessionPresentationBudgetAndAccountIsolation(t *testing.T) {
	store := newTestGatewayStore(t)
	h := session.SessionHeader{ID: "history", AccountID: 11, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	long := strings.Repeat("展示内容 ", 20000)
	entries := session.Entry{ID: "entry-1", Message: agentcore.UserMessage{RoleField: agentcore.RoleUser, Content: agentcore.ContentList{agentcore.NewTextContent(long)}}}
	if err := store.SaveEntries(h, []session.Entry{entries}); err != nil {
		t.Fatal(err)
	}
	svc := &Service{Store: store, Runs: NewRunManager(), Audit: store}
	req := httptest.NewRequest(http.MethodGet, "/v1/agent/sessions/history", nil).WithContext(context.WithValue(context.Background(), accountContextKey{}, Account{ID: 11}))
	req = pathvar.WithVars(req, map[string]string{"sessionId": "history"})
	rr := httptest.NewRecorder()
	svc.handleGetSession(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	if rr.Body.Len() > maxHistoryResponseBytes+4096 {
		t.Fatalf("response exceeded budget: %d", rr.Body.Len())
	}
	var got SessionDetailResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Messages) != 1 || got.Messages[0].ID != "entry-1" || got.Messages[0].Seq != 1 || !got.Messages[0].ContentTruncated {
		t.Fatalf("projection=%+v", got.Messages)
	}
	if got.Messages[0].Content == long {
		t.Fatal("presentation truncation missing")
	}
	bad := httptest.NewRequest(http.MethodGet, "/v1/agent/sessions/history", nil).WithContext(context.WithValue(context.Background(), accountContextKey{}, Account{ID: 12}))
	bad = pathvar.WithVars(bad, map[string]string{"sessionId": "history"})
	denied := httptest.NewRecorder()
	svc.handleGetSession(denied, bad)
	if denied.Code != http.StatusNotFound {
		t.Fatalf("cross-account status=%d", denied.Code)
	}
}
