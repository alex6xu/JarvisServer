package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestUISettingsPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway.db")
	store, err := OpenGatewayStore(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	settings, err := store.LoadUISettings(ctx)
	if err != nil || settings.HomeLayout != "classic" {
		t.Fatalf("default = %+v, %v", settings, err)
	}
	if err := store.SaveUISettings(ctx, UISettings{HomeLayout: "workbench"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = OpenGatewayStore(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	settings, err = store.LoadUISettings(ctx)
	if err != nil || settings.HomeLayout != "workbench" {
		t.Fatalf("reopened = %+v, %v", settings, err)
	}
	if err := store.SaveUISettings(ctx, UISettings{HomeLayout: "unknown"}); err == nil {
		t.Fatal("invalid layout accepted")
	}
	settings, _ = store.LoadUISettings(ctx)
	if settings.HomeLayout != "workbench" {
		t.Fatal("invalid write changed settings")
	}
	if err := store.SaveUISettings(ctx, UISettings{HomeLayout: "classic"}); err != nil {
		t.Fatal(err)
	}
	settings, _ = store.LoadUISettings(ctx)
	if settings.HomeLayout != "classic" {
		t.Fatal("cannot restore classic layout")
	}
}

func TestUISettingsAuthorization(t *testing.T) {
	store := newTestGatewayStore(t)
	svc := &Service{Audit: store, Opts: Options{AuthMode: "token"}}
	tokens := map[string]string{}
	for _, role := range []string{"admin", "user"} {
		account, err := store.CreateAccount(context.Background(), "ui-"+role, "", role, "test-password")
		if err != nil {
			t.Fatal(err)
		}
		_, token, err := store.IssueToken(context.Background(), account.ID, "ui-test", "sk-", 0)
		if err != nil {
			t.Fatal(err)
		}
		tokens[role] = token
	}
	for _, tc := range []struct {
		name, role, method, path, body string
		status                         int
	}{
		{"anonymous read", "", "GET", "/v1/ui/settings", "", 401},
		{"anonymous write", "", "PUT", "/v1/admin/ui/settings", `{"home_layout":"workbench"}`, 401},
		{"user read", "user", "GET", "/v1/ui/settings", "", 200},
		{"user write", "user", "PUT", "/v1/admin/ui/settings", `{"home_layout":"workbench"}`, 403},
		{"invalid json", "admin", "PUT", "/v1/admin/ui/settings", `{`, 400},
		{"missing layout", "admin", "PUT", "/v1/admin/ui/settings", `{}`, 400},
		{"invalid layout", "admin", "PUT", "/v1/admin/ui/settings", `{"home_layout":"other"}`, 400},
		{"admin write", "admin", "PUT", "/v1/admin/ui/settings", `{"home_layout":"workbench"}`, 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			if token := tokens[tc.role]; token != "" {
				req.Header.Set("Authorization", "Bearer "+token)
			}
			res := httptest.NewRecorder()
			handler := svc.handleGetUISettings
			if tc.method == http.MethodPut {
				handler = svc.handleUpdateUISettings
			}
			bearerAuthMiddleware(svc)(handler)(res, req)
			if res.Code != tc.status {
				t.Fatalf("status = %d, body = %s", res.Code, res.Body.String())
			}
		})
	}
	req := httptest.NewRequest(http.MethodGet, "/v1/ui/settings", nil)
	req.Header.Set("Authorization", "Bearer "+tokens["user"])
	res := httptest.NewRecorder()
	bearerAuthMiddleware(svc)(svc.handleGetUISettings)(res, req)
	var settings UISettings
	if err := json.Unmarshal(res.Body.Bytes(), &settings); err != nil {
		t.Fatal(err)
	}
	if settings.HomeLayout != "workbench" {
		t.Fatal("admin setting not visible to user")
	}
}
