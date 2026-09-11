package gateway

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

func TestProjectTipsCRUDAndIsolation(t *testing.T) {
	store := newTestGatewayStore(t)
	ctx := context.Background()
	first, err := store.CreateAccount(ctx, "tips-first", "", "user", "password")
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.CreateAccount(ctx, "tips-second", "", "user", "password")
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, first.ID, "Ideas", "")
	if err != nil {
		t.Fatal(err)
	}

	tip, err := store.CreateProjectTip(ctx, first.ID, CreateProjectTipInput{
		ProjectID: project.ID,
		Type:      TipTypeIdea,
		Content:   "Move token refresh into the provider layer",
		Priority:  3,
		DueAt:     "2026-09-10T10:00:00+08:00",
	})
	if err != nil {
		t.Fatal(err)
	}
	if tip.Status != TipStatusInbox || tip.Version != 1 || tip.Priority != 3 || tip.DueAt != "2026-09-10T02:00:00Z" {
		t.Fatalf("created tip = %+v", tip)
	}

	listed, err := store.ListProjectTips(ctx, first.ID, project.ID, ProjectTipListOptions{Type: TipTypeIdea, Query: "provider"})
	if err != nil || len(listed) != 1 || listed[0].ID != tip.ID {
		t.Fatalf("listed tips = %+v, err = %v", listed, err)
	}
	if _, err := store.ProjectTipByID(ctx, second.ID, project.ID, tip.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("foreign account read err = %v, want sql.ErrNoRows", err)
	}

	status := TipStatusDone
	updated, err := store.UpdateProjectTip(ctx, first.ID, UpdateProjectTipInput{
		ProjectID: project.ID,
		ID:        tip.ID,
		Version:   tip.Version,
		Status:    &status,
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != TipStatusDone || updated.Version != 2 || updated.CompletedAt == "" {
		t.Fatalf("updated tip = %+v", updated)
	}
	if _, err := store.UpdateProjectTip(ctx, first.ID, UpdateProjectTipInput{ProjectID: project.ID, ID: tip.ID, Version: 1, Status: &status}); !errors.Is(err, ErrTipVersionConflict) {
		t.Fatalf("stale update err = %v, want conflict", err)
	}
	if err := store.DeleteProjectTip(ctx, second.ID, project.ID, tip.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("foreign delete err = %v, want sql.ErrNoRows", err)
	}
	if err := store.DeleteProjectTip(ctx, first.ID, project.ID, tip.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ProjectTipByID(ctx, first.ID, project.ID, tip.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("deleted tip err = %v, want sql.ErrNoRows", err)
	}
}

func TestProjectTipsValidateInputAndMigration(t *testing.T) {
	store := newTestGatewayStore(t)
	ctx := context.Background()
	var count int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version=24`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("migration count = %d, err = %v", count, err)
	}
	account, _ := store.CreateAccount(ctx, "tips-validation", "", "user", "password")
	project, _ := store.CreateProject(ctx, account.ID, "Validation", "")

	cases := []CreateProjectTipInput{
		{ProjectID: project.ID, Type: "invalid", Content: "content"},
		{ProjectID: project.ID, Type: TipTypeNote, Content: "   "},
		{ProjectID: project.ID, Type: TipTypeTodo, Content: "content", Priority: 4},
		{ProjectID: project.ID, Type: TipTypeTodo, Content: "content", DueAt: "tomorrow"},
	}
	for _, input := range cases {
		if _, err := store.CreateProjectTip(ctx, account.ID, input); err == nil {
			t.Fatalf("invalid input accepted: %+v", input)
		}
	}
	if tips, err := store.ListProjectTips(ctx, account.ID, project.ID, ProjectTipListOptions{}); err != nil || tips == nil || len(tips) != 0 {
		t.Fatalf("empty tips = %#v, err = %v", tips, err)
	}
}
