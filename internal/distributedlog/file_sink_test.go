package distributedlog

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/zeromicro/go-zero/core/logx"
)

func TestLogxSinkWritesStructuredFile(t *testing.T) {
	dir := t.TempDir()
	logx.MustSetup(logx.LogConf{
		ServiceName: "gateway-test",
		Mode:        "file",
		Encoding:    "json",
		Path:        dir,
		Level:       "info",
		Rotation:    "size",
		MaxSize:     1,
		MaxBackups:  2,
		KeepDays:    1,
	})
	logger := New(Config{Service: "gateway", Environment: "test", InstanceID: "node-1"})
	ctx := WithRequestID(context.Background(), "req-file-1")
	logger.Info(ctx, "file sink event", F("component", "upload"))
	if err := logx.Close(); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(filepath.Join(dir, "access.log"))
	if err != nil {
		t.Fatal(err)
	}
	var entry map[string]any
	if err := json.Unmarshal(raw, &entry); err != nil {
		t.Fatalf("decode log %q: %v", raw, err)
	}
	for key, want := range map[string]string{
		"content": "file sink event", "service": "gateway", "environment": "test",
		"instance_id": "node-1", "request_id": "req-file-1", "component": "upload",
	} {
		if got := entry[key]; got != want {
			t.Errorf("field %s = %v, want %q", key, got, want)
		}
	}
}
