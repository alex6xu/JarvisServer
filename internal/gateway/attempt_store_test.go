package gateway

import (
	"context"
	"testing"
	"time"

	"github.com/alex6xu/jarvisserver/internal/agentcore"
)

func TestSaveRunCheckpointKeepsLatestTurnOnly(t *testing.T) {
	store := newTestGatewayStore(t)
	ctx := context.Background()
	if _, err := store.db.Exec(`INSERT INTO runs(id,session_id,model,status,created_at) VALUES('run-checkpoint','session-checkpoint','model','running',?)`, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	for turn := 1; turn <= 3; turn++ {
		if err := store.SaveRunCheckpoint(ctx, RunCheckpoint{
			RunID: "run-checkpoint", Turn: turn, SessionID: "session-checkpoint", Model: "model",
			Messages:  agentcore.MessageList{agentcore.UserMessage{RoleField: agentcore.RoleUser, Content: agentcore.ContentList{agentcore.NewTextContent("turn")}}},
			CreatedAt: time.Now().UTC(),
		}); err != nil {
			t.Fatal(err)
		}
	}
	var count, turn int
	if err := store.db.QueryRow(`SELECT COUNT(*),MAX(turn) FROM run_checkpoints WHERE run_id='run-checkpoint'`).Scan(&count, &turn); err != nil {
		t.Fatal(err)
	}
	if count != 1 || turn != 3 {
		t.Fatalf("checkpoint count=%d turn=%d, want one latest turn", count, turn)
	}
	checkpoint, err := store.LoadLatestRunCheckpoint(ctx, "run-checkpoint")
	if err != nil || checkpoint.Turn != 3 {
		t.Fatalf("checkpoint=%+v err=%v", checkpoint, err)
	}
}
