package gateway

import (
	"encoding/json"
	"net/http"

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
	writeJSON(w, http.StatusOK, map[string]any{
		"token":   "dev-token",
		"account": stubAccount("dev"),
	})
}

func (s *Service) handleListAccounts(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"accounts": s.Mem.listAccounts()})
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
	acct, err := s.Mem.createAccount(body.Username, body.Email, body.Role, body.Password)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"account": acct})
}

func (s *Service) handleDeleteAccount(w http.ResponseWriter, r *http.Request) {
	id := parseInt(pathParam(r, "id"))
	if err := s.Mem.deleteAccount(id); err != nil {
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
