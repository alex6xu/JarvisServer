package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/alex6xu/jarvisserver/internal/agentcore"
	"github.com/alex6xu/jarvisserver/internal/session"
)

const localClassifierVersion = 1

type localTagRule struct {
	Slug     string
	Name     string
	Kind     string
	Keywords []string
}

type localTagMatch struct {
	Rule       localTagRule
	Confidence float64
	Evidence   []string
}

type MessageTagHit struct {
	Slug       string  `json:"slug"`
	Name       string  `json:"name"`
	Kind       string  `json:"kind"`
	Confidence float64 `json:"confidence"`
}

type TagGroup struct {
	Tag      Tag             `json:"tag"`
	Messages []TaggedMessage `json:"messages"`
}

var localTagRules = []localTagRule{
	{Slug: "programming", Name: "编程开发", Kind: "category", Keywords: []string{"代码", "编程", "开发", "实现", "function", "class", "compile", "build"}},
	{Slug: "debugging", Name: "调试排错", Kind: "category", Keywords: []string{"报错", "错误", "失败", "异常", "排查", "修复", "debug", "bug", "panic", "crash", "timeout", "cannot", "failed", "http 404", "http 500"}},
	{Slug: "deployment", Name: "部署运维", Kind: "category", Keywords: []string{"部署", "发布", "上线", "重启", "服务", "systemctl", "systemd", "caddy", "nginx", "healthz", "二进制"}},
	{Slug: "testing", Name: "测试", Kind: "category", Keywords: []string{"测试", "单元测试", "集成测试", "test", "vitest", "pytest", "coverage"}},
	{Slug: "refactoring", Name: "重构", Kind: "category", Keywords: []string{"重构", "优化代码", "可维护性", "refactor"}},
	{Slug: "documentation", Name: "文档", Kind: "category", Keywords: []string{"文档", "说明书", "方案", "设计", "readme", "documentation"}},
	{Slug: "authentication", Name: "认证安全", Kind: "topic", Keywords: []string{"认证", "授权", "登录", "权限", "密钥", "token", "oauth", "ssh key", "api key", "password"}},
	{Slug: "github", Name: "GitHub", Kind: "topic", Keywords: []string{"github", "git push", "git pull", "repository", "repo", "deploy key", "pull request"}},
	{Slug: "git", Name: "Git", Kind: "topic", Keywords: []string{"git ", "commit", "rebase", "merge", "fast-forward", "分支"}},
	{Slug: "golang", Name: "Go", Kind: "topic", Keywords: []string{"golang", "go test", "go build", ".go", "go.mod"}},
	{Slug: "frontend", Name: "前端", Kind: "topic", Keywords: []string{"前端", "页面", "浏览器", "react", "typescript", "tsx", "vite", "npm"}},
	{Slug: "backend", Name: "后端", Kind: "topic", Keywords: []string{"后端", "gateway", "接口", "api", "handler", "middleware", "server"}},
	{Slug: "database", Name: "数据库", Kind: "topic", Keywords: []string{"数据库", "sqlite", "sql", "migration", "数据表"}},
	{Slug: "docker", Name: "Docker", Kind: "topic", Keywords: []string{"docker", "container", "容器", "dockerfile", "compose"}},
	{Slug: "linux", Name: "Linux", Kind: "topic", Keywords: []string{"linux", "systemd", "systemctl", "bash", "shell", "chmod", "服务器"}},
	{Slug: "ai", Name: "AI / 模型", Kind: "topic", Keywords: []string{"llm", "大模型", "模型", "embedding", "向量", "prompt", "上下文", "token"}},
	{Slug: "workspace", Name: "工作区", Kind: "topic", Keywords: []string{"workspace", "工作区", "worktree"}},
	{Slug: "crypto", Name: "加密货币", Kind: "topic", Keywords: []string{"btc", "eth", "比特币", "以太坊", "加密货币", "清算", "爆仓", "crypto"}},
	{Slug: "stocks", Name: "股票投资", Kind: "topic", Keywords: []string{"股票", "行情", "美股", "a股", "港股", "stock", "watchlist"}},
	{Slug: "hardware", Name: "硬件", Kind: "topic", Keywords: []string{"主板", "cpu", "内存", "电源", "显卡", "bios", "硬件", "xeon", "lga"}},
}

func classifyTextLocally(text string) []localTagMatch {
	normalized := normalizeClassificationText(text)
	if normalized == "" {
		return nil
	}
	matches := make([]localTagMatch, 0, 8)
	for _, rule := range localTagRules {
		var evidence []string
		for _, keyword := range rule.Keywords {
			if classificationContains(normalized, strings.ToLower(keyword)) {
				evidence = append(evidence, keyword)
			}
		}
		if len(evidence) == 0 {
			continue
		}
		confidence := 0.58 + float64(min(len(evidence), 3))*0.12
		if len(evidence[0]) >= 6 {
			confidence += 0.06
		}
		if confidence > 0.98 {
			confidence = 0.98
		}
		matches = append(matches, localTagMatch{Rule: rule, Confidence: confidence, Evidence: evidence})
	}
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].Confidence == matches[j].Confidence {
			return matches[i].Rule.Slug < matches[j].Rule.Slug
		}
		return matches[i].Confidence > matches[j].Confidence
	})
	categories, topics := 0, 0
	filtered := matches[:0]
	for _, match := range matches {
		if match.Rule.Kind == "category" {
			if categories >= 3 {
				continue
			}
			categories++
		} else {
			if topics >= 5 {
				continue
			}
			topics++
		}
		filtered = append(filtered, match)
	}
	return filtered
}

func normalizeClassificationText(text string) string {
	text = strings.ToLower(strings.TrimSpace(text))
	if len(text) > 16<<10 {
		text = text[:16<<10]
	}
	var b strings.Builder
	space := false
	for _, r := range text {
		if unicode.IsLetter(r) || unicode.IsNumber(r) || strings.ContainsRune("._/-+:#", r) {
			b.WriteRune(r)
			space = false
		} else if !space {
			b.WriteByte(' ')
			space = true
		}
	}
	return strings.TrimSpace(b.String())
}

func classificationContains(text, keyword string) bool {
	keyword = strings.TrimSpace(keyword)
	if keyword == "" {
		return false
	}
	return strings.Contains(text, keyword)
}

func (s *GatewayStore) ClassifyStoredUserMessage(ctx context.Context, header session.SessionHeader, entry session.Entry) error {
	user, ok := entry.Message.(agentcore.UserMessage)
	if !ok || header.AccountID <= 0 || entry.ID == "" {
		return nil
	}
	text := strings.TrimSpace(agentcore.ContentToText(user.Content))
	matches := classifyTextLocally(text)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM message_tags WHERE account_id=? AND session_id=? AND entry_id=? AND source='rule'`, header.AccountID, header.ID, entry.ID); err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, match := range matches {
		tagID := ""
		err := tx.QueryRowContext(ctx, `SELECT id FROM account_tags WHERE account_id=? AND slug=?`, header.AccountID, match.Rule.Slug).Scan(&tagID)
		if err != nil {
			tagID = newID("tag")
			if _, err := tx.ExecContext(ctx, `INSERT INTO account_tags(id,account_id,slug,name,kind,description,source,use_count,created_at,updated_at) VALUES(?,?,?,?,?,?, 'system',0,?,?)`, tagID, header.AccountID, match.Rule.Slug, match.Rule.Name, match.Rule.Kind, "本地规则自动分类", now, now); err != nil {
				return err
			}
		}
		evidence, _ := json.Marshal(map[string]any{"matched_keywords": match.Evidence})
		if _, err := tx.ExecContext(ctx, `INSERT INTO message_tags(account_id,session_id,entry_id,tag_id,confidence,source,evidence_json,classifier_version,created_at,updated_at) VALUES(?,?,?,?,?,'rule',?,?,?,?) ON CONFLICT(session_id,entry_id,tag_id) DO UPDATE SET confidence=excluded.confidence,source=excluded.source,evidence_json=excluded.evidence_json,classifier_version=excluded.classifier_version,updated_at=excluded.updated_at`, header.AccountID, header.ID, entry.ID, tagID, match.Confidence, string(evidence), localClassifierVersion, now, now); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE account_tags SET use_count=(SELECT COUNT(*) FROM message_tags mt WHERE mt.tag_id=account_tags.id), updated_at=? WHERE account_id=?`, now, header.AccountID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *GatewayStore) RetagAccountMessages(ctx context.Context, accountID, limit int) (int, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	headers, err := s.List()
	if err != nil {
		return 0, err
	}
	classified := 0
	for _, header := range headers {
		if !sessionOwnedByAccount(header, accountID) {
			continue
		}
		_, entries, err := s.LoadEntries(header.ID)
		if err != nil {
			return classified, err
		}
		for _, entry := range entries {
			if _, ok := entry.Message.(agentcore.UserMessage); !ok {
				continue
			}
			if err := s.ClassifyStoredUserMessage(ctx, header, entry); err != nil {
				return classified, err
			}
			classified++
			if classified >= limit {
				return classified, nil
			}
		}
	}
	return classified, nil
}

func classificationPreview(text string) string {
	text = strings.TrimSpace(text)
	runes := []rune(text)
	if len(runes) > 180 {
		return string(runes[:180]) + "…"
	}
	return text
}

func validateTagKind(kind string) error {
	if kind != "category" && kind != "topic" {
		return fmt.Errorf("invalid tag kind %q", kind)
	}
	return nil
}
