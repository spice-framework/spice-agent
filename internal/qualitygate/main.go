// Command qualitygate owns Spice Agent's cross-platform repository checks.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"
)

const (
	requiredGoVersion = "go1.26.5"
	modulePath        = "github.com/spice-framework/spice-agent"
	minimumCoverage   = 85.0
)

func main() {
	os.Exit(execute()) // Entrypoint exception: translate the returned gate status.
}

func execute() int {
	mode := flag.String("mode", "verify", "verification mode: fast, check, coverage, or verify")
	flag.Parse()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	root, err := repositoryRoot()
	if err == nil {
		err = run(ctx, root, *mode)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "quality gate failed: %v\n", err)
		return 1
	}
	return 0
}

type step struct {
	name string
	run  func() error
}

func run(ctx context.Context, root, mode string) error {
	if runtime.Version() != requiredGoVersion {
		return fmt.Errorf("go version is %s; require exactly %s", runtime.Version(), requiredGoVersion)
	}
	identity := step{"repository identity", func() error { return checkIdentity(root) }}
	diffHygiene := step{"diff hygiene", func() error { return command(ctx, root, nil, "git", "diff", "--check", "HEAD", "--") }}
	tests := step{"tests", func() error { return command(ctx, root, nil, "go", "test", "-shuffle=on", "-count=1", "./...") }}
	steps := []step{identity, diffHygiene, tests}
	if mode == "coverage" {
		steps = []step{identity, diffHygiene, {"coverage", func() error { return coverage(ctx, root) }}}
	}
	if mode == "check" || mode == "verify" {
		steps = []step{
			identity,
			diffHygiene,
			{"formatting", func() error { return checkFormatting(ctx, root) }},
			{"module and vendor", func() error { return checkModule(ctx, root) }},
			{"architecture", func() error { return checkArchitecture(root) }},
			{"go vet", func() error { return command(ctx, root, nil, "go", "vet", "./...") }},
			tests,
		}
	}
	if mode == "verify" {
		steps = append(
			steps,
			step{"lint and nil safety", func() error { return lint(ctx, root) }},
			step{"security", func() error { return security(ctx, root) }},
			step{"race tests", func() error {
				return command(ctx, root, nil, "go", "test", "-race", "-shuffle=on", "-count=1", "./...")
			}},
			step{"fuzz smoke", func() error { return fuzz(ctx, root) }},
			step{"coverage", func() error { return coverage(ctx, root) }},
			step{"offline vendor", func() error { return offline(ctx, root) }},
		)
	}
	if mode != "fast" && mode != "check" && mode != "coverage" && mode != "verify" {
		return fmt.Errorf("unknown mode %q", mode)
	}
	for _, current := range steps {
		started := time.Now()
		fmt.Printf("==> %s\n", current.name)
		if err := current.run(); err != nil {
			return fmt.Errorf("%s (%s): %w", current.name, time.Since(started).Round(time.Millisecond), err)
		}
		fmt.Printf("<== %s passed in %s\n", current.name, time.Since(started).Round(time.Millisecond))
	}
	fmt.Println("==> all verification passed")
	return nil
}

func checkIdentity(root string) error {
	content, err := os.ReadFile(filepath.Join(root, "go.mod")) // #nosec G304 -- repository-owned path.
	if err != nil {
		return fmt.Errorf("read go.mod: %w", err)
	}
	text := string(content)
	if !strings.Contains(text, "module "+modulePath+"\n") {
		return fmt.Errorf("go.mod does not declare %s", modulePath)
	}
	if strings.Contains(text, "\nreplace ") || strings.Contains(text, "\nreplace (") {
		return errors.New("committed go.mod must not contain replace directives")
	}
	return nil
}

func checkFormatting(ctx context.Context, root string) error {
	files, err := goFiles(root)
	if err != nil {
		return err
	}
	for _, name := range []string{"goimports", "gofumpt"} {
		executable, pathErr := toolPath(ctx, root, name)
		if pathErr != nil {
			return pathErr
		}
		output, runErr := capture(ctx, root, nil, executable, append([]string{"-l"}, files...)...)
		if runErr != nil {
			return runErr
		}
		if strings.TrimSpace(output) != "" {
			return fmt.Errorf("%s requires formatting: %s", name, strings.Join(strings.Fields(output), ", "))
		}
	}
	return nil
}

func goFiles(root string) ([]string, error) {
	var result []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() && path != root && (entry.Name() == ".git" || entry.Name() == "vendor") {
			return filepath.SkipDir
		}
		if !entry.IsDir() && filepath.Ext(path) == ".go" {
			result = append(result, path)
		}
		return nil
	})
	slices.Sort(result)
	return result, err
}

func checkModule(ctx context.Context, root string) error {
	offline := map[string]string{"GOPROXY": "off", "GOWORK": "off", "GOTOOLCHAIN": "local"}
	if err := command(ctx, root, offline, "go", "mod", "tidy", "-diff"); err != nil {
		return err
	}
	if err := command(ctx, filepath.Join(root, "tools"), offline, "go", "mod", "tidy", "-diff"); err != nil {
		return err
	}
	temporary, err := os.MkdirTemp("", "spice-agent-vendor-*")
	if err != nil {
		return fmt.Errorf("create vendor comparison directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(temporary) }()
	candidate := filepath.Join(temporary, "vendor")
	if err = command(ctx, root, offline, "go", "mod", "vendor", "-o", candidate); err != nil {
		return err
	}
	want, err := treeDigests(candidate)
	if err != nil {
		return err
	}
	got, err := treeDigests(filepath.Join(root, "vendor"))
	if err != nil {
		return err
	}
	if !maps.Equal(got, want) {
		return errors.New("vendor differs from a fresh go mod vendor result")
	}
	return nil
}

func lint(ctx context.Context, root string) error {
	environment := map[string]string{"GOWORK": "off", "GOTOOLCHAIN": "local", "GOFLAGS": "-mod=vendor"}
	golangci, err := toolPath(ctx, root, "golangci-lint")
	if err != nil {
		return err
	}
	if err = command(ctx, root, environment, golangci, "run", "--timeout=10m"); err != nil {
		return err
	}
	nilaway, err := toolPath(ctx, root, "nilaway")
	if err != nil {
		return err
	}
	return command(ctx, root, environment, nilaway, "-include-pkgs="+modulePath, "./...")
}

func security(ctx context.Context, root string) error {
	environment := map[string]string{"GOWORK": "off", "GOTOOLCHAIN": "local", "GOFLAGS": "-mod=vendor", "GOPROXY": "off"}
	gosec, err := toolPath(ctx, root, "gosec")
	if err != nil {
		return err
	}
	if err = command(ctx, root, environment, gosec, "-quiet", "-exclude-generated", "./..."); err != nil {
		return err
	}
	govulncheck, err := toolPath(ctx, root, "govulncheck")
	if err != nil {
		return err
	}
	return command(ctx, root, environment, govulncheck, "./...")
}

func fuzz(ctx context.Context, root string) error {
	for _, target := range []struct{ pkg, name string }{
		{"./message", "FuzzNewID"},
		{"./tool", "FuzzToolCall"},
		{"./agent", "FuzzParseSnapshot"},
	} {
		if err := command(ctx, root, nil, "go", "test", "-run=^$", "-fuzz=^"+target.name+"$", "-fuzztime=1s", target.pkg); err != nil {
			return err
		}
	}
	return nil
}

func toolPath(ctx context.Context, root, name string) (string, error) {
	output, err := capture(ctx, root, map[string]string{"GOWORK": "off", "GOTOOLCHAIN": "local"}, "go", "-C", "tools", "tool", "-n", name)
	if err != nil {
		return "", err
	}
	path := strings.TrimSpace(output)
	if path == "" {
		return "", fmt.Errorf("resolve tool %q: empty path", name)
	}
	return path, nil
}

func treeDigests(root string) (map[string][sha256.Size]byte, error) {
	opened, err := os.OpenRoot(root)
	if err != nil {
		return nil, fmt.Errorf("open tree root: %w", err)
	}
	defer func() { _ = opened.Close() }()
	result := make(map[string][sha256.Size]byte)
	err = fs.WalkDir(opened.FS(), ".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		content, readErr := opened.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		result[filepath.ToSlash(path)] = sha256.Sum256(content)
		return nil
	})
	return result, err
}

func checkArchitecture(root string) error {
	files, err := goFiles(root)
	if err != nil {
		return err
	}
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") || strings.Contains(path, string(filepath.Separator)+"internal"+string(filepath.Separator)) {
			continue
		}
		content, readErr := os.ReadFile(path) // #nosec G304 -- paths come from the bounded repository walk.
		if readErr != nil {
			return readErr
		}
		for _, forbidden := range []string{"type RuntimeGraph", "type ServiceLocator", "reflect.Value", "plugin.Open("} {
			if bytes.Contains(content, []byte(forbidden)) {
				return fmt.Errorf("%s contains forbidden compiled composition mechanism %q", path, forbidden)
			}
		}
	}
	return nil
}

func coverage(ctx context.Context, root string) (returnErr error) {
	profile, err := os.CreateTemp("", "spice-agent-coverage-*.out")
	if err != nil {
		return err
	}
	path := profile.Name()
	if err = profile.Close(); err != nil {
		return err
	}
	defer func() { returnErr = errors.Join(returnErr, os.Remove(path)) }()
	packageOutput, err := capture(ctx, root, nil, "go", "list", "./...")
	if err != nil {
		return err
	}
	packages := make([]string, 0)
	for packagePath := range strings.FieldsSeq(packageOutput) {
		if packagePath != modulePath+"/internal/qualitygate" {
			packages = append(packages, packagePath)
		}
	}
	arguments := append([]string{"test", "-covermode=atomic", "-coverprofile=" + path}, packages...)
	if err = command(ctx, root, nil, "go", arguments...); err != nil {
		return err
	}
	report, err := capture(ctx, root, nil, "go", "tool", "cover", "-func="+path)
	if err != nil {
		return err
	}
	total, err := totalCoverage(report)
	if err != nil {
		return err
	}
	fmt.Printf("repository coverage %.1f%% (minimum %.1f%%)\n", total, minimumCoverage)
	if total < minimumCoverage {
		return fmt.Errorf("coverage %.1f%% is below %.1f%%", total, minimumCoverage)
	}
	return nil
}

func totalCoverage(report string) (float64, error) {
	lines := strings.Split(strings.TrimSpace(report), "\n")
	if len(lines) == 0 {
		return 0, errors.New("coverage report is empty")
	}
	fields := strings.Fields(lines[len(lines)-1])
	if len(fields) == 0 || !strings.HasSuffix(fields[len(fields)-1], "%") {
		return 0, errors.New("coverage report has no total percentage")
	}
	return strconv.ParseFloat(strings.TrimSuffix(fields[len(fields)-1], "%"), 64)
}

func offline(ctx context.Context, root string) error {
	environment := map[string]string{"GOFLAGS": "-mod=vendor", "GOPROXY": "off", "GOWORK": "off", "GOTOOLCHAIN": "local"}
	if err := command(ctx, root, environment, "go", "test", "-count=1", "./..."); err != nil {
		return err
	}
	return command(ctx, root, environment, "go", "build", "-trimpath", "./...")
}

func repositoryRoot() (string, error) {
	current, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		content, readErr := os.ReadFile(filepath.Join(current, "go.mod")) // #nosec G304 -- candidates are bounded ancestors.
		if readErr == nil && bytes.Contains(content, []byte("module "+modulePath)) {
			return current, nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", errors.New("find repository root: go.mod not found")
		}
		current = parent
	}
}

func command(ctx context.Context, directory string, environment map[string]string, executable string, arguments ...string) error {
	_, err := runCommand(ctx, directory, environment, false, executable, arguments...)
	return err
}

func capture(ctx context.Context, directory string, environment map[string]string, executable string, arguments ...string) (string, error) {
	return runCommand(ctx, directory, environment, true, executable, arguments...)
}

func runCommand(ctx context.Context, directory string, environment map[string]string, captureOutput bool, executable string, arguments ...string) (string, error) {
	// #nosec G204,G702 -- commands and arguments are fixed repository-owned values.
	process := exec.CommandContext(ctx, executable, arguments...)
	process.Dir = directory
	process.Env = mergedEnvironment(environment)
	var output bytes.Buffer
	if captureOutput {
		process.Stdout = &output
	} else {
		process.Stdout = os.Stdout
	}
	process.Stderr = os.Stderr
	if err := process.Run(); err != nil {
		return "", fmt.Errorf("%s %s: %w", executable, strings.Join(arguments, " "), err)
	}
	return output.String(), nil
}

func mergedEnvironment(overrides map[string]string) []string {
	values := make(map[string]string, len(overrides))
	for key, value := range overrides {
		values[strings.ToUpper(key)] = value
	}
	result := make([]string, 0, len(os.Environ())+len(values))
	for _, entry := range os.Environ() {
		key, _, found := strings.Cut(entry, "=")
		if found {
			if _, replaced := values[strings.ToUpper(key)]; replaced {
				continue
			}
		}
		result = append(result, entry)
	}
	for key, value := range values {
		result = append(result, key+"="+value)
	}
	slices.Sort(result)
	return result
}
