package gateway

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/zeromicro/go-zero/rest/pathvar"
)

func pathParam(r *http.Request, key string) string {
	if v := pathvar.Vars(r); v != nil {
		if s := v[key]; s != "" {
			return s
		}
	}
	return r.PathValue(key)
}

func (s *Service) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	var body struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json body")
		return
	}
	if body.NewPassword == "" {
		writeErr(w, http.StatusBadRequest, "new_password is required")
		return
	}
	account, ok := requestAccount(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if err := s.Audit.ChangePassword(r.Context(), account.ID, body.CurrentPassword, body.NewPassword); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	_, token, err := s.Audit.IssueToken(r.Context(), account.ID, "web-session", "sess_", 24*time.Hour)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"token": token, "account": account})
}

func (s *Service) handleListAccounts(w http.ResponseWriter, r *http.Request) {
	accounts, err := s.Audit.ListAccounts(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"accounts": accounts})
}

func (s *Service) handleCreateAccount(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
		Email    string `json:"email"`
		Role     string `json:"role"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json body")
		return
	}
	acct, err := s.Audit.CreateAccount(r.Context(), body.Username, body.Email, body.Role, body.Password)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"account": acct})
}

func (s *Service) handleDeleteAccount(w http.ResponseWriter, r *http.Request) {
	id := parseInt(pathParam(r, "id"))
	if err := s.Audit.DeleteAccount(r.Context(), id); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func parseInt(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int(c-'0')
	}
	return n
}
