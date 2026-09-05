package gateway

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const wechatQRSessionTTL = 5 * time.Minute

type wechatQRSession struct {
	ID            string
	AccountID     int
	BridgeURL     string
	AccessToken   string
	BridgeSession string
	ExpiresAt     time.Time
}

type WeChatQRStart struct {
	SessionID string `json:"session_id"`
	Status    string `json:"status"`
	QRCodeURL string `json:"qr_code_url,omitempty"`
	QRCode    string `json:"qr_code,omitempty"`
	ExpiresAt string `json:"expires_at"`
}

type WeChatQRStatus struct {
	SessionID string               `json:"session_id"`
	Status    string               `json:"status"`
	Message   string               `json:"message,omitempty"`
	Channel   *NotificationChannel `json:"channel,omitempty"`
}

type wechatBridgeQRResponse struct {
	SessionID   string `json:"session_id"`
	Status      string `json:"status"`
	QRCodeURL   string `json:"qr_code_url"`
	QRCode      string `json:"qr_code"`
	ExpiresAt   string `json:"expires_at"`
	Target      string `json:"target"`
	DisplayName string `json:"display_name"`
	AccessToken string `json:"access_token"`
	Message     string `json:"message"`
}

func (s *NotificationService) StartWeChatQR(ctx context.Context, accountID int, bridgeURL, accessToken string) (WeChatQRStart, error) {
	bridgeURL = strings.TrimSpace(bridgeURL)
	accessToken = strings.TrimSpace(accessToken)
	if _, existing, err := s.channelConfig(ctx, accountID, notificationWeChat); err == nil {
		if bridgeURL == "" {
			bridgeURL = existing.BridgeURL
		}
		if accessToken == "" {
			accessToken = existing.AccessToken
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return WeChatQRStart{}, err
	}
	if err := validateProviderURL(ctx, bridgeURL, s.allowPrivate); err != nil {
		return WeChatQRStart{}, fmt.Errorf("invalid OpenClaw bridge: %w", err)
	}

	var response wechatBridgeQRResponse
	if err := s.wechatBridgeAction(ctx, bridgeURL, accessToken, map[string]string{
		"action": "login_qr_start", "channel": "wechat",
	}, &response); err != nil {
		return WeChatQRStart{}, err
	}
	if strings.TrimSpace(response.SessionID) == "" || (strings.TrimSpace(response.QRCodeURL) == "" && strings.TrimSpace(response.QRCode) == "") {
		return WeChatQRStart{}, errors.New("OpenClaw bridge did not return a WeChat QR code")
	}
	expiresAt := time.Now().UTC().Add(wechatQRSessionTTL)
	if parsed, err := time.Parse(time.RFC3339, response.ExpiresAt); err == nil && parsed.After(time.Now()) {
		expiresAt = parsed.UTC()
	}
	localID, err := randomToken("wxqr_")
	if err != nil {
		return WeChatQRStart{}, err
	}
	s.wechatQRMu.Lock()
	for id, session := range s.wechatQR {
		if session.ExpiresAt.Before(time.Now()) || session.AccountID == accountID {
			delete(s.wechatQR, id)
		}
	}
	s.wechatQR[localID] = wechatQRSession{ID: localID, AccountID: accountID, BridgeURL: bridgeURL,
		AccessToken: accessToken, BridgeSession: response.SessionID, ExpiresAt: expiresAt}
	s.wechatQRMu.Unlock()
	return WeChatQRStart{SessionID: localID, Status: normalizeWeChatQRStatus(response.Status),
		QRCodeURL: response.QRCodeURL, QRCode: response.QRCode, ExpiresAt: expiresAt.Format(time.RFC3339)}, nil
}

func (s *NotificationService) WeChatQRStatus(ctx context.Context, accountID int, sessionID string) (WeChatQRStatus, error) {
	s.wechatQRMu.Lock()
	session, ok := s.wechatQR[sessionID]
	if ok && (session.AccountID != accountID || session.ExpiresAt.Before(time.Now())) {
		delete(s.wechatQR, sessionID)
		ok = false
	}
	s.wechatQRMu.Unlock()
	if !ok {
		return WeChatQRStatus{}, errors.New("WeChat QR session not found or expired")
	}
	var response wechatBridgeQRResponse
	if err := s.wechatBridgeAction(ctx, session.BridgeURL, session.AccessToken, map[string]string{
		"action": "login_qr_status", "channel": "wechat", "session_id": session.BridgeSession,
	}, &response); err != nil {
		return WeChatQRStatus{}, err
	}
	status := normalizeWeChatQRStatus(response.Status)
	result := WeChatQRStatus{SessionID: sessionID, Status: status, Message: response.Message}
	if status != "connected" {
		if status == "expired" || status == "cancelled" {
			s.wechatQRMu.Lock()
			delete(s.wechatQR, sessionID)
			s.wechatQRMu.Unlock()
		}
		return result, nil
	}
	if strings.TrimSpace(response.Target) == "" {
		return WeChatQRStatus{}, errors.New("OpenClaw bridge connected without a WeChat target")
	}
	accessToken := session.AccessToken
	if response.AccessToken != "" {
		accessToken = response.AccessToken
	}
	name := "微信"
	if strings.TrimSpace(response.DisplayName) != "" {
		name = "微信 · " + strings.TrimSpace(response.DisplayName)
	}
	events := []string{"run_done", "run_failed"}
	enabled := true
	if channel, _, err := s.channelConfig(ctx, accountID, notificationWeChat); err == nil {
		events, enabled = channel.Events, channel.Enabled
	}
	channel, err := s.Upsert(ctx, accountID, notificationWeChat, name, enabled, events, NotificationConfig{
		BridgeURL: session.BridgeURL, AccessToken: accessToken, Target: response.Target,
	})
	if err != nil {
		return WeChatQRStatus{}, err
	}
	s.wechatQRMu.Lock()
	delete(s.wechatQR, sessionID)
	s.wechatQRMu.Unlock()
	result.Channel = &channel
	return result, nil
}

func (s *NotificationService) wechatBridgeAction(ctx context.Context, bridgeURL, accessToken string, payload map[string]string, result *wechatBridgeQRResponse) error {
	raw, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, bridgeURL, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if accessToken != "" {
		req.Header.Set("Authorization", "Bearer "+accessToken)
	}
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("OpenClaw bridge request failed: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("OpenClaw bridge returned HTTP %d", resp.StatusCode)
	}
	if err := json.Unmarshal(body, result); err != nil {
		return errors.New("OpenClaw bridge returned invalid QR response")
	}
	return nil
}

func normalizeWeChatQRStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "scanned", "confirmed", "connected", "expired", "cancelled":
		return strings.ToLower(strings.TrimSpace(status))
	default:
		return "waiting"
	}
}
