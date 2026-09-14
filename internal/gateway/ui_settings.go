package gateway

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
)

const uiSettingsSchema = `
CREATE TABLE IF NOT EXISTS ui_settings (
    id INTEGER PRIMARY KEY CHECK(id = 1),
    home_layout TEXT NOT NULL CHECK(home_layout IN ('classic', 'workbench'))
);
`

type UISettings struct {
	HomeLayout string `json:"home_layout"`
}

func (s *GatewayStore) LoadUISettings(ctx context.Context) (UISettings, error) {
	settings := UISettings{HomeLayout: "classic"}
	err := s.db.QueryRowContext(ctx, `SELECT home_layout FROM ui_settings WHERE id = 1`).Scan(&settings.HomeLayout)
	if errors.Is(err, sql.ErrNoRows) {
		return settings, nil
	}
	return settings, err
}

func (s *GatewayStore) SaveUISettings(ctx context.Context, settings UISettings) error {
	if settings.HomeLayout != "classic" && settings.HomeLayout != "workbench" {
		return errors.New("home_layout must be classic or workbench")
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO ui_settings(id, home_layout) VALUES(1, ?)
ON CONFLICT(id) DO UPDATE SET home_layout = excluded.home_layout`, settings.HomeLayout)
	return err
}

func (s *Service) handleGetUISettings(w http.ResponseWriter, r *http.Request) {
	settings, err := s.Audit.LoadUISettings(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "cannot load UI settings")
		return
	}
	writeJSON(w, http.StatusOK, settings)
}

func (s *Service) handleUpdateUISettings(w http.ResponseWriter, r *http.Request) {
	account, ok := requestAccount(r)
	if !ok || account.Role != "admin" {
		writeErr(w, http.StatusForbidden, "admin role required")
		return
	}
	var settings UISettings
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&settings); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json body")
		return
	}
	if settings.HomeLayout != "classic" && settings.HomeLayout != "workbench" {
		writeErr(w, http.StatusBadRequest, "home_layout must be classic or workbench")
		return
	}
	if err := s.Audit.SaveUISettings(r.Context(), settings); err != nil {
		writeErr(w, http.StatusInternalServerError, "cannot save UI settings")
		return
	}
	writeJSON(w, http.StatusOK, settings)
}
