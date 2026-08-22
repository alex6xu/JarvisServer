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
		[]string{"run_done", "run_failed", "stock_digest"}, NotificationConfig{BridgeURL: bridge.URL, AccessToken: "bridge-secret", Target: "wx-user"})
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

func TestNotificationPublishIsIdempotent(t *testing.T) {
	requests := 0
	fail := false
	bridge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		if fail {
			http.Error(w, "failed", http.StatusBadGateway)
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}))
	t.Cleanup(bridge.Close)
	root := t.TempDir()
	svc, err := NewService(Options{Cwd: root, DatabasePath: filepath.Join(root, "gateway.db"), AdminPassword: "test-password", NoTools: true, AllowPrivateNotificationURLs: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Close() })
	_, err = svc.Notifications.Upsert(context.Background(), 1, notificationWeChat, "微信", true,
		[]string{"stock_digest"}, NotificationConfig{BridgeURL: bridge.URL, Target: "wx-user"})
	if err != nil {
		t.Fatal(err)
	}
	message := NotificationMessage{Event: "stock_digest", Body: "digest", IdempotencyKey: "digest-1"}
	first, err := svc.Notifications.Publish(context.Background(), 1, message)
	if err != nil || first.Sent != 1 {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	second, err := svc.Notifications.Publish(context.Background(), 1, message)
	if err != nil || second.Skipped != 1 || second.AlreadySent != 1 || requests != 1 {
		t.Fatalf("second=%+v requests=%d err=%v", second, requests, err)
	}
	fail = true
	failedMessage := NotificationMessage{Event: "stock_digest", Body: "digest", IdempotencyKey: "digest-2"}
	failed, err := svc.Notifications.Publish(context.Background(), 1, failedMessage)
	if err != nil || failed.Failed != 1 {
		t.Fatalf("failed=%+v err=%v", failed, err)
	}
	duplicateFailed, err := svc.Notifications.Publish(context.Background(), 1, failedMessage)
	if err != nil || duplicateFailed.Failed != 1 || duplicateFailed.AlreadySent != 0 || requests != 2 {
		t.Fatalf("duplicate failed=%+v requests=%d err=%v", duplicateFailed, requests, err)
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
