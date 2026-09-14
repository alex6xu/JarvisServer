package gateway

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeGatewayTestPlugin(t *testing.T, dir, name, manifest string) {
	t.Helper()
	path := filepath.Join(dir, name)
	script := `#!/usr/bin/env python3
import json, sys
manifest = ` + manifest + `
for line in sys.stdin:
    req = json.loads(line)
    if req.get("method") == "initialize":
        print(json.dumps({"jsonrpc":"2.0","id":req.get("id"),"result":manifest}), flush=True)
    elif req.get("method") == "tools/call":
        print(json.dumps({"jsonrpc":"2.0","id":req.get("id"),"result":{"content":"ok"}}), flush=True)
    elif req.get("method") == "shutdown":
        break
`
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
}

func TestPluginRegistryListsAndPersistsDisabledState(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "plugins")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeGatewayTestPlugin(t, dir, "demo", `{"name":"demo","version":"1","tools":[{"name":"demo_tool","description":"demo","schema":{"type":"object"}}]}`)
	enabled := true
	opts := Options{Cwd: root, PluginsEnabled: &enabled, PluginsDir: dir, PluginStatePath: filepath.Join(root, "state.json"), PluginInitTimeout: 3 * time.Second, PluginCallTimeout: time.Second, PluginMaxOutputBytes: 1024}.withDefaults()
	registry, err := NewPluginRegistry(opts)
	if err != nil {
		t.Fatal(err)
	}
	plugins, err := registry.List()
	if err != nil || len(plugins) != 1 || plugins[0].Status != "ready" || plugins[0].Tools[0].Name != "demo_tool" {
		t.Fatalf("plugins=%+v err=%v", plugins, err)
	}
	if err := registry.SetEnabled("demo", false); err != nil {
		t.Fatal(err)
	}
	plugins, err = registry.List()
	if err != nil || plugins[0].Enabled || plugins[0].Status != "disabled" {
		t.Fatalf("disabled plugins=%+v err=%v", plugins, err)
	}
	reloaded, err := NewPluginRegistry(opts)
	if err != nil {
		t.Fatal(err)
	}
	plugins, _ = reloaded.List()
	if plugins[0].Enabled {
		t.Fatal("disabled state did not persist")
	}
	raw, err := os.ReadFile(opts.PluginStatePath)
	if err != nil || !json.Valid(raw) {
		t.Fatalf("state=%q err=%v", raw, err)
	}
}

func TestPluginRegistryWebInstallRequiresPinnedVersion(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	t.Setenv("JARVIS_HOME", home)
	enabled := true
	registry, err := NewPluginRegistry(Options{
		Cwd: root, PluginsEnabled: &enabled, PluginsDir: filepath.Join(home, "plugins"),
		PluginStatePath: filepath.Join(home, "plugins-state.json"),
	}.withDefaults())
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := registry.Install("npm:demo-plugin"); err == nil || !strings.Contains(err.Error(), "explicit version") {
		t.Fatalf("unpinned install error=%v", err)
	}
	if _, _, err := registry.Install("file:./plugin"); err == nil || !strings.Contains(err.Error(), "unsupported package source") {
		t.Fatalf("unsupported install error=%v", err)
	}
}

func TestPluginRegistryDoesNotExposeGatewaySecrets(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "plugins")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeGatewayTestPlugin(t, dir, "env", `{"name":"env","tools":[{"name":"env_tool","schema":{"type":"object"}}]}`)
	t.Setenv("OPENAI_API_KEY", "secret-value")
	enabled := true
	registry, err := NewPluginRegistry(Options{Cwd: root, PluginsEnabled: &enabled, PluginsDir: dir}.withDefaults())
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range registry.cleanEnvironment("env") {
		if strings.HasPrefix(item, "OPENAI_API_KEY=") || strings.Contains(item, "secret-value") {
			t.Fatalf("secret leaked to plugin environment: %q", item)
		}
	}
}
