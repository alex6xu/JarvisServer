package pkgmgr

import (
	"path/filepath"
	"testing"
)

// TestHomeHonorsJARVISHOME verifies Home prefers JARVIS_HOME over the default.
func TestHomeHonorsJARVISHOME(t *testing.T) {
	t.Setenv("JARVIS_HOME", "/custom/jarvis")
	if got := Home(); got != "/custom/jarvis" {
		t.Errorf("Home() = %q, want /custom/jarvis", got)
	}
}

// TestTypeDirsUnderHome verifies the plugins/commands/themes dirs nest under
// $JARVIS_HOME.
func TestTypeDirsUnderHome(t *testing.T) {
	t.Setenv("JARVIS_HOME", "/custom/jarvis")
	cases := map[PackageType]string{
		TypeExtension: "/custom/jarvis/plugins",
		TypePrompt:    "/custom/jarvis/commands",
		TypeTheme:     "/custom/jarvis/themes",
	}
	for typ, want := range cases {
		if got := DirForType(typ); got != want {
			t.Errorf("DirForType(%s) = %q, want %q", typ, got, want)
		}
	}
}

// TestSkillsDirHonorsOverride verifies skills use JARVIS_SKILLS_DIR, not $JARVIS_HOME.
func TestSkillsDirHonorsOverride(t *testing.T) {
	t.Setenv("JARVIS_SKILLS_DIR", "/custom/skills")
	if got := SkillsDir(); got != "/custom/skills" {
		t.Errorf("SkillsDir() = %q, want /custom/skills", got)
	}
	if got := DirForType(TypeSkill); got != "/custom/skills" {
		t.Errorf("DirForType(skill) = %q, want /custom/skills", got)
	}
}

// TestSkillsDirDefault verifies skills default to ~/.agents/skills.
func TestSkillsDirDefault(t *testing.T) {
	t.Setenv("JARVIS_SKILLS_DIR", "")
	t.Setenv("HOME", "/home/tester")
	want := filepath.Join("/home/tester", ".agents", "skills")
	if got := SkillsDir(); got != want {
		t.Errorf("SkillsDir() = %q, want %q", got, want)
	}
}

// TestDirForUnknownType verifies an unknown type yields "".
func TestDirForUnknownType(t *testing.T) {
	if got := DirForType(PackageType("bogus")); got != "" {
		t.Errorf("DirForType(bogus) = %q, want empty", got)
	}
}
