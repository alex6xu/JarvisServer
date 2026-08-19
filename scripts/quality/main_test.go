package main

import (
	"math"
	"os"
	"path/filepath"
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
