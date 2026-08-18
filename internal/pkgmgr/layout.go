// This file defines jarvis's install directory layout (#154): where each pi
// package type is placed so jarvis's existing discovery mechanisms load it with
// no extra configuration. The paths intentionally match the conventions already
// used elsewhere in cmd/jarvis and internal/*:
//
//   - extensions → $JARVIS_HOME/plugins   (internal/plugin.Discover)
//   - prompts    → $JARVIS_HOME/commands  (runtime.LoadUserCommandsDir)
//   - themes     → $JARVIS_HOME/themes    (no runtime consumer yet)
//   - skills     → skills dir           (~/.agents/skills, JARVIS_SKILLS_DIR override)
//
// Skills are the one exception to the $JARVIS_HOME root: jarvis loads skills from
// ~/.agents/skills (overridable with JARVIS_SKILLS_DIR), so SkillsDir honors that
// rather than nesting under $JARVIS_HOME.
package pkgmgr

import (
	"os"
	"path/filepath"
)

// Home returns the jarvis home directory: $JARVIS_HOME, or ~/.jarvis when unset. It
// returns "" when the home directory cannot be resolved and no override is set,
// matching trust.DefaultPath's "unavailable" contract.
func Home() string {
	if dir := os.Getenv("JARVIS_HOME"); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".jarvis")
}

// PluginsDir returns $JARVIS_HOME/plugins, where installed extensions (including
// MCP adapters) are laid down for internal/plugin.Discover. It returns "" when
// Home is unavailable.
func PluginsDir() string {
	h := Home()
	if h == "" {
		return ""
	}
	return filepath.Join(h, "plugins")
}

// CommandsDir returns $JARVIS_HOME/commands, the legacy location for installed
// prompt/command templates (still loaded by runtime.LoadUserCommandsDir for
// back-compat). New installs go to PromptsDir. It returns "" when Home is
// unavailable.
func CommandsDir() string {
	h := Home()
	if h == "" {
		return ""
	}
	return filepath.Join(h, "commands")
}

// PromptsDir returns $JARVIS_HOME/prompts, the pi-aligned location where installed
// prompt templates are laid down for runtime.LoadUserCommandsDir (which loads
// both prompts/ and the legacy commands/). It returns "" when Home is
// unavailable.
func PromptsDir() string {
	h := Home()
	if h == "" {
		return ""
	}
	return filepath.Join(h, "prompts")
}

// ThemesDir returns $JARVIS_HOME/themes, where installed themes are stored. jarvis
// has no theme runtime yet, so this is a holding location for a future consumer.
// It returns "" when Home is unavailable.
func ThemesDir() string {
	h := Home()
	if h == "" {
		return ""
	}
	return filepath.Join(h, "themes")
}

// SkillsDir returns the directory installed skills are placed in: JARVIS_SKILLS_DIR
// when set, else ~/.agents/skills — matching cmd/jarvis's skill loader. It returns
// "" when the home directory cannot be resolved and no override is set.
func SkillsDir() string {
	if dir := os.Getenv("JARVIS_SKILLS_DIR"); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".agents", "skills")
}

// DirForType returns the install directory for a package type, or "" when the
// type is unknown or the underlying home directory is unavailable.
func DirForType(t PackageType) string {
	switch t {
	case TypeExtension:
		return PluginsDir()
	case TypePrompt:
		return CommandsDir()
	case TypeTheme:
		return ThemesDir()
	case TypeSkill:
		return SkillsDir()
	default:
		return ""
	}
}
