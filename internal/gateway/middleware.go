package gateway

import (
	"context"
	"crypto/subtle"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/zeromicro/go-zero/rest"
)

// serverRunOptions returns go-zero rest.RunOption values applied at server construction
// (CORS and related cross-cutting HTTP setup).
func serverRunOptions() []rest.RunOption {
	return nil
}

// registerMiddlewares attaches request middlewares via rest.Server.Use.
func registerMiddlewares(server *rest.Server, svc *Service) {
	server.Use(bearerAuthMiddleware(svc))
}

// bearerAuthMiddleware enforces Authorization when AuthMode=token.
// Paths under /v1/auth/login|register and /healthz remain public.
func bearerAuthMiddleware(svc *Service) rest.Middleware {
	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if isPublicPath(r.URL.Path) {
				next(w, r)
				return
			}
			account, err := svc.authenticateRequest(r)
			if err != nil {
				writeErr(w, http.StatusUnauthorized, "unauthorized")
				return
			}
			if strings.HasPrefix(r.URL.Path, "/v1/admin/") && account.Role != "admin" {
				writeErr(w, http.StatusForbidden, "admin role required")
				return
			}
			if selected := strings.TrimSpace(r.Header.Get("X-Account-ID")); selected != "" && account.Role != "admin" {
				id, parseErr := strconv.Atoi(selected)
				if parseErr != nil || id != account.ID {
					writeErr(w, http.StatusForbidden, "cannot act as another account")
					return
				}
			}
			next(w, r.WithContext(context.WithValue(r.Context(), accountContextKey{}, account)))
		}
	}
}

type accountContextKey struct{}

func requestAccount(r *http.Request) (Account, bool) {
	a, ok := r.Context().Value(accountContextKey{}).(Account)
	return a, ok
}

func (svc *Service) requestAccountID(r *http.Request) (int, bool) {
	account, ok := requestAccount(r)
	if !ok || account.ID <= 0 {
		return 0, false
	}
	selected := strings.TrimSpace(r.Header.Get("X-Account-ID"))
	if selected == "" || account.Role != "admin" {
		return account.ID, true
	}
	id, err := strconv.Atoi(selected)
	if err != nil || id <= 0 {
		return 0, false
	}
	if _, err := svc.Audit.GetAccount(r.Context(), id); err != nil {
		return 0, false
	}
	return id, true
}

func bearerToken(r *http.Request) string {
	raw := strings.TrimSpace(r.Header.Get("X-API-Key"))
	if raw != "" {
		return raw
	}
	auth := strings.TrimSpace(r.Header.Get("Authorization"))
	if len(auth) >= 7 && strings.EqualFold(auth[:7], "Bearer ") {
		return strings.TrimSpace(auth[7:])
	}
	return ""
}

func (svc *Service) authenticateRequest(r *http.Request) (Account, error) {
	raw := bearerToken(r)
	if raw != "" {
		if configured := svc.Opts.APIToken; configured != "" && subtle.ConstantTimeCompare([]byte(raw), []byte(configured)) == 1 {
			accounts, err := svc.Audit.ListAccounts(r.Context())
			if err == nil {
				for _, account := range accounts {
					if account.Role == "admin" {
						return account, nil
					}
				}
			}
		}
		return svc.Audit.AccountForToken(r.Context(), raw)
	}
	if svc.Opts.AuthMode == "none" {
		accounts, err := svc.Audit.ListAccounts(r.Context())
		if err == nil && len(accounts) > 0 {
			return accounts[0], nil
		}
	}
	return Account{}, errors.New("missing token")
}

func isPublicPath(path string) bool {
	switch path {
	case "/healthz", "/v1/auth/login", "/v1/auth/register":
		return true
	default:
		return false
	}
}
