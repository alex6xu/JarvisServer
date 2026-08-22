package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNotificationChannelCRUDAndOpenClawDelivery(t *testing.T) {
	var received struct {
		Authorization string
		Channel       string
		Target        string
		Message       string
	}
	bridge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received.Authorization = r.Header.Get("Authorization")
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		received.Channel, received.Target, received.Message = body["channel"], body["target"], body["message"]
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}))
	t.Cleanup(bridge.Close)

	root := t.TempDir()
	svc, err := NewService(Options{Cwd: root, DatabasePath: filepath.Join(root, "gateway.db"), AdminPassword: "test-password", NoTools: true, AllowPrivateNotificationURLs: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Close() })

	channel, err := svc.Notifications.Upsert(context.Background(), 1, notificationWeChat, "微信", true,
		[]string{"run_done", "run_failed"}, NotificationConfig{BridgeURL: bridge.URL, AccessToken: "bridge-secret", Target: "wx-user"})
	if err != nil {
		t.Fatal(err)
	}
	if !channel.Configured || channel.TargetHint != "wx-user" {
		t.Fatalf("channel=%+v", channel)
	}

	channels, err := svc.Notifications.List(context.Background(), 1)
	if err != nil || len(channels) != 1 {
		t.Fatalf("channels=%+v err=%v", channels, err)
	}
	raw, _ := json.Marshal(channels)
	if strings.Contains(string(raw), "bridge-secret") || strings.Contains(string(raw), bridge.URL) {
		t.Fatalf("secret leaked: %s", raw)
	}

	if err := svc.Notifications.Test(context.Background(), 1, notificationWeChat); err != nil {
		t.Fatal(err)
	}
	if received.Authorization != "Bearer bridge-secret" || received.Channel != "wechat" || received.Target != "wx-user" || !strings.Contains(received.Message, "测试成功") {
		t.Fatalf("received=%+v", received)
	}
	if err := svc.Notifications.Delete(context.Background(), 1, notificationWeChat); err != nil {
		t.Fatal(err)
	}
}

func TestNotificationValidationAndFormatting(t *testing.T) {
	if _, err := normalizeNotificationEvents([]string{"unknown"}); err == nil {
		t.Fatal("expected invalid event")
	}
	if got := formatRunNotification(RunNotification{Status: runStatusDone, Mode: "coder", Model: "gpt", SessionID: "s1", Duration: 2 * time.Second, Response: "done"}); !strings.Contains(got, "任务已完成") || !strings.Contains(got, "s1") {
		t.Fatalf("message=%q", got)
	}

	raw := signedDingTalkURL("https://oapi.dingtalk.com/robot/send?access_token=test", "secret")
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Query().Get("timestamp") == "" || parsed.Query().Get("sign") == "" || parsed.Query().Get("access_token") != "test" {
		t.Fatalf("signed URL=%s", raw)
	}
}
