// This file implements the plugin client (US-016, #132): it launches a plugin
// executable, performs the initialize handshake, and exposes the plugin's
// declared tools as agentcore.AgentTool values that forward invocations over
// JSON-RPC. Crash isolation lives here — a call against a plugin whose process
// has died returns an error result, never a panic.
package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/alex6xu/jarvisserver/internal/agentcore"
	"github.com/alex6xu/jarvisserver/internal/jsonrpc"
)

// initTimeout bounds the initialize handshake so a plugin that never replies
// cannot hang plugin discovery.
const initTimeout = 10 * time.Second

// eventTimeout bounds one fire-and-forget lifecycle-event delivery (US-017,
// #133). It is short so a slow or hung plugin adds only a small, bounded delay
// per event rather than stalling the agent loop; the event is dropped on
// timeout.
const eventTimeout = 2 * time.Second

// Plugin is a running plugin: its JSON-RPC client plus the manifest it declared
// during initialize.
type Plugin struct {
	ID             string
	Path           string
	Manifest       Manifest
	client         *jsonrpc.Client
	callTimeout    time.Duration
	maxOutputBytes int
}

// LoadOptions constrains one plugin process. Env is explicit: nil inherits the
// parent for CLI compatibility; a non-nil slice is used verbatim by os/exec.
type LoadOptions struct {
	ID             string
	Dir            string
	Env            []string
	InitTimeout    time.Duration
	CallTimeout    time.Duration
	MaxOutputBytes int
}

// Load preserves the CLI's legacy environment behavior. Gateway uses
// LoadWithOptions with a cleaned environment and independent call deadline.
func Load(command string, args []string, stderr io.Writer) (*Plugin, error) {
	return LoadWithOptions(command, args, stderr, LoadOptions{})
}

// LoadWithOptions starts and initializes one JSON-RPC plugin process.
func LoadWithOptions(command string, args []string, stderr io.Writer, opts LoadOptions) (*Plugin, error) {
	client, err := jsonrpc.NewClient(jsonrpc.Config{
		Command: command,
		Args:    args,
		Env:     opts.Env,
		Dir:     opts.Dir,
		Stderr:  stderr,
	})
	if err != nil {
		return nil, fmt.Errorf("plugin: launch %q: %w", command, err)
	}

	initializeTimeout := opts.InitTimeout
	if initializeTimeout <= 0 {
		initializeTimeout = initTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), initializeTimeout)
	defer cancel()

	raw, err := client.Call(ctx, "initialize", nil)
	if err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("plugin: initialize %q: %w", command, err)
	}
	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("plugin: decode manifest from %q: %w", command, err)
	}
	if m.Name == "" {
		_ = client.Close()
		return nil, fmt.Errorf("plugin %q: manifest has empty name", command)
	}
	if err := validateManifest(m); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("plugin %q: %w", command, err)
	}
	callTimeout := opts.CallTimeout
	if callTimeout <= 0 {
		callTimeout = 60 * time.Second
	}
	maxOutputBytes := opts.MaxOutputBytes
	if maxOutputBytes <= 0 {
		maxOutputBytes = 1 << 20
	}
	return &Plugin{
		ID: opts.ID, Path: command, Manifest: m, client: client,
		callTimeout: callTimeout, maxOutputBytes: maxOutputBytes,
	}, nil
}

func validateManifest(manifest Manifest) error {
	seenTools := make(map[string]bool, len(manifest.Tools))
	for _, tool := range manifest.Tools {
		name := strings.TrimSpace(tool.Name)
		if name == "" {
			return fmt.Errorf("manifest contains an empty tool name")
		}
		if seenTools[name] {
			return fmt.Errorf("manifest contains duplicate tool %q", name)
		}
		seenTools[name] = true
		if len(tool.Schema) > 0 && !json.Valid(tool.Schema) {
			return fmt.Errorf("tool %q has invalid JSON schema", name)
		}
	}
	seenCommands := make(map[string]bool, len(manifest.Commands))
	for _, command := range manifest.Commands {
		name := strings.TrimSpace(command.Name)
		if name == "" {
			return fmt.Errorf("manifest contains an empty command name")
		}
		if seenCommands[name] {
			return fmt.Errorf("manifest contains duplicate command %q", name)
		}
		seenCommands[name] = true
	}
	return nil
}

// Tools adapts each tool the plugin declared into an agentcore.AgentTool that
// forwards Execute over JSON-RPC.
func (p *Plugin) Tools() []agentcore.AgentTool {
	out := make([]agentcore.AgentTool, 0, len(p.Manifest.Tools))
	for _, spec := range p.Manifest.Tools {
		out = append(out, &pluginTool{plugin: p, spec: spec})
	}
	return out
}

// Close shuts the plugin down: it sends a best-effort shutdown notification then
// closes the transport (which closes stdin and, if needed, kills the child). The
// shutdown notify is bounded by eventTimeout so a plugin that has stopped reading
// its stdin (whose write pipe is full) cannot make Close block on the transport
// write mutex — Close falls through to client.Close, which kills the child.
func (p *Plugin) Close() error {
	done := make(chan struct{})
	go func() { _ = p.client.Notify("shutdown", nil); close(done) }()
	select {
	case <-done:
	case <-time.After(eventTimeout):
	}
	return p.client.Close()
}

// call forwards a tool invocation to the plugin and returns its result.
func (p *Plugin) call(ctx context.Context, name string, args json.RawMessage) (CallResult, error) {
	callCtx, cancel := context.WithTimeout(ctx, p.callTimeout)
	defer cancel()
	raw, err := p.client.Call(callCtx, "tools/call", CallParams{Name: name, Arguments: args})
	if err != nil {
		return CallResult{}, err
	}
	var res CallResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return CallResult{}, fmt.Errorf("plugin %q: decode result for %q: %w", p.Manifest.Name, name, err)
	}
	if len(res.Content) > p.maxOutputBytes {
		originalBytes := len(res.Content)
		res.Content = strings.ToValidUTF8(res.Content[:p.maxOutputBytes], "") +
			fmt.Sprintf("\n[plugin output truncated from %d bytes]", originalBytes)
	}
	return res, nil
}

// CallCommand forwards a slash-command invocation to the plugin over JSON-RPC
// (commands/call) and returns the plugin's result. args carries the command's
// free-form arguments (passed through verbatim). A transport error (e.g. the
// plugin crashed) or a malformed reply is surfaced as a returned error rather
// than a panic — mirroring how pluginTool.Execute isolates a dead plugin, but
// leaving the caller to decide how to present the failure.
func (p *Plugin) CallCommand(ctx context.Context, name string, args json.RawMessage) (CommandCallResult, error) {
	raw, err := p.client.Call(ctx, "commands/call", CommandCallParams{Name: name, Args: args})
	if err != nil {
		return CommandCallResult{}, fmt.Errorf("plugin %q: command %q: %w", p.Manifest.Name, name, err)
	}
	var res CommandCallResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return CommandCallResult{}, fmt.Errorf("plugin %q: decode command result for %q: %w", p.Manifest.Name, name, err)
	}
	return res, nil
}

// Subscribes reports whether the plugin asked to receive the given event type in
// its manifest (US-017, #133). jarvis only delivers subscribed events.
func (p *Plugin) Subscribes(eventType string) bool {
	return slices.Contains(p.Manifest.Events, eventType)
}

// SendEvent delivers one lifecycle event to the plugin as a one-way `event`
// notification (US-017, #133). Delivery is fire-and-forget and bounded by
// eventTimeout: the underlying write runs on its own goroutine so a plugin that
// has stopped reading its stdin (a hung or slow plugin) cannot block the agent
// loop — the send is abandoned when the timeout elapses and its error returned.
// The dropped write goroutine ends on its own when the plugin dies or Close
// tears the pipe down.
func (p *Plugin) SendEvent(params EventParams) error {
	done := make(chan error, 1)
	go func() { done <- p.client.Notify("event", params) }()
	select {
	case err := <-done:
		return err
	case <-time.After(eventTimeout):
		return fmt.Errorf("plugin %q: event %q delivery timed out after %s", p.Manifest.Name, params.Type, eventTimeout)
	}
}

// pluginTool adapts one plugin-declared tool to the agentcore.AgentTool
// interface. All invocations are forwarded to the owning plugin over RPC.
type pluginTool struct {
	plugin *Plugin
	spec   ToolSpec
}

// Name implements AgentTool.
func (t *pluginTool) Name() string { return t.spec.Name }

// Description implements AgentTool.
func (t *pluginTool) Description() string { return t.spec.Description }

// Schema implements AgentTool. An empty schema declared by the plugin degrades
// to a permissive object schema so registration never fails.
func (t *pluginTool) Schema() json.RawMessage {
	if len(t.spec.Schema) == 0 {
		return json.RawMessage(`{"type":"object"}`)
	}
	return t.spec.Schema
}

// ExecutionMode implements AgentTool. Plugin calls cross a process boundary and
// have unknown side effects, so they run sequentially to be safe.
func (t *pluginTool) ExecutionMode() agentcore.ToolExecutionMode {
	return agentcore.ToolExecutionSequential
}

// Execute implements AgentTool by forwarding the call to the plugin process. A
// transport error (e.g. the plugin crashed) is isolated: it degrades to an error
// result so a dead plugin cannot take down the agent loop.
func (t *pluginTool) Execute(ctx context.Context, id string, args json.RawMessage, onUpdate agentcore.ToolUpdateFunc) (agentcore.AgentToolResult, error) {
	res, err := t.plugin.call(ctx, t.spec.Name, args)
	if err != nil {
		return agentcore.AgentToolResult{
			Content: agentcore.ContentList{agentcore.NewTextContent(
				fmt.Sprintf("%s: plugin call failed: %v", t.spec.Name, err))},
		}, nil
	}
	result := agentcore.AgentToolResult{
		Content: agentcore.ContentList{agentcore.NewTextContent(res.Content)},
	}
	if res.IsError {
		result.Details = map[string]any{"isError": true}
	}
	return result, nil
}
