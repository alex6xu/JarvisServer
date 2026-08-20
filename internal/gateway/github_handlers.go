package gateway

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
)

func (s *Service) handleGitHubStatus(w http.ResponseWriter, r *http.Request) {
	accountID, ok := s.requestAccountID(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "account context is required")
		return
	}
	if s.GitHub == nil {
		writeJSON(w, http.StatusOK, map[string]any{"configured": false, "connected": false, "github_login": ""})
		return
	}
	credential, connected, err := s.GitHub.connected(r.Context(), accountID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "cannot load github connection")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"configured": s.GitHub.oauthConfigured(), "connected": connected,
		"github_login": credential.Login, "auth_method": credential.AuthMethod,
	})
}

func (s *Service) handleGitHubConnectToken(w http.ResponseWriter, r *http.Request) {
	accountID, ok := s.requestAccountID(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "account context is required")
		return
	}
	var body struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json body")
		return
	}
	user, err := s.GitHub.connectToken(r.Context(), accountID, body.Token)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"connected": true, "github_login": user.Login, "auth_method": "pat"})
}

func (s *Service) handleGitHubAuthorize(w http.ResponseWriter, r *http.Request) {
	accountID, ok := s.requestAccountID(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "account context is required")
		return
	}
	authorizeURL, err := s.GitHub.authorizeURL(r.Context(), accountID, r.URL.Query().Get("return_to"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"authorize_url": authorizeURL})
}

func (s *Service) handleGitHubCallback(w http.ResponseWriter, r *http.Request) {
	returnPath := "/code"
	if oauthError := strings.TrimSpace(r.URL.Query().Get("error")); oauthError != "" {
		githubOAuthRedirect(w, r, returnPath, "error", "", oauthError)
		return
	}
	code, state := strings.TrimSpace(r.URL.Query().Get("code")), strings.TrimSpace(r.URL.Query().Get("state"))
	if code == "" || state == "" || s.GitHub == nil {
		githubOAuthRedirect(w, r, returnPath, "error", "", "missing oauth code or state")
		return
	}
	_, savedReturnPath, user, err := s.GitHub.exchangeOAuth(r.Context(), code, state)
	if savedReturnPath != "" {
		returnPath = savedReturnPath
	}
	if err != nil {
		githubOAuthRedirect(w, r, returnPath, "error", "", err.Error())
		return
	}
	githubOAuthRedirect(w, r, returnPath, "connected", user.Login, "")
}

func githubOAuthRedirect(w http.ResponseWriter, r *http.Request, returnPath, status, login, message string) {
	if !validGitHubReturnPath(returnPath) {
		returnPath = "/code"
	}
	values := url.Values{"github": {status}}
	if login != "" {
		values.Set("login", login)
	}
	if message != "" {
		values.Set("message", message)
	}
	http.Redirect(w, r, returnPath+"?"+values.Encode(), http.StatusFound)
}

func (s *Service) handleGitHubDisconnect(w http.ResponseWriter, r *http.Request) {
	accountID, ok := s.requestAccountID(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "account context is required")
		return
	}
	if err := s.Audit.DeleteGitHubCredential(r.Context(), accountID); err != nil {
		writeErr(w, http.StatusInternalServerError, "cannot disconnect github")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Service) handleGitHubRepos(w http.ResponseWriter, r *http.Request) {
	accountID, ok := s.requestAccountID(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "account context is required")
		return
	}
	repos, err := s.GitHub.repositories(r.Context(), accountID, parsePositiveInt(r.URL.Query().Get("per_page"), 50))
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"repos": repos})
}

func (s *Service) handleGitHubImport(w http.ResponseWriter, r *http.Request) {
	accountID, ok := s.requestAccountID(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "account context is required")
		return
	}
	var body struct {
		Owner  string `json:"owner"`
		Repo   string `json:"repo"`
		Branch string `json:"branch"`
		Name   string `json:"name"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json body")
		return
	}
	workspace, err := s.importGitHubRepository(r.Context(), accountID, body.Owner, body.Repo, body.Branch, body.Name)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"workspace": workspace})
}

func (s *Service) handleGitHubPull(w http.ResponseWriter, r *http.Request) {
	accountID, ok := s.requestAccountID(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "account context is required")
		return
	}
	result, err := s.pullGitHubWorkspace(r.Context(), accountID, pathParam(r, "workspaceId"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"result": result})
}

func (s *Service) handleGitHubPush(w http.ResponseWriter, r *http.Request) {
	accountID, ok := s.requestAccountID(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "account context is required")
		return
	}
	var body struct {
		Message string `json:"message"`
		Branch  string `json:"branch"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json body")
		return
	}
	result, err := s.pushGitHubWorkspace(r.Context(), accountID, pathParam(r, "workspaceId"), body.Branch, body.Message)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"result": result})
}
