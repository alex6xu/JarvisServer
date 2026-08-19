package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

func (s *Service) handleListWorkspaces(w http.ResponseWriter, _ *http.Request) {
	list, err := s.listWorkspaces()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"workspaces": list})
}

func (s *Service) handleUploadWorkspace(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(256 << 20); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid multipart form")
		return
	}
	name := r.FormValue("name")
	file, _, err := r.FormFile("archive")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "archive is required")
		return
	}
	defer file.Close()
	data, err := io.ReadAll(file)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	info, err := s.createWorkspaceFromZip(name, bytesReader(data), int64(len(data)))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"workspace": info})
}

func (s *Service) handleDownloadWorkspace(w http.ResponseWriter, r *http.Request) {
	id := pathParam(r, "id")
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", "attachment; filename="+id+".zip")
	if err := s.zipWorkspace(id, w); err != nil {
		writeErr(w, http.StatusNotFound, err.Error())
	}
}

func (s *Service) handleDeleteWorkspace(w http.ResponseWriter, r *http.Request) {
	if err := s.deleteWorkspace(pathParam(r, "id")); err != nil {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Service) handleGitHubStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"configured": false, "connected": false, "github_login": "",
	})
}

func (s *Service) handleGitHubAuthorize(w http.ResponseWriter, _ *http.Request) {
	writeErr(w, http.StatusBadRequest, "github oauth is not configured")
}

func (s *Service) handleGitHubDisconnect(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Service) handleGitHubRepos(w http.ResponseWriter, _ *http.Request) {
	writeErr(w, http.StatusBadRequest, "github oauth is not configured")
}

func (s *Service) handleGitHubImport(w http.ResponseWriter, _ *http.Request) {
	writeErr(w, http.StatusBadRequest, "github oauth is not configured")
}

func (s *Service) handleGitHubPull(w http.ResponseWriter, _ *http.Request) {
	writeErr(w, http.StatusBadRequest, "github oauth is not configured")
}

func (s *Service) handleGitHubPush(w http.ResponseWriter, _ *http.Request) {
	writeErr(w, http.StatusBadRequest, "github oauth is not configured")
}

func (s *Service) handleClaudeOAuthStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"configured": false, "connected": false, "email": "",
	})
}

func (s *Service) handleClaudeOAuthAuthorize(w http.ResponseWriter, _ *http.Request) {
	writeErr(w, http.StatusBadRequest, "claude_oauth is not configured")
}

func (s *Service) handleClaudeOAuthExchange(w http.ResponseWriter, _ *http.Request) {
	writeErr(w, http.StatusBadRequest, "claude_oauth is not configured")
}

func (s *Service) handleClaudeOAuthDisconnect(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Service) handleASRStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"enabled": false})
}

func (s *Service) handleASR(w http.ResponseWriter, _ *http.Request) {
	writeErr(w, http.StatusBadRequest, "asr is not configured")
}

func (s *Service) handleListTasks(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"tasks": s.Mem.listTasks()})
}

func (s *Service) handleCreateTask(w http.ResponseWriter, r *http.Request) {
	var body struct {
		WorkspaceID  string `json:"workspace_id"`
		RouteProfile string `json:"route_profile"`
		Type         string `json:"type"`
		Prompt       string `json:"prompt"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json body")
		return
	}
	if body.WorkspaceID == "" || body.Prompt == "" {
		writeErr(w, http.StatusBadRequest, "workspace_id and prompt are required")
		return
	}
	profileID := ""
	if p := s.Mem.findProfileByName(body.RouteProfile); p != nil {
		profileID = p.ID
	}
	task := s.Mem.createTask(AgentTask{
		WorkspaceID:    body.WorkspaceID,
		RouteProfileID: profileID,
		Type:           body.Type,
		Prompt:         body.Prompt,
	})
	if err := s.Control.UpsertAgentTask(r.Context(), task); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	model := ""
	if profile := s.Mem.findProfileByName(body.RouteProfile); profile != nil && len(profile.Models) > 0 {
		model = profile.Models[0]
	}
	go func(id string) {
		running, _ := s.Mem.updateTask(id, func(t *AgentTask) {
			t.Status = "running"
		})
		if err := s.Control.UpsertAgentTask(context.Background(), running); err != nil {
			fmt.Fprintf(os.Stderr, "gateway: persist running task %s: %v\n", id, err)
		}
		response, err := s.StartChat(context.Background(), ChatRequest{
			Message: body.Prompt, Model: model, WorkspaceID: body.WorkspaceID, Mode: "coder",
		})
		if err != nil {
			s.finishTask(id, "", nil, err)
			return
		}
		run, ok := s.Runs.Get(response.RunID)
		if !ok {
			s.finishTask(id, "", nil, fmt.Errorf("run %s not found", response.RunID))
			return
		}
		var result string
		var steps []ToolStep
		for event := range run.Subscribe(0) {
			if event.Payload.Type == "done" {
				result = event.Payload.Content
				steps = event.Payload.ToolSteps
			}
		}
		s.finishTask(id, result, steps, run.Err)
	}(task.ID)
	writeJSON(w, http.StatusAccepted, task)
}

func (s *Service) finishTask(id, result string, steps []ToolStep, runErr error) {
	task, ok := s.Mem.updateTask(id, func(t *AgentTask) {
		if runErr != nil {
			t.Status = "failed"
			t.Error = runErr.Error()
		} else {
			t.Status = "completed"
			t.Result = result
			t.ToolSteps = steps
		}
		t.FinishedAt = time.Now().UTC().Format(time.RFC3339)
	})
	if ok {
		if err := s.Control.UpsertAgentTask(context.Background(), task); err != nil {
			fmt.Fprintf(os.Stderr, "gateway: persist finished task %s: %v\n", id, err)
		}
	}
}

func (s *Service) handleListTags(w http.ResponseWriter, _ *http.Request) {
	s.Mem.ensureDemoTag()
	writeJSON(w, http.StatusOK, map[string]any{"tags": s.Mem.listTags()})
}

func (s *Service) handleTagsOverview(w http.ResponseWriter, _ *http.Request) {
	s.Mem.ensureDemoTag()
	writeJSON(w, http.StatusOK, map[string]any{"groups": []any{}})
}

func (s *Service) handleGetTag(w http.ResponseWriter, r *http.Request) {
	slug := pathParam(r, "slug")
	tag, ok := s.Mem.getTag(slug)
	if !ok {
		s.Mem.ensureDemoTag()
		tag, ok = s.Mem.getTag(slug)
	}
	if !ok {
		writeErr(w, http.StatusNotFound, "tag not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tag": tag, "messages": []any{}})
}

func (s *Service) handleRetag(w http.ResponseWriter, _ *http.Request) {
	s.Mem.ensureDemoTag()
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
