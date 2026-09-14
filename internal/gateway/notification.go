package gateway

import (
	"bytes"
	"context"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	notificationWeChat   = "wechat"
	notificationTelegram = "telegram"
	notificationDingTalk = "dingtalk"
)

type NotificationService struct {
	store        *GatewayStore
	box          cipher.AEAD
	httpClient   *http.Client
	allowPrivate bool
	wechatQRMu   sync.Mutex
	wechatQR     map[string]wechatQRSession
}

type NotificationChannel struct {
	Kind       string   `json:"kind"`
	Name       string   `json:"name"`
	Enabled    bool     `json:"enabled"`
	Events     []string `json:"events"`
	Configured bool     `json:"configured"`
	TargetHint string   `json:"target_hint"`
	LastError  string   `json:"last_error,omitempty"`
	LastTestAt string   `json:"last_test_at,omitempty"`
	UpdatedAt  string   `json:"updated_at"`
}

type NotificationConfig struct {
	BotToken    string `json:"bot_token,omitempty"`
	ChatID      string `json:"chat_id,omitempty"`
	WebhookURL  string `json:"webhook_url,omitempty"`
	Secret      string `json:"secret,omitempty"`
	BridgeURL   string `json:"bridge_url,omitempty"`
	AccessToken string `json:"access_token,omitempty"`
	Target      string `json:"target,omitempty"`
}

type RunNotification struct {
	RunID       string
	Status      string
	Mode        string
	Model       string
	SessionID   string
	WorkspaceID string
	Response    string
	Error       string
	Duration    time.Duration
}

type NotificationMessage struct {
	Event          string            `json:"event"`
	Title          string            `json:"title"`
	Body           string            `json:"body"`
	IdempotencyKey string            `json:"-"`
	Metadata       map[string]string `json:"metadata,omitempty"`
}

type NotificationDeliveryResult struct {
	Channel string `json:"channel"`
	Status  string `json:"status"`
	Error   string `json:"error,omitempty"`
}

type NotificationPublishResult struct {
	Event       string                       `json:"event"`
	Configured  int                          `json:"configured"`
	Sent        int                          `json:"sent"`
	AlreadySent int                          `json:"already_sent"`
	Failed      int                          `json:"failed"`
	Skipped     int                          `json:"skipped"`
	Deliveries  []NotificationDeliveryResult `json:"deliveries"`
}

func NewNotificationService(opts Options, store *GatewayStore, stateRoot string) (*NotificationService, error) {
	box, err := newGitHubCredentialBox(stateRoot, opts.GitHubTokenKey)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: 10 * time.Second}
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	return &NotificationService{store: store, box: box, httpClient: client,
		allowPrivate: opts.AllowPrivateNotificationURLs, wechatQR: make(map[string]wechatQRSession)}, nil
}

func validNotificationKind(kind string) bool {
	return kind == notificationWeChat || kind == notificationTelegram || kind == notificationDingTalk
}

func normalizeNotificationEvents(events []string) ([]string, error) {
	seen := map[string]bool{}
	result := make([]string, 0, len(events))
	for _, event := range events {
		if !validNotificationEvent(event) {
			return nil, fmt.Errorf("unsupported notification event %q", event)
		}
		if !seen[event] {
			seen[event] = true
			result = append(result, event)
		}
	}
	if len(result) == 0 {
		return nil, errors.New("at least one notification event is required")
	}
	return result, nil
}

func validNotificationEvent(event string) bool {
	return event == "run_done" || event == "run_failed" || event == "stock_digest"
}

func (s *NotificationService) encryptConfig(accountID int, kind string, config NotificationConfig) (string, error) {
	plain, err := json.Marshal(config)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, s.box.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := s.box.Seal(nonce, nonce, plain, []byte(strconv.Itoa(accountID)+":"+kind))
	return base64.RawURLEncoding.EncodeToString(sealed), nil
}

func (s *NotificationService) decryptConfig(accountID int, kind, encoded string) (NotificationConfig, error) {
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(raw) < s.box.NonceSize() {
		return NotificationConfig{}, errors.New("invalid notification credential")
	}
	nonce := raw[:s.box.NonceSize()]
	plain, err := s.box.Open(nil, nonce, raw[s.box.NonceSize():], []byte(strconv.Itoa(accountID)+":"+kind))
	if err != nil {
		return NotificationConfig{}, errors.New("cannot decrypt notification credential")
	}
	var config NotificationConfig
	err = json.Unmarshal(plain, &config)
	return config, err
}

func (s *NotificationService) List(ctx context.Context, accountID int) ([]NotificationChannel, error) {
	rows, err := s.store.db.QueryContext(ctx, `
SELECT kind, name, enabled, events_json, target_hint, last_error,
       COALESCE(last_test_at, ''), updated_at
FROM notification_channels WHERE account_id = ? ORDER BY kind`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	channels := []NotificationChannel{}
	for rows.Next() {
		var channel NotificationChannel
		var enabled int
		var eventsJSON string
		if err := rows.Scan(&channel.Kind, &channel.Name, &enabled, &eventsJSON, &channel.TargetHint,
			&channel.LastError, &channel.LastTestAt, &channel.UpdatedAt); err != nil {
			return nil, err
		}
		channel.Enabled = enabled != 0
		channel.Configured = true
		_ = json.Unmarshal([]byte(eventsJSON), &channel.Events)
		channels = append(channels, channel)
	}
	return channels, rows.Err()
}

func (s *NotificationService) channelConfig(ctx context.Context, accountID int, kind string) (NotificationChannel, NotificationConfig, error) {
	var channel NotificationChannel
	var enabled int
	var eventsJSON, ciphertext string
	err := s.store.db.QueryRowContext(ctx, `
SELECT name, enabled, events_json, config_cipher, target_hint, last_error,
       COALESCE(last_test_at, ''), updated_at
FROM notification_channels WHERE account_id = ? AND kind = ?`, accountID, kind).
		Scan(&channel.Name, &enabled, &eventsJSON, &ciphertext, &channel.TargetHint,
			&channel.LastError, &channel.LastTestAt, &channel.UpdatedAt)
	if err != nil {
		return channel, NotificationConfig{}, err
	}
	channel.Kind, channel.Enabled, channel.Configured = kind, enabled != 0, true
	_ = json.Unmarshal([]byte(eventsJSON), &channel.Events)
	config, err := s.decryptConfig(accountID, kind, ciphertext)
	return channel, config, err
}

func (s *NotificationService) Upsert(ctx context.Context, accountID int, kind, name string, enabled bool, events []string, config NotificationConfig) (NotificationChannel, error) {
	if !validNotificationKind(kind) {
		return NotificationChannel{}, errors.New("unsupported notification channel")
	}
	events, err := normalizeNotificationEvents(events)
	if err != nil {
		return NotificationChannel{}, err
	}
	if _, existing, loadErr := s.channelConfig(ctx, accountID, kind); loadErr == nil {
		config = mergeNotificationConfig(existing, config)
	} else if !errors.Is(loadErr, sql.ErrNoRows) {
		return NotificationChannel{}, loadErr
	}
	if err := s.validateConfig(ctx, kind, config); err != nil {
		return NotificationChannel{}, err
	}
	ciphertext, err := s.encryptConfig(accountID, kind, config)
	if err != nil {
		return NotificationChannel{}, err
	}
	eventsJSON, _ := json.Marshal(events)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if strings.TrimSpace(name) == "" {
		name = notificationKindName(kind)
	}
	hint := notificationTargetHint(kind, config)
	_, err = s.store.db.ExecContext(ctx, `
INSERT INTO notification_channels(account_id, kind, name, enabled, events_json, config_cipher,
 target_hint, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(account_id, kind) DO UPDATE SET name=excluded.name, enabled=excluded.enabled,
 events_json=excluded.events_json, config_cipher=excluded.config_cipher,
 target_hint=excluded.target_hint, last_error='', updated_at=excluded.updated_at`,
		accountID, kind, strings.TrimSpace(name), boolInt(enabled), string(eventsJSON), ciphertext, hint, now, now)
	if err != nil {
		return NotificationChannel{}, err
	}
	return NotificationChannel{Kind: kind, Name: name, Enabled: enabled, Events: events,
		Configured: true, TargetHint: hint, UpdatedAt: now}, nil
}

func (s *NotificationService) Delete(ctx context.Context, accountID int, kind string) error {
	_, err := s.store.db.ExecContext(ctx, `DELETE FROM notification_channels WHERE account_id = ? AND kind = ?`, accountID, kind)
	return err
}

func (s *NotificationService) Test(ctx context.Context, accountID int, kind string) error {
	_, config, err := s.channelConfig(ctx, accountID, kind)
	if err == nil {
		err = s.send(ctx, kind, config, "CodeGateway 通知渠道测试成功")
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	errorText := ""
	if err != nil {
		errorText = err.Error()
	}
	_, _ = s.store.db.ExecContext(context.Background(), `UPDATE notification_channels SET last_error=?, last_test_at=?, updated_at=? WHERE account_id=? AND kind=?`, errorText, now, now, accountID, kind)
	return err
}

func (s *NotificationService) NotifyRun(ctx context.Context, accountID int, run RunNotification) {
	event := "run_done"
	if run.Status != runStatusDone {
		event = "run_failed"
	}
	key := strings.TrimSpace(run.RunID)
	if key == "" {
		key = run.SessionID + ":" + run.Status + ":" + strconv.FormatInt(time.Now().UnixNano(), 10)
	}
	_, _ = s.Publish(ctx, accountID, NotificationMessage{
		Event: event, Title: "CodeGateway 任务通知", Body: formatRunNotification(run),
		IdempotencyKey: "run:" + key,
	})
}

// Publish sends an event to every enabled, subscribed channel. The delivery
// reservation is persisted before the network request so retries of the same
// tool call cannot duplicate a mobile notification.
func (s *NotificationService) Publish(ctx context.Context, accountID int, message NotificationMessage) (NotificationPublishResult, error) {
	message.Event = strings.TrimSpace(message.Event)
	message.IdempotencyKey = strings.TrimSpace(message.IdempotencyKey)
	if !validNotificationEvent(message.Event) {
		return NotificationPublishResult{}, fmt.Errorf("unsupported notification event %q", message.Event)
	}
	if message.IdempotencyKey == "" || len(message.IdempotencyKey) > 256 {
		return NotificationPublishResult{}, errors.New("notification idempotency key is required")
	}
	body := strings.TrimSpace(message.Body)
	if body == "" {
		return NotificationPublishResult{}, errors.New("notification body is required")
	}
	if len([]rune(body)) > 4000 {
		body = string([]rune(body)[:4000])
	}
	result := NotificationPublishResult{Event: message.Event, Deliveries: []NotificationDeliveryResult{}}
	channels, err := s.List(ctx, accountID)
	if err != nil {
		return result, err
	}
	for _, channel := range channels {
		if !channel.Enabled || !containsString(channel.Events, message.Event) {
			continue
		}
		result.Configured++
		reserved, existingStatus, reserveErr := s.reserveDelivery(ctx, accountID, message.Event, message.IdempotencyKey, channel.Kind)
		if reserveErr != nil {
			return result, reserveErr
		}
		if !reserved {
			result.Skipped++
			status := "duplicate_" + existingStatus
			if existingStatus == "sent" {
				result.AlreadySent++
			} else if existingStatus == "failed" {
				result.Failed++
			}
			result.Deliveries = append(result.Deliveries, NotificationDeliveryResult{Channel: channel.Kind, Status: status})
			continue
		}
		_, config, sendErr := s.channelConfig(ctx, accountID, channel.Kind)
		if sendErr == nil {
			sendErr = s.send(ctx, channel.Kind, config, body)
		}
		status, errorText := "sent", ""
		if sendErr != nil {
			status, errorText = "failed", sendErr.Error()
			result.Failed++
		} else {
			result.Sent++
		}
		_ = s.finishDelivery(context.Background(), accountID, message.IdempotencyKey, channel.Kind, status, errorText)
		_, _ = s.store.db.ExecContext(context.Background(), `UPDATE notification_channels SET last_error=? WHERE account_id=? AND kind=?`, errorText, accountID, channel.Kind)
		result.Deliveries = append(result.Deliveries, NotificationDeliveryResult{Channel: channel.Kind, Status: status, Error: errorText})
	}
	return result, nil
}

func (s *NotificationService) reserveDelivery(ctx context.Context, accountID int, event, key, kind string) (bool, string, error) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	result, err := s.store.db.ExecContext(ctx, `
INSERT INTO notification_deliveries(account_id, event, idempotency_key, channel_kind, status, created_at, updated_at)
VALUES (?, ?, ?, ?, 'pending', ?, ?) ON CONFLICT(account_id, idempotency_key, channel_kind) DO NOTHING`,
		accountID, event, key, kind, now, now)
	if err != nil {
		return false, "", err
	}
	affected, err := result.RowsAffected()
	if err != nil || affected == 1 {
		return affected == 1, "", err
	}
	var status string
	err = s.store.db.QueryRowContext(ctx, `
SELECT status FROM notification_deliveries
WHERE account_id=? AND idempotency_key=? AND channel_kind=?`, accountID, key, kind).Scan(&status)
	return false, status, err
}

func (s *NotificationService) finishDelivery(ctx context.Context, accountID int, key, kind, status, errorText string) error {
	_, err := s.store.db.ExecContext(ctx, `
UPDATE notification_deliveries SET status=?, error=?, updated_at=?
WHERE account_id=? AND idempotency_key=? AND channel_kind=?`,
		status, errorText, time.Now().UTC().Format(time.RFC3339Nano), accountID, key, kind)
	return err
}

func (s *NotificationService) validateConfig(ctx context.Context, kind string, config NotificationConfig) error {
	switch kind {
	case notificationTelegram:
		if strings.TrimSpace(config.BotToken) == "" || strings.TrimSpace(config.ChatID) == "" {
			return errors.New("Telegram Bot Token and Chat ID are required")
		}
	case notificationDingTalk:
		if err := validateProviderURL(ctx, config.WebhookURL, false); err != nil {
			return fmt.Errorf("invalid DingTalk webhook: %w", err)
		}
		parsed, _ := url.Parse(config.WebhookURL)
		if !strings.EqualFold(parsed.Hostname(), "oapi.dingtalk.com") {
			return errors.New("DingTalk webhook must use oapi.dingtalk.com")
		}
	case notificationWeChat:
		if strings.TrimSpace(config.Target) == "" {
			return errors.New("WeChat target is required")
		}
		if err := validateProviderURL(ctx, config.BridgeURL, s.allowPrivate); err != nil {
			return fmt.Errorf("invalid OpenClaw bridge: %w", err)
		}
	}
	return nil
}

func (s *NotificationService) send(ctx context.Context, kind string, config NotificationConfig, text string) error {
	endpoint, payload := "", any(nil)
	headers := http.Header{}
	switch kind {
	case notificationTelegram:
		endpoint = "https://api.telegram.org/bot" + url.PathEscape(config.BotToken) + "/sendMessage"
		payload = map[string]any{"chat_id": config.ChatID, "text": text}
	case notificationDingTalk:
		endpoint = signedDingTalkURL(config.WebhookURL, config.Secret)
		payload = map[string]any{"msgtype": "text", "text": map[string]string{"content": text}}
	case notificationWeChat:
		endpoint = config.BridgeURL
		payload = map[string]string{"channel": "wechat", "target": config.Target, "message": text}
		if config.AccessToken != "" {
			headers.Set("Authorization", "Bearer "+config.AccessToken)
		}
	}
	raw, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	for key, values := range headers {
		req.Header[key] = values
	}
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("notification request failed: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("notification endpoint returned HTTP %d", resp.StatusCode)
	}
	if kind == notificationTelegram {
		var result struct {
			OK          bool   `json:"ok"`
			Description string `json:"description"`
		}
		_ = json.Unmarshal(body, &result)
		if !result.OK {
			return errors.New("Telegram rejected message: " + result.Description)
		}
	}
	if kind == notificationDingTalk {
		var result struct {
			ErrCode int    `json:"errcode"`
			ErrMsg  string `json:"errmsg"`
		}
		_ = json.Unmarshal(body, &result)
		if result.ErrCode != 0 {
			return errors.New("DingTalk rejected message: " + result.ErrMsg)
		}
	}
	return nil
}

func signedDingTalkURL(rawURL, secret string) string {
	if secret == "" {
		return rawURL
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	timestamp := strconv.FormatInt(time.Now().UnixMilli(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(timestamp + "\n" + secret))
	query := parsed.Query()
	query.Set("timestamp", timestamp)
	query.Set("sign", base64.StdEncoding.EncodeToString(mac.Sum(nil)))
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

func mergeNotificationConfig(old, next NotificationConfig) NotificationConfig {
	if next.BotToken == "" {
		next.BotToken = old.BotToken
	}
	if next.ChatID == "" {
		next.ChatID = old.ChatID
	}
	if next.WebhookURL == "" {
		next.WebhookURL = old.WebhookURL
	}
	if next.Secret == "" {
		next.Secret = old.Secret
	}
	if next.BridgeURL == "" {
		next.BridgeURL = old.BridgeURL
	}
	if next.AccessToken == "" {
		next.AccessToken = old.AccessToken
	}
	if next.Target == "" {
		next.Target = old.Target
	}
	return next
}

func notificationTargetHint(kind string, config NotificationConfig) string {
	if kind == notificationTelegram {
		return config.ChatID
	}
	if kind == notificationDingTalk {
		return "钉钉群机器人"
	}
	return config.Target
}
func notificationKindName(kind string) string {
	if kind == notificationTelegram {
		return "Telegram"
	}
	if kind == notificationDingTalk {
		return "钉钉"
	}
	return "微信"
}
func formatRunNotification(run RunNotification) string {
	title := "任务已完成"
	if run.Status != runStatusDone {
		title = "任务执行失败"
	}
	detail := strings.TrimSpace(run.Response)
	if run.Error != "" {
		detail = run.Error
	}
	chars := []rune(detail)
	if len(chars) > 300 {
		detail = string(chars[:300]) + "..."
	}
	return fmt.Sprintf("CodeGateway · %s\n模式：%s\n模型：%s\n耗时：%s\n会话：%s\n%s", title, run.Mode, run.Model, run.Duration.Round(time.Second), run.SessionID, detail)
}
func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
func isMissingNotificationChannel(err error) bool { return errors.Is(err, sql.ErrNoRows) }
