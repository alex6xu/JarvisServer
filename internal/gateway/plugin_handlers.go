package gateway

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
)

type pluginStatusBody struct {
	Enabled bool `json:"enabled"`
}

type pluginInstallBody struct {
	Reference string `json:"reference"`
}

type pluginUninstallBody struct {
	Package string `json:"package"`
}

func (s *Service) handleAdminListPlugins(w http.ResponseWriter, r *http.Request) {
	if s.Plugins == nil {
		writeJSON(w, http.StatusOK, map[string]any{"plugins": []PluginSummary{}, "directory": ""})
		return
	}
	plugins, err := s.Plugins.List()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"plugins": plugins, "directory": s.Plugins.Directory()})
}

func (s *Service) handleAdminPluginStatus(w http.ResponseWriter, r *http.Request) {
	if s.Plugins == nil {
		writeErr(w, http.StatusServiceUnavailable, "plugin registry is unavailable")
		return
	}
	var body pluginStatusBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json body")
		return
	}
	id := strings.TrimSpace(pathParam(r, "id"))
	if err := s.Plugins.SetEnabled(id, body.Enabled); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			writeErr(w, http.StatusNotFound, "plugin not found")
			return
		}
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	plugins, err := s.Plugins.List()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	for _, item := range plugins {
		if item.ID == id {
			writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "plugin": item})
			return
		}
	}
	writeErr(w, http.StatusNotFound, "plugin not found")
}

func (s *Service) handleAdminInstallPlugin(w http.ResponseWriter, r *http.Request) {
	if s.Plugins == nil {
		writeErr(w, http.StatusServiceUnavailable, "plugin registry is unavailable")
		return
	}
	var body pluginInstallBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json body")
		return
	}
	result, plugins, err := s.Plugins.Install(strings.TrimSpace(body.Reference))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"status": "ok", "package": result, "plugins": plugins, "directory": s.Plugins.Directory(),
	})
}

func (s *Service) handleAdminUninstallPlugin(w http.ResponseWriter, r *http.Request) {
	if s.Plugins == nil {
		writeErr(w, http.StatusServiceUnavailable, "plugin registry is unavailable")
		return
	}
	var body pluginUninstallBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json body")
		return
	}
	plugins, err := s.Plugins.Uninstall(strings.TrimSpace(body.Package))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			writeErr(w, http.StatusNotFound, "installed plugin package not found")
			return
		}
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "ok", "plugins": plugins, "directory": s.Plugins.Directory(),
	})
}

func (s *Service) handleAdminReloadPlugins(w http.ResponseWriter, r *http.Request) {
	if s.Plugins == nil {
		writeErr(w, http.StatusServiceUnavailable, "plugin registry is unavailable")
		return
	}
	plugins, err := s.Plugins.Reload()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	loaded, failed := 0, 0
	for _, item := range plugins {
		if item.Status == "ready" {
			loaded++
		} else if item.Status == "load_error" {
			failed++
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"plugins": plugins, "directory": s.Plugins.Directory(), "loaded": loaded, "failed": failed,
	})
}
