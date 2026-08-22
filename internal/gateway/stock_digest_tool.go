package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/alex6xu/jarvisserver/internal/agentcore"
)

type StockDigestTool struct {
	Service   *StockDigestService
	AccountID int
	SessionID string
}

func (t *StockDigestTool) Name() string { return "stock_latest_digest" }

func (t *StockDigestTool) Description() string {
	return "Get the latest stock or crypto quotes, news, and sentiment in one call. " +
		"Symbols may be market-qualified codes, crypto pairs, or names; omit them to use the account watchlist. " +
		"Use delivery=required only when the user explicitly asks for a mobile notification."
}

func (t *StockDigestTool) Schema() json.RawMessage {
	return json.RawMessage(`{
  "type": "object",
  "properties": {
    "symbols": {"type": "array", "items": {"type": "string", "maxLength": 80}, "maxItems": 10},
    "days": {"type": "integer", "minimum": 1, "maximum": 30, "default": 3},
    "limit": {"type": "integer", "minimum": 1, "maximum": 20, "default": 10},
    "include_sentiment": {"type": "boolean", "default": true},
    "delivery": {"type": "string", "enum": ["never", "configured", "required"], "default": "never"}
  },
  "additionalProperties": false
}`)
}

func (t *StockDigestTool) ExecutionMode() agentcore.ToolExecutionMode {
	return agentcore.ToolExecutionSequential
}

func (t *StockDigestTool) Execute(ctx context.Context, id string, args json.RawMessage, _ agentcore.ToolUpdateFunc) (agentcore.AgentToolResult, error) {
	if t.Service == nil || t.AccountID <= 0 {
		return stockDigestToolError("stock_latest_digest is unavailable"), nil
	}
	request := StockDigestRequest{IncludeSentiment: true}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &request); err != nil {
			return stockDigestToolError("invalid arguments"), nil
		}
	}
	callID := strings.TrimSpace(t.SessionID) + ":" + strings.TrimSpace(id)
	result, err := t.Service.Latest(ctx, t.AccountID, callID, request)
	if err != nil {
		return stockDigestToolError(err.Error()), nil
	}
	payload, err := json.Marshal(result)
	if err != nil {
		return stockDigestToolError("cannot encode digest result"), nil
	}
	return agentcore.AgentToolResult{
		Content: agentcore.ContentList{agentcore.NewTextContent(string(payload))},
		Details: result,
	}, nil
}

func stockDigestToolError(message string) agentcore.AgentToolResult {
	return agentcore.AgentToolResult{
		Content: agentcore.ContentList{agentcore.NewTextContent("stock_latest_digest: " + message)},
		Details: map[string]any{"isError": true, "message": fmt.Sprintf("%s", message)},
	}
}
