package gateway

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func tipExecutionService(t *testing.T) (*Service, Account, Project, ProjectTip) {
	t.Helper()
	svc, err := NewService(Options{Cwd: t.TempDir(), AuthMode: "none", AdminPassword: "password", NoSkills: true, Approve: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Close() })
	svc.Mem.providers = map[int]*Provider{}
	accounts, err := svc.Audit.ListAccounts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	account := accounts[0]
	project, err := svc.Audit.CreateProject(context.Background(), account.ID, "Tip execution", "")
	if err != nil {
		t.Fatal(err)
	}
	tip, err := svc.Audit.CreateProjectTip(context.Background(), account.ID, CreateProjectTipInput{ProjectID: project.ID, Content: "Explain the change", Type: "todo"})
	if err != nil {
		t.Fatal(err)
	}
	return svc, account, project, tip
}

func executeTipRequest(svc *Service, account Account, projectID, tipID, mode, key string) *httptest.ResponseRecorder {
	res := httptest.NewRecorder()
	svc.handleExecuteProjectTip(res, tipRequest(http.MethodPost, projectID, tipID, account, fmt.Sprintf(`{"mode":%q,"idempotency_key":%q}`, mode, key)))
	return res
}

func TestTipExecutionOwnershipAndFailedStart(t *testing.T) {
	svc, account, project, tip := tipExecutionService(t)
	other, err := svc.Audit.CreateAccount(context.Background(), "other", "other@test.local", "user", "password")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		account          Account
		projectID, tipID string
	}{{other, project.ID, tip.ID}, {account, "missing", tip.ID}, {account, project.ID, "missing"}} {
		res := executeTipRequest(svc, tc.account, tc.projectID, tc.tipID, "execute", "one")
		if res.Code != 404 {
			t.Fatalf("ownership: %d %s", res.Code, res.Body.String())
		}
	}
	if res := executeTipRequest(svc, account, project.ID, tip.ID, "invalid", "one"); res.Code != 400 {
		t.Fatal(res.Code)
	}
	// A project cannot launch into an unavailable/unowned workspace.
	if _, err := svc.Audit.db.Exec(`UPDATE projects SET linked_workspace_id='ws_missing' WHERE id=?`, project.ID); err != nil {
		t.Fatal(err)
	}
	if res := executeTipRequest(svc, account, project.ID, tip.ID, "execute", "workspace"); res.Code != 404 {
		t.Fatalf("workspace ownership: %d %s", res.Code, res.Body.String())
	}
	if _, err := svc.Audit.db.Exec(`UPDATE projects SET linked_workspace_id='' WHERE id=?`, project.ID); err != nil {
		t.Fatal(err)
	}
	res := executeTipRequest(svc, account, project.ID, tip.ID, "execute", "one")
	if res.Code != 502 {
		t.Fatalf("failed launch: %d %s", res.Code, res.Body.String())
	}
	current, _ := svc.Audit.ProjectTipByID(context.Background(), account.ID, project.ID, tip.ID)
	if current.Status != TipStatusInbox || current.Version != 1 {
		t.Fatalf("failed launch changed tip: %+v", current)
	}
	history, err := svc.Audit.ListProjectTipRuns(context.Background(), account.ID, project.ID, tip.ID)
	if err != nil || len(history) != 1 || history[0].Status != "failed" || history[0].SessionID != "" {
		t.Fatalf("history: %+v %v", history, err)
	}
	res = executeTipRequest(svc, account, project.ID, tip.ID, "execute", "one")
	if res.Code != 502 || !bytes.Contains(res.Body.Bytes(), []byte(`"replayed":true`)) {
		t.Fatalf("replay: %s", res.Body.String())
	}
	history, _ = svc.Audit.ListProjectTipRuns(context.Background(), account.ID, project.ID, tip.ID)
	if len(history) != 1 {
		t.Fatal("duplicate failed launch")
	}
}

func TestTipExecutionReservationConcurrent(t *testing.T) {
	svc, account, project, tip := tipExecutionService(t)
	var wg sync.WaitGroup
	var mu sync.Mutex
	success := 0
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, _, err := svc.Audit.reserveTipRun(context.Background(), tip, "execute", "chat", "", fmt.Sprint(i))
			if err == nil {
				mu.Lock()
				success++
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()
	if success != 1 {
		t.Fatalf("reserved %d launches", success)
	}
	runs, _ := svc.Audit.ListProjectTipRuns(context.Background(), account.ID, project.ID, tip.ID)
	if len(runs) != 1 {
		t.Fatalf("history: %+v", runs)
	}
}

func TestTipExecutionSuccessfulLaunchAndLinkage(t *testing.T) {
	for _, mode := range []string{"analyze", "execute"} {
		for _, workspace := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/workspace=%v", mode, workspace), func(t *testing.T) {
				svc, account, project, tip := tipExecutionService(t)
				if workspace {
					var data bytes.Buffer
					zw := zip.NewWriter(&data)
					file, _ := zw.Create("README.md")
					_, _ = file.Write([]byte("test"))
					_ = zw.Close()
					ws, err := svc.createWorkspaceFromZip("Tip workspace", account.ID, bytes.NewReader(data.Bytes()), int64(data.Len()))
					if err != nil {
						t.Fatal(err)
					}
					project, err = svc.Audit.EnsureWorkspaceProject(context.Background(), account.ID, ws.ID)
					if err != nil {
						t.Fatal(err)
					}
					tip, err = svc.Audit.CreateProjectTip(context.Background(), account.ID, CreateProjectTipInput{ProjectID: project.ID, Content: "Explain the change"})
					if err != nil {
						t.Fatal(err)
					}
				}
				received := make(chan map[string]any, 4)
				release := make(chan struct{})
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					var request map[string]any
					_ = json.NewDecoder(r.Body).Decode(&request)
					received <- request
					<-release
					w.Header().Set("Content-Type", "text/event-stream")
					fmt.Fprint(w, "data: {\"id\":\"test\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"Analysis complete\"},\"finish_reason\":null}]}\n\n")
					fmt.Fprint(w, "data: {\"id\":\"test\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
				}))
				defer server.Close()
				defer close(release)
				svc.Mem.upsertProvider(0, Provider{Name: "fake", Type: 1, Key: "test", BaseURL: server.URL + "/v1", Models: "tip-model", Status: 1, IsDefault: 1, AuthMode: "api_key"})
				res := executeTipRequest(svc, account, project.ID, tip.ID, mode, "first")
				if res.Code != 202 {
					t.Fatalf("launch: %d %s", res.Code, res.Body.String())
				}
				var body struct {
					Tip       ProjectTip    `json:"tip"`
					Execution ProjectTipRun `json:"execution"`
				}
				if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
					t.Fatal(err)
				}
				if body.Tip.Status != "doing" || body.Execution.RunID == "" || body.Execution.SessionID == "" || body.Execution.Snapshot.Status != "inbox" || body.Execution.Snapshot.Version != 1 {
					t.Fatalf("launch response: %+v", body)
				}
				expectedMode := "chat"
				if workspace {
					expectedMode = "coder"
				}
				if body.Execution.SessionMode != expectedMode || body.Execution.WorkspaceID != project.LinkedWorkspaceID {
					t.Fatalf("binding: %+v", body.Execution)
				}
				var linked string
				if err := svc.Audit.db.QueryRow(`SELECT project_id FROM session_projects WHERE account_id=? AND session_id=?`, account.ID, body.Execution.SessionID).Scan(&linked); err != nil || linked != project.ID {
					t.Fatalf("project linkage without docs: %q %v", linked, err)
				}
				select {
				case request := <-received:
					if mode == "analyze" {
						if tools, ok := request["tools"].([]any); ok && len(tools) > 0 {
							t.Fatalf("analysis exposes tools: %+v", tools)
						}
					}
				case <-time.After(10 * time.Second):
					t.Fatal("provider not called")
				}
				replay := executeTipRequest(svc, account, project.ID, tip.ID, mode, "first")
				if replay.Code != 202 || !bytes.Contains(replay.Body.Bytes(), []byte(body.Execution.RunID)) {
					t.Fatalf("replay: %s", replay.Body.String())
				}
				duplicate := executeTipRequest(svc, account, project.ID, tip.ID, mode, "second")
				if duplicate.Code != 409 {
					t.Fatalf("active duplicate: %d %s", duplicate.Code, duplicate.Body.String())
				}
				state, _ := svc.Runs.Get(body.Execution.RunID)
				// Release the fake provider and wait for normal completion before checking the Tip.
				release <- struct{}{}
				deadline := time.Now().Add(10 * time.Second)
				for state.Info().Status == runStatusRunning && time.Now().Before(deadline) {
					time.Sleep(10 * time.Millisecond)
				}
				if state.Info().Status != runStatusDone {
					t.Fatalf("run status: %+v", state.Info())
				}
				current, _ := svc.Audit.ProjectTipByID(context.Background(), account.ID, project.ID, tip.ID)
				if current.Status != "doing" {
					t.Fatalf("auto-completed tip: %+v", current)
				}
				changed := "Edited after execution"
				if _, err := svc.Audit.UpdateProjectTip(context.Background(), account.ID, UpdateProjectTipInput{ProjectID: project.ID, ID: tip.ID, Version: current.Version, Content: &changed}); err != nil {
					t.Fatal(err)
				}
				history, _ := svc.Audit.ListProjectTipRuns(context.Background(), account.ID, project.ID, tip.ID)
				if len(history) != 1 || history[0].Status != "done" || history[0].Snapshot.Content != tip.Content {
					t.Fatalf("history: %+v", history)
				}
			})
		}
	}
}
