package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/zeromicro/go-zero/rest/pathvar"
)

func TestProjectTipHandlersCRUD(t *testing.T) {
	root := t.TempDir()
	svc, err := NewService(Options{Cwd: root, DatabasePath: filepath.Join(root, "gateway.db"), AuthMode: "none", AdminPassword: "password", NoTools: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Close() })
	accounts, _ := svc.Audit.ListAccounts(context.Background())
	account := accounts[0]
	project, err := svc.Audit.CreateProject(context.Background(), account.ID, "Tips API", "")
	if err != nil {
		t.Fatal(err)
	}

	createReq := tipRequest(http.MethodPost, project.ID, "", account, `{"type":"todo","content":"Add API tests","priority":2}`)
	createRes := httptest.NewRecorder()
	svc.handleCreateProjectTip(createRes, createReq)
	if createRes.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", createRes.Code, createRes.Body.String())
	}
	var created struct {
		Tip ProjectTip `json:"tip"`
	}
	if err := json.Unmarshal(createRes.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}

	listReq := tipRequest(http.MethodGet, project.ID, "", account, "")
	listRes := httptest.NewRecorder()
	svc.handleListProjectTips(listRes, listReq)
	if listRes.Code != http.StatusOK || !bytes.Contains(listRes.Body.Bytes(), []byte("Add API tests")) {
		t.Fatalf("list status=%d body=%s", listRes.Code, listRes.Body.String())
	}

	updateBody := `{"version":1,"status":"done"}`
	updateReq := tipRequest(http.MethodPatch, project.ID, created.Tip.ID, account, updateBody)
	updateRes := httptest.NewRecorder()
	svc.handleUpdateProjectTip(updateRes, updateReq)
	if updateRes.Code != http.StatusOK || !bytes.Contains(updateRes.Body.Bytes(), []byte(`"status":"done"`)) {
		t.Fatalf("update status=%d body=%s", updateRes.Code, updateRes.Body.String())
	}

	conflictReq := tipRequest(http.MethodPatch, project.ID, created.Tip.ID, account, updateBody)
	conflictRes := httptest.NewRecorder()
	svc.handleUpdateProjectTip(conflictRes, conflictReq)
	if conflictRes.Code != http.StatusConflict {
		t.Fatalf("conflict status=%d body=%s", conflictRes.Code, conflictRes.Body.String())
	}

	deleteReq := tipRequest(http.MethodDelete, project.ID, created.Tip.ID, account, "")
	deleteRes := httptest.NewRecorder()
	svc.handleDeleteProjectTip(deleteRes, deleteReq)
	if deleteRes.Code != http.StatusOK {
		t.Fatalf("delete status=%d body=%s", deleteRes.Code, deleteRes.Body.String())
	}
}

func tipRequest(method, projectID, tipID string, account Account, body string) *http.Request {
	request := httptest.NewRequest(method, "/v1/projects/x/tips", bytes.NewBufferString(body))
	request = request.WithContext(context.WithValue(request.Context(), accountContextKey{}, account))
	vars := map[string]string{"projectId": projectID}
	if tipID != "" {
		vars["tipId"] = tipID
	}
	return pathvar.WithVars(request, vars)
}
