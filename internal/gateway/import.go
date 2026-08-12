package gateway

import (
	"bytes"
	"strings"
	"time"

	"github.com/smallnest/pigo/internal/agentcore"
	"github.com/smallnest/pigo/internal/session"
)

func bytesReader(b []byte) *bytes.Reader { return bytes.NewReader(b) }

// ImportPreview parses pasted markdown into title + role/content pairs.
func ImportPreview(content, title string) (string, []map[string]string) {
	msgs := parseImportMarkdown(content)
	if title == "" {
		title = "Imported"
		for _, m := range msgs {
			if m["role"] == "user" && strings.TrimSpace(m["content"]) != "" {
				t := strings.TrimSpace(m["content"])
				if len(t) > 64 {
					t = t[:64] + "…"
				}
				title = t
				break
			}
		}
	}
	return title, msgs
}

func parseImportMarkdown(content string) []map[string]string {
	lines := strings.Split(content, "\n")
	var out []map[string]string
	role := ""
	var buf strings.Builder
	flush := func() {
		if role == "" {
			return
		}
		text := strings.TrimSpace(buf.String())
		if text == "" {
			buf.Reset()
			return
		}
		out = append(out, map[string]string{"role": role, "content": text})
		buf.Reset()
	}
	for _, line := range lines {
		trim := strings.TrimSpace(line)
		lower := strings.ToLower(trim)
		switch {
		case strings.HasPrefix(lower, "# "):
			continue
		case strings.HasPrefix(lower, "### user"), strings.HasPrefix(lower, "## user"), strings.HasPrefix(lower, "user:"):
			flush()
			role = "user"
			if i := strings.Index(line, ":"); i >= 0 && strings.HasPrefix(lower, "user:") {
				buf.WriteString(strings.TrimSpace(line[i+1:]))
				buf.WriteByte('\n')
			}
		case strings.HasPrefix(lower, "### assistant"), strings.HasPrefix(lower, "## assistant"), strings.HasPrefix(lower, "assistant:"):
			flush()
			role = "assistant"
			if i := strings.Index(line, ":"); i >= 0 && strings.HasPrefix(lower, "assistant:") {
				buf.WriteString(strings.TrimSpace(line[i+1:]))
				buf.WriteByte('\n')
			}
		default:
			if role == "" {
				role = "user"
			}
			buf.WriteString(line)
			buf.WriteByte('\n')
		}
	}
	flush()
	if len(out) == 0 && strings.TrimSpace(content) != "" {
		out = append(out, map[string]string{"role": "user", "content": strings.TrimSpace(content)})
	}
	return out
}

func (s *Service) importSession(content, title string) (SessionMeta, error) {
	title, pairs := ImportPreview(content, title)
	now := time.Now().UTC()
	header := session.SessionHeader{
		ID:        session.NewID(now),
		CreatedAt: now,
		UpdatedAt: now,
		Model:     s.Opts.Model,
		Cwd:       s.Opts.Cwd,
	}
	msgs := make(agentcore.MessageList, 0, len(pairs))
	for _, p := range pairs {
		text := p["content"]
		switch p["role"] {
		case "assistant":
			msgs = append(msgs, agentcore.AssistantMessage{
				RoleField: agentcore.RoleAssistant,
				Content:   agentcore.ContentList{agentcore.NewTextContent(text)},
			})
		default:
			msgs = append(msgs, agentcore.UserMessage{
				RoleField: agentcore.RoleUser,
				Content:   agentcore.ContentList{agentcore.NewTextContent(text)},
			})
		}
	}
	if err := s.Store.Save(header, msgs); err != nil {
		return SessionMeta{}, err
	}
	return SessionMeta{
		ID:           header.ID,
		Title:        title,
		Platform:     "import",
		MessageCount: len(msgs),
		Model:        header.Model,
		CreatedAt:    header.CreatedAt.Format(time.RFC3339),
		UpdatedAt:    header.UpdatedAt.Format(time.RFC3339),
	}, nil
}
