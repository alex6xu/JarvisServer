package gateway

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/alex6xu/jarvisserver/internal/cli/run"
	"github.com/alex6xu/jarvisserver/internal/pkgmgr"
	"github.com/alex6xu/jarvisserver/internal/plugin"
)

type PluginToolSummary struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type PluginSummary struct {
	ID        string              `json:"id"`
	Name      string              `json:"name,omitempty"`
	Package   string              `json:"package,omitempty"`
	Version   string              `json:"version,omitempty"`
	Enabled   bool                `json:"enabled"`
	Status    string              `json:"status"`
	Tools     []PluginToolSummary `json:"tools"`
	Commands  []string            `json:"commands"`
	Events    []string            `json:"events"`
	LastError string              `json:"last_error,omitempty"`
}

type pluginState struct {
	Disabled map[string]bool `json:"disabled"`
}

// PluginRegistry persists administrator enable/disable choices and creates a
// constrained per-run discovery snapshot. Existing runs keep their own plugin
// processes; changes apply to subsequent runs.
type PluginRegistry struct {
	mu        sync.RWMutex
	opMu      sync.Mutex
	directory string
	statePath string
	enabled   bool
	state     pluginState
	opts      Options
}

func NewPluginRegistry(opts Options) (*PluginRegistry, error) {
	directory := strings.TrimSpace(opts.PluginsDir)
	if directory == "" {
		directory = run.PluginsDir()
	}
	statePath := strings.TrimSpace(opts.PluginStatePath)
	if statePath == "" {
		base := filepath.Dir(directory)
		if directory == "" || base == "." {
			base = filepath.Join(opts.Cwd, ".jarvis")
		}
		statePath = filepath.Join(base, "plugins-state.json")
	}
	registry := &PluginRegistry{
		directory: directory, statePath: statePath,
		enabled: opts.PluginsEnabled == nil || *opts.PluginsEnabled,
		state:   pluginState{Disabled: map[string]bool{}}, opts: opts,
	}
	if raw, err := os.ReadFile(statePath); err == nil {
		if err := json.Unmarshal(raw, &registry.state); err != nil {
			return nil, fmt.Errorf("decode plugin state: %w", err)
		}
		if registry.state.Disabled == nil {
			registry.state.Disabled = map[string]bool{}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read plugin state: %w", err)
	}
	return registry, nil
}

func (r *PluginRegistry) Directory() string { return r.directory }

func (r *PluginRegistry) lockfilePath() string {
	return filepath.Join(filepath.Dir(r.directory), "packages.json")
}

func (r *PluginRegistry) ensurePackageLayout() error {
	configured, err := filepath.Abs(r.directory)
	if err != nil {
		return err
	}
	managed, err := filepath.Abs(pkgmgr.PluginsDir())
	if err != nil || managed == "" || configured != managed {
		return fmt.Errorf("web install requires plugin directory %q to match JARVIS_HOME/plugins %q", configured, managed)
	}
	return nil
}

func (r *PluginRegistry) candidateEnabled(id string) bool {
	return r.enabled && !r.state.Disabled[id]
}

func hasPackageType(types []pkgmgr.PackageType, want pkgmgr.PackageType) bool {
	for _, item := range types {
		if item == want {
			return true
		}
	}
	return false
}

func (r *PluginRegistry) installedExtensions() (map[string]pkgmgr.InstalledPackage, error) {
	packages, err := pkgmgr.ListInstalled(r.lockfilePath())
	if err != nil {
		return nil, err
	}
	out := make(map[string]pkgmgr.InstalledPackage)
	for _, installed := range packages {
		if !hasPackageType(installed.Types, pkgmgr.TypeExtension) {
			continue
		}
		for _, file := range installed.Files {
			if filepath.Dir(file) != r.directory {
				continue
			}
			info, err := os.Lstat(file)
			if err == nil && info.Mode().IsRegular() && info.Mode()&0o111 != 0 {
				out[filepath.Base(file)] = installed
			}
		}
	}
	return out, nil
}

func (r *PluginRegistry) cleanEnvironment(pluginID string) []string {
	allowed := map[string]bool{
		"PATH": true, "HOME": true, "LANG": true, "LC_ALL": true,
		"NODE_PATH": true, "TMPDIR": true, "SYSTEMROOT": true,
	}
	env := make([]string, 0, len(allowed)+1)
	for _, item := range os.Environ() {
		name := item
		if at := strings.IndexByte(item, '='); at >= 0 {
			name = item[:at]
		}
		if allowed[strings.ToUpper(name)] {
			env = append(env, item)
		}
	}
	env = append(env, "JARVIS_PLUGIN_ID="+pluginID)
	return env
}

func (r *PluginRegistry) LoadOptions(cwd string) run.PluginLoadOptions {
	r.mu.RLock()
	defer r.mu.RUnlock()
	candidates, _ := plugin.Candidates(r.directory)
	enabled := make(map[string]bool, len(candidates))
	for _, candidate := range candidates {
		enabled[candidate.ID] = r.candidateEnabled(candidate.ID)
	}
	// A single sanitized environment is sufficient for all enabled launchers;
	// JARVIS_PLUGIN_ID is omitted here because each manager can host many plugins.
	environment := r.cleanEnvironment("")
	return run.PluginLoadOptions{
		Disabled: !r.enabled, Directory: r.directory, Enabled: enabled,
		Environment: environment, InitTimeout: r.opts.PluginInitTimeout,
		CallTimeout: r.opts.PluginCallTimeout, MaxOutputBytes: r.opts.PluginMaxOutputBytes,
	}
}

func (r *PluginRegistry) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(r.statePath), 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(r.state, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(r.statePath), ".plugins-state-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(0o600); err == nil {
		_, err = tmp.Write(raw)
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(name, r.statePath)
}

func (r *PluginRegistry) SetEnabled(id string, enabled bool) error {
	r.opMu.Lock()
	defer r.opMu.Unlock()
	id = strings.TrimSpace(id)
	if id == "" || filepath.Base(id) != id {
		return errors.New("invalid plugin id")
	}
	candidates, err := plugin.Candidates(r.directory)
	if err != nil {
		return err
	}
	found := false
	for _, candidate := range candidates {
		if candidate.ID == id {
			found = true
			break
		}
	}
	if !found {
		return os.ErrNotExist
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if enabled {
		delete(r.state.Disabled, id)
	} else {
		r.state.Disabled[id] = true
	}
	return r.saveLocked()
}

func (r *PluginRegistry) List() ([]PluginSummary, error) {
	candidates, err := plugin.Candidates(r.directory)
	if err != nil {
		return nil, err
	}
	installed, err := r.installedExtensions()
	if err != nil {
		return nil, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]PluginSummary, 0, len(candidates))
	for _, candidate := range candidates {
		enabled := r.candidateEnabled(candidate.ID)
		summary := PluginSummary{ID: candidate.ID, Name: candidate.ID, Enabled: enabled, Status: "disabled", Tools: []PluginToolSummary{}, Commands: []string{}, Events: []string{}}
		if installedPackage, ok := installed[candidate.ID]; ok {
			summary.Package, summary.Version = installedPackage.Name, installedPackage.Version
		}
		if !enabled {
			out = append(out, summary)
			continue
		}
		var warnings bytes.Buffer
		manager, loadErr := plugin.DiscoverWithOptions(r.directory, &warnings, &warnings, plugin.DiscoveryOptions{
			Enabled: map[string]bool{candidate.ID: true}, Dir: r.opts.Cwd,
			Env: r.cleanEnvironment(candidate.ID), InitTimeout: r.opts.PluginInitTimeout,
			CallTimeout: r.opts.PluginCallTimeout, MaxOutputBytes: r.opts.PluginMaxOutputBytes,
		})
		if loadErr != nil || len(manager.Plugins()) != 1 {
			summary.Status = "load_error"
			if loadErr != nil {
				summary.LastError = loadErr.Error()
			} else {
				summary.LastError = strings.TrimSpace(warnings.String())
				if summary.LastError == "" {
					summary.LastError = "plugin did not complete initialization"
				}
			}
			if manager != nil {
				_ = manager.Close()
			}
			out = append(out, summary)
			continue
		}
		loaded := manager.Plugins()[0]
		summary.Name, summary.Status = loaded.Manifest.Name, "ready"
		if loaded.Manifest.Version != "" {
			summary.Version = loaded.Manifest.Version
		}
		for _, tool := range loaded.Manifest.Tools {
			summary.Tools = append(summary.Tools, PluginToolSummary{Name: tool.Name, Description: tool.Description})
		}
		for _, command := range loaded.Manifest.Commands {
			summary.Commands = append(summary.Commands, command.Name)
		}
		summary.Events = append(summary.Events, loaded.Manifest.Events...)
		_ = manager.Close()
		out = append(out, summary)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (r *PluginRegistry) Reload() ([]PluginSummary, error) {
	return r.List()
}

func (r *PluginRegistry) Install(rawRef string) (pkgmgr.InstallResult, []PluginSummary, error) {
	r.opMu.Lock()
	defer r.opMu.Unlock()
	if err := r.ensurePackageLayout(); err != nil {
		return pkgmgr.InstallResult{}, nil, err
	}
	ref, err := pkgmgr.ParsePackageRef(rawRef)
	if err != nil {
		return pkgmgr.InstallResult{}, nil, err
	}
	// Web installs must be reproducible. Require a pinned package version rather
	// than silently trusting whatever npm publishes as latest tomorrow.
	if strings.TrimSpace(ref.Version) == "" {
		return pkgmgr.InstallResult{}, nil, errors.New("web plugin install requires an explicit version, e.g. npm:package@1.2.3")
	}
	if err := pkgmgr.EnsureNPM(); err != nil {
		return pkgmgr.InstallResult{}, nil, err
	}
	installedPackages, err := pkgmgr.ListInstalled(r.lockfilePath())
	if err != nil {
		return pkgmgr.InstallResult{}, nil, err
	}
	for _, installed := range installedPackages {
		if installed.Name == ref.Name {
			return pkgmgr.InstallResult{}, nil, fmt.Errorf("package %q is already installed; uninstall it before installing another version", ref.Name)
		}
	}
	result, err := pkgmgr.InstallWithEnvironment(ref.String(), r.lockfilePath(), nil, r.cleanEnvironment("npm-install"))
	if err != nil {
		return pkgmgr.InstallResult{}, nil, err
	}
	if !hasPackageType(result.Types, pkgmgr.TypeExtension) {
		_ = pkgmgr.Uninstall(result.Name, r.lockfilePath(), nil)
		return pkgmgr.InstallResult{}, nil, errors.New("package does not contain an extension plugin")
	}
	plugins, err := r.List()
	if err != nil {
		return result, nil, err
	}
	for _, item := range plugins {
		if item.Package == result.Name && item.Status == "ready" {
			return result, plugins, nil
		}
	}
	_ = pkgmgr.Uninstall(result.Name, r.lockfilePath(), nil)
	return pkgmgr.InstallResult{}, nil, errors.New("installed package failed plugin initialization and was rolled back")
}

func (r *PluginRegistry) Uninstall(packageName string) ([]PluginSummary, error) {
	r.opMu.Lock()
	defer r.opMu.Unlock()
	if err := r.ensurePackageLayout(); err != nil {
		return nil, err
	}
	packageName = strings.TrimSpace(packageName)
	if packageName == "" {
		return nil, errors.New("package name is required")
	}
	installed, err := pkgmgr.ListInstalled(r.lockfilePath())
	if err != nil {
		return nil, err
	}
	found := false
	for _, item := range installed {
		if item.Name == packageName && hasPackageType(item.Types, pkgmgr.TypeExtension) {
			found = true
			break
		}
	}
	if !found {
		return nil, os.ErrNotExist
	}
	if err := pkgmgr.Uninstall(packageName, r.lockfilePath(), nil); err != nil {
		return nil, err
	}
	// Remove stale disabled entries for launchers that no longer exist.
	candidates, _ := plugin.Candidates(r.directory)
	present := make(map[string]bool, len(candidates))
	for _, item := range candidates {
		present[item.ID] = true
	}
	r.mu.Lock()
	for id := range r.state.Disabled {
		if !present[id] {
			delete(r.state.Disabled, id)
		}
	}
	err = r.saveLocked()
	r.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return r.List()
}

func (r *PluginRegistry) UpdatedAt() string { return time.Now().UTC().Format(time.RFC3339Nano) }
