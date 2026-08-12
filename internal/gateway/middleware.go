package gateway

import (
	"net/http"
	"strings"

	"github.com/zeromicro/go-zero/rest"
)

// serverRunOptions returns go-zero rest.RunOption values applied at server construction
// (CORS and related cross-cutting HTTP setup).
func serverRunOptions() []rest.RunOption {
	return []rest.RunOption{
		rest.WithCors("*"),
		rest.WithCorsHeaders("Authorization", "Content-Type", "X-Account-ID"),
	}
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
			if svc.Opts.AuthMode != "token" || isPublicPath(r.URL.Path) {
				next(w, r)
				return
			}
			if !svc.authOK(r) {
				writeErr(w, http.StatusUnauthorized, "unauthorized")
				return
			}
			next(w, r)
		}
	}
}

func isPublicPath(path string) bool {
	switch path {
	case "/healthz", "/v1/auth/login", "/v1/auth/register", "/v1/auth/logout":
		return true
	default:
		return strings.HasPrefix(path, "/v1/auth/") && path != "/v1/auth/me"
	}
}
