package gateway

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/alex6xu/jarvisserver/internal/agentcore"
	"github.com/alex6xu/jarvisserver/internal/session"
)

func (s *GatewayStore) ListAccountTags(ctx context.Context, accountID int, kind string, limit int) ([]Tag, error) {
	if limit <= 0 || limit > 200 {
		limit = 80
	}
	query := `SELECT id,slug,name,kind,use_count,updated_at FROM account_tags WHERE account_id=?`
	args := []any{accountID}
	if kind != "" {
		if err := validateTagKind(kind); err != nil {
			return nil, err
		}
		query += ` AND kind=?`
		args = append(args, kind)
	}
	query += ` ORDER BY use_count DESC, updated_at DESC, name LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	tags := []Tag{}
	for rows.Next() {
		var tag Tag
		if err := rows.Scan(&tag.ID, &tag.Slug, &tag.Name, &tag.Kind, &tag.UseCount, &tag.UpdatedAt); err != nil {
			return nil, err
		}
		tags = append(tags, tag)
	}
	return tags, rows.Err()
}

func (s *GatewayStore) AccountTagBySlug(ctx context.Context, accountID int, slug string) (Tag, error) {
	var tag Tag
	err := s.db.QueryRowContext(ctx, `SELECT id,slug,name,kind,use_count,updated_at FROM account_tags WHERE account_id=? AND slug=?`, accountID, strings.ToLower(strings.TrimSpace(slug))).Scan(
		&tag.ID, &tag.Slug, &tag.Name, &tag.Kind, &tag.UseCount, &tag.UpdatedAt)
	return tag, err
}

type taggedMessageRef struct {
	sessionID, entryID, createdAt string
}

func (s *GatewayStore) TaggedMessages(ctx context.Context, accountID int, slug string, limit int) ([]TaggedMessage, error) {
	if limit <= 0 || limit > 200 {
		limit = 80
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT mt.session_id, mt.entry_id, mt.created_at
FROM message_tags mt JOIN account_tags t ON t.id=mt.tag_id
WHERE mt.account_id=? AND t.account_id=? AND t.slug=?
ORDER BY mt.created_at DESC LIMIT ?`, accountID, accountID, strings.ToLower(strings.TrimSpace(slug)), limit)
	if err != nil {
		return nil, err
	}
	var refs []taggedMessageRef
	for rows.Next() {
		var ref taggedMessageRef
		if err := rows.Scan(&ref.sessionID, &ref.entryID, &ref.createdAt); err != nil {
			rows.Close()
			return nil, err
		}
		refs = append(refs, ref)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	messages := make([]TaggedMessage, 0, len(refs))
	for _, ref := range refs {
		header, entries, err := s.LoadEntries(ref.sessionID)
		if err != nil || !sessionOwnedByAccount(header, accountID) {
			continue
		}
		var target *session.Entry
		for i := range entries {
			if entries[i].ID == ref.entryID {
				target = &entries[i]
				break
			}
		}
		if target == nil {
			continue
		}
		user, ok := target.Message.(agentcore.UserMessage)
		if !ok {
			continue
		}
		content := strings.TrimSpace(agentcore.ContentToText(user.Content))
		tags, err := s.messageTagHits(ctx, accountID, ref.sessionID, ref.entryID)
		if err != nil {
			return nil, err
		}
		platform := "chat"
		if sessionTypeFromHeader(header) == sessionTypeCode {
			platform = "coder"
		}
		messages = append(messages, TaggedMessage{MessageID: ref.entryID, SessionID: ref.sessionID,
			Content: content, Preview: classificationPreview(content), Platform: platform,
			SessionTitle: sessionTitle(entriesToRestored(entries, header.Model)), CreatedAt: target.Timestamp.UTC().Format(time.RFC3339Nano), Tags: tags})
	}
	return messages, nil
}

func (s *GatewayStore) messageTagHits(ctx context.Context, accountID int, sessionID, entryID string) ([]TagHit, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT t.slug,t.name,t.kind,mt.confidence
FROM message_tags mt JOIN account_tags t ON t.id=mt.tag_id
WHERE mt.account_id=? AND mt.session_id=? AND mt.entry_id=?
ORDER BY mt.confidence DESC,t.name`, accountID, sessionID, entryID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	hits := []TagHit{}
	for rows.Next() {
		var hit TagHit
		if err := rows.Scan(&hit.Slug, &hit.Name, &hit.Kind, &hit.Confidence); err != nil {
			return nil, err
		}
		hits = append(hits, hit)
	}
	return hits, rows.Err()
}

func (s *GatewayStore) TagsOverview(ctx context.Context, accountID, top, perTag int) ([]TagGroup, error) {
	if top <= 0 || top > 50 {
		top = 12
	}
	if perTag <= 0 || perTag > 20 {
		perTag = 5
	}
	tags, err := s.ListAccountTags(ctx, accountID, "", top)
	if err != nil {
		return nil, err
	}
	groups := make([]TagGroup, 0, len(tags))
	for _, tag := range tags {
		messages, err := s.TaggedMessages(ctx, accountID, tag.Slug, perTag)
		if err != nil {
			return nil, err
		}
		groups = append(groups, TagGroup{Tag: tag, Messages: messages})
	}
	return groups, nil
}

func (s *GatewayStore) classificationCounts(ctx context.Context, accountID int) (int, int, error) {
	var tags, messages int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM account_tags WHERE account_id=?`, accountID).Scan(&tags); err != nil {
		return 0, 0, err
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM message_tags WHERE account_id=?`, accountID).Scan(&messages); err != nil {
		return 0, 0, err
	}
	return tags, messages, nil
}

func isMissingAccountTag(err error) bool { return errors.Is(err, sql.ErrNoRows) }

// stableTagOrder is kept separate for deterministic tests and API responses.
func stableTagOrder(tags []Tag) {
	sort.Slice(tags, func(i, j int) bool {
		if tags[i].UseCount == tags[j].UseCount {
			return tags[i].Slug < tags[j].Slug
		}
		return tags[i].UseCount > tags[j].UseCount
	})
}
