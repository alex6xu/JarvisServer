package main

import (
	"bytes"
	"go/format"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStatementCoverage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "coverage.out")
	profile := "mode: atomic\nmodule/internal/gateway/example.go:1.1,2.1 3 1\nmodule/internal/gateway/example.go:3.1,4.1 1 0\nmodule/internal/router/example.go:1.1,2.1 2 1\n"
	if err := os.WriteFile(path, []byte(profile), 0o600); err != nil {
		t.Fatal(err)
	}
	coverage, err := statementCoverage(path)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(coverage-83.3333) > 0.001 {
		t.Fatalf("coverage = %.3f, want 83.333", coverage)
	}
	gatewayCoverage, err := statementCoverageFor(path, "/internal/gateway/")
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(gatewayCoverage-75) > 0.001 {
		t.Fatalf("gateway coverage = %.3f, want 75", gatewayCoverage)
	}
}

func TestCheckFormattingSuggestsRepositoryFormatter(t *testing.T) {
	root := formattingFixture(t, "package example\n\nfunc example( ){ }\n")
	err := checkFormatting(root, false)
	if err == nil {
		t.Fatal("checkFormatting() error = nil, want unformatted-file error")
	}
	if !strings.Contains(err.Error(), "-fix-format") {
		t.Fatalf("checkFormatting() error = %q, want -fix-format hint", err)
	}
}

func TestCheckFormattingFixesWithRepositoryToolchain(t *testing.T) {
	const source = "package example\n\nfunc example( ){ }\n"
	root := formattingFixture(t, source)
	if err := checkFormatting(root, true); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "example.go")
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want, err := format.Source([]byte(source))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("formatted source = %q, want %q", got, want)
	}
}

func formattingFixture(t *testing.T, source string) string {
	t.Helper()
	root := t.TempDir()
	qualityDir := filepath.Join(root, "scripts", "quality")
	if err := os.MkdirAll(qualityDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(qualityDir, "gofmt-baseline.txt"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "example.go"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestReadLineSetIgnoresCommentsAndNormalizesPaths(t *testing.T) {
	path := filepath.Join(t.TempDir(), "baseline.txt")
	if err := os.WriteFile(path, []byte("# comment\ninternal\\one.go\n\ninternal/two.go\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	lines, err := readLineSet(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 2 || !lines["internal/one.go"] || !lines["internal/two.go"] {
		t.Fatalf("line set = %#v", lines)
	}
}
