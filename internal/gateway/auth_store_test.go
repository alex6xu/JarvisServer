package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

func TestGatewayStoreAuthenticationLifecycle(t *testing.T) {
	store, err := OpenGatewayStore(filepath.Join(t.TempDir(), "gateway.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	admin, err := store.CreateAccount(ctx, "admin", "admin@example.test", "admin", "correct-password")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Authenticate(ctx, "admin", "wrong-password"); err == nil {
		t.Fatal("wrong password must fail")
	}
	got, err := store.Authenticate(ctx, "admin", "correct-password")
	if err != nil || got.ID != admin.ID {
		t.Fatalf("authenticate = %+v, %v", got, err)
	}
	_, raw, err := store.IssueToken(ctx, admin.ID, "session", "sess_", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if tokenAccount, err := store.AccountForToken(ctx, raw); err != nil || tokenAccount.ID != admin.ID {
		t.Fatalf("token account = %+v, %v", tokenAccount, err)
	}
	if err := store.RevokeToken(ctx, raw); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AccountForToken(ctx, raw); err == nil {
		t.Fatal("revoked token must fail")
	}
	if err := store.DeleteAccount(ctx, admin.ID); err == nil {
		t.Fatal("last admin deletion must fail")
	}
}

func TestAuthMiddlewareEnforcesAdminRole(t *testing.T) {
	dir := t.TempDir()
	svc, err := NewService(Options{Cwd: dir, DatabasePath: filepath.Join(dir, "gateway.db"), AuthMode: "token", AdminPassword: "admin-password", NoTools: true})
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()
	user, err := svc.Audit.CreateAccount(context.Background(), "user", "", "user", "user-password")
	if err != nil {
		t.Fatal(err)
	}
	_, raw, err := svc.Audit.IssueToken(context.Background(), user.ID, "session", "sess_", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	handler := bearerAuthMiddleware(svc)(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })

	req := httptest.NewRequest(http.MethodGet, "/v1/admin/providers", nil)
	req.Header.Set("Authorization", "Bearer "+raw)
	rr := httptest.NewRecorder()
	handler(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("admin route status = %d, want 403", rr.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer "+raw)
	rr = httptest.NewRecorder()
	handler(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("normal route status = %d, want 204", rr.Code)
	}
}
