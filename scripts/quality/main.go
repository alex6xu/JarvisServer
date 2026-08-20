// Command quality runs the repository's backend quality gates consistently in
// local development and CI.
package main

import (
	"bufio"
	"bytes"
	"errors"
	"flag"
	"fmt"
	"go/format"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

var corePackages = []string{
	"./internal/gateway",
	"./internal/router",
	"./internal/runtime",
	"./internal/provider",
}

var packageCoverageMinimums = []struct {
	name    string
	marker  string
	minimum float64
}{
	{name: "gateway", marker: "/internal/gateway/", minimum: 40},
	{name: "router", marker: "/internal/router/", minimum: 55},
	{name: "runtime", marker: "/internal/runtime/", minimum: 80},
	{name: "provider", marker: "/internal/provider/", minimum: 85},
}

func main() {
	mode := flag.String("mode", "core", "test scope: core or full")
	runRace := flag.Bool("race", false, "run the race detector on core packages")
	fixFormatting := flag.Bool("fix-format", false, "format non-baseline Go files before running quality gates")
	coverageOutput := flag.String("coverage-output", "coverage.out", "coverage profile output path")
	coverageMinimum := flag.Float64("coverage-min", 60, "minimum combined core statement coverage")
	flag.Parse()

	root, err := repositoryRoot()
	if err != nil {
		fatal(err)
	}
	if *mode != "core" && *mode != "full" {
		fatal(fmt.Errorf("unknown mode %q", *mode))
	}
	if err := checkFormatting(root, *fixFormatting); err != nil {
		fatal(err)
	}

	packages := corePackages
	if *mode == "full" {
		packages = []string{"./..."}
	}
	if err := run(root, "go", append([]string{"vet"}, packages...)...); err != nil {
		fatal(err)
	}
	testArgs := []string{"test", "-buildvcs=false", "-shuffle=on"}
	testArgs = append(testArgs, packages...)
	if err := run(root, "go", testArgs...); err != nil {
		fatal(err)
	}
	if *runRace {
		if err := requireRaceSupport(root); err != nil {
			fatal(err)
		}
		raceArgs := []string{"test", "-buildvcs=false", "-race", "-shuffle=on"}
		raceArgs = append(raceArgs, corePackages...)
		if err := run(root, "go", raceArgs...); err != nil {
			fatal(err)
		}
	}

	profile := *coverageOutput
	if !filepath.IsAbs(profile) {
		profile = filepath.Join(root, profile)
	}
	coverageArgs := []string{"test", "-buildvcs=false", "-covermode=atomic", "-coverprofile=" + profile}
	coverageArgs = append(coverageArgs, corePackages...)
	if err := run(root, "go", coverageArgs...); err != nil {
		fatal(err)
	}
	coverage, err := statementCoverage(profile)
	if err != nil {
		fatal(err)
	}
	fmt.Printf("\ncore statement coverage: %.1f%% (minimum %.1f%%)\n", coverage, *coverageMinimum)
	if coverage+0.0001 < *coverageMinimum {
		fatal(fmt.Errorf("core statement coverage %.1f%% is below %.1f%%", coverage, *coverageMinimum))
	}
	for _, floor := range packageCoverageMinimums {
		packageCoverage, err := statementCoverageFor(profile, floor.marker)
		if err != nil {
			fatal(err)
		}
		fmt.Printf("%s statement coverage: %.1f%% (minimum %.1f%%)\n", floor.name, packageCoverage, floor.minimum)
		if packageCoverage+0.0001 < floor.minimum {
			fatal(fmt.Errorf("%s statement coverage %.1f%% is below %.1f%%", floor.name, packageCoverage, floor.minimum))
		}
	}
	fmt.Println("quality gates passed")
}

func requireRaceSupport(root string) error {
	cmd := exec.Command("go", "env", "CGO_ENABLED")
	cmd.Dir = root
	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("check race detector support: %w", err)
	}
	if strings.TrimSpace(string(output)) != "1" {
		return errors.New("race detector requires CGO_ENABLED=1; rerun without -race or use the Linux CI gate")
	}
	return nil
}

func repositoryRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("go.mod not found in current directory or parents")
		}
		dir = parent
	}
}

func checkFormatting(root string, fix bool) error {
	baselinePath := filepath.Join(root, "scripts", "quality", "gofmt-baseline.txt")
	baseline, err := readLineSet(baselinePath)
	if err != nil {
		return err
	}
	var files []string
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() && path != root {
			switch entry.Name() {
			case ".git", ".jarvis", "node_modules", "vendor", "dist", "bin":
				return filepath.SkipDir
			}
		}
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".go") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return err
	}
	unformatted := make(map[string]bool)
	for _, path := range files {
		source, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		formatted, err := format.Source(source)
		if err != nil {
			return fmt.Errorf("format %s: %w", path, err)
		}
		normalizedSource := bytes.ReplaceAll(source, []byte("\r\n"), []byte("\n"))
		if !bytes.Equal(normalizedSource, formatted) {
			relative, relErr := filepath.Rel(root, path)
			if relErr == nil {
				path = relative
			}
			unformatted[filepath.ToSlash(path)] = true
		}
	}
	var unexpected, fixed, stale []string
	for path := range unformatted {
		if baseline[path] {
			continue
		}
		if fix {
			sourcePath := filepath.Join(root, filepath.FromSlash(path))
			source, readErr := os.ReadFile(sourcePath)
			if readErr != nil {
				return readErr
			}
			formatted, formatErr := format.Source(source)
			if formatErr != nil {
				return fmt.Errorf("format %s: %w", sourcePath, formatErr)
			}
			if writeErr := os.WriteFile(sourcePath, formatted, 0o644); writeErr != nil {
				return fmt.Errorf("write formatted %s: %w", sourcePath, writeErr)
			}
			fixed = append(fixed, path)
			continue
		}
		unexpected = append(unexpected, path)
	}
	for path := range baseline {
		if !unformatted[path] {
			stale = append(stale, path)
		}
	}
	sort.Strings(unexpected)
	sort.Strings(fixed)
	sort.Strings(stale)
	if len(unexpected) > 0 {
		return fmt.Errorf("gofmt required for:\n  %s\nrerun with -fix-format to format these files using the repository Go toolchain", strings.Join(unexpected, "\n  "))
	}
	if len(stale) > 0 {
		return fmt.Errorf("remove formatted files from %s:\n  %s", baselinePath, strings.Join(stale, "\n  "))
	}
	if len(fixed) > 0 {
		fmt.Printf("formatted %d Go file(s):\n  %s\n", len(fixed), strings.Join(fixed, "\n  "))
	}
	fmt.Printf("format check passed (%d historical files tracked in baseline)\n", len(baseline))
	return nil
}

func readLineSet(path string) (map[string]bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	lines := make(map[string]bool)
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" && !strings.HasPrefix(line, "#") {
			lines[filepath.ToSlash(strings.ReplaceAll(line, "\\", "/"))] = true
		}
	}
	return lines, scanner.Err()
}

func statementCoverage(path string) (float64, error) {
	return statementCoverageFor(path, "")
}

func statementCoverageFor(path, marker string) (float64, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer file.Close()
	var total, covered int64
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 3 || fields[0] == "mode:" {
			continue
		}
		profilePath := "/" + strings.TrimPrefix(filepath.ToSlash(fields[0]), "/")
		if marker != "" && !strings.Contains(profilePath, marker) {
			continue
		}
		statements, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			return 0, fmt.Errorf("parse coverage statements: %w", err)
		}
		count, err := strconv.ParseInt(fields[2], 10, 64)
		if err != nil {
			return 0, fmt.Errorf("parse coverage count: %w", err)
		}
		total += statements
		if count > 0 {
			covered += statements
		}
	}
	if err := scanner.Err(); err != nil {
		return 0, err
	}
	if total == 0 {
		return 0, fmt.Errorf("coverage profile contains no statements for marker %q", marker)
	}
	return float64(covered) * 100 / float64(total), nil
}

func run(root, name string, args ...string) error {
	fmt.Printf("\n> %s %s\n", name, strings.Join(args, " "))
	cmd := exec.Command(name, args...)
	cmd.Dir = root
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s failed: %w", name, err)
	}
	return nil
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "quality:", err)
	os.Exit(1)
}
