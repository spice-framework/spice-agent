// Command qualitygate owns Spice Agent's cross-platform repository checks.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
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
	requiredGoVersion         = "go1.26.5"
	modulePath                = "github.com/spice-framework/spice-agent"
	minimumCoverage           = 85.0
	minimumExperimentCoverage = 85.0
	releaseWorkflowCommit     = "a8f9cc6ffd3a2744c5cae3b52c05e6e91cbc875e"
	verifyWorkflowCommit      = "0534fe1247f892b287f624b7abb6f2347765ab22"
	standardGateTimeout       = 15 * time.Minute
	verifyGateTimeout         = 30 * time.Minute
)

func main() {
	os.Exit(execute()) // Entrypoint exception: translate the returned gate status.
}

func execute() int {
	mode := flag.String("mode", "verify", "verification mode: tools-bootstrap, proto, fast, check, coverage, benchmark, or verify")
	flag.Parse()
	ctx, cancel := context.WithTimeout(context.Background(), gateTimeout(*mode))
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

func gateTimeout(mode string) time.Duration {
	if mode == "verify" {
		return verifyGateTimeout
	}
	return standardGateTimeout
}

type step struct {
	name string
	run  func() error
}

func run(ctx context.Context, root, mode string) error {
	if runtime.Version() != requiredGoVersion {
		return fmt.Errorf("go version is %s; require exactly %s", runtime.Version(), requiredGoVersion)
	}
	if networkAllowed(mode) {
		return bootstrapDependencies(ctx, root, networkCommand)
	}
	if mode == "proto" {
		return generateProtocol(ctx, root, root)
	}
	productEnvironment := map[string]string{
		"GOFLAGS":     "-mod=vendor",
		"GOPROXY":     "off",
		"GOTOOLCHAIN": "local",
		"GOWORK":      "off",
	}
	identity := step{"repository identity", func() error {
		if err := checkIdentity(root); err != nil {
			return err
		}
		return checkReleaseMetadata(root)
	}}
	diffHygiene := step{"diff hygiene", func() error {
		// Nested vendor is byte-reproduced below and may contain upstream
		// whitespace that this repository must not hand-edit.
		return command(ctx, root, nil, "git", "diff", "--check", "HEAD", "--", ".", ":(exclude)experiments/*/vendor/**")
	}}
	tests := step{"tests", func() error {
		return command(ctx, root, productEnvironment, "go", "test", "-shuffle=on", "-count=1", "./...")
	}}
	permissionExperiment := step{"permission experiment", func() error {
		return verifyPermissionExperiment(ctx, root, mode)
	}}
	sqliteRecoveryExperiment := step{"SQLite recovery experiment", func() error {
		return verifySQLiteRecoveryExperiment(ctx, root, mode)
	}}
	twoWorkerExperiment := step{"two-worker experiment", func() error {
		return verifyTwoWorkerExperiment(ctx, root, mode)
	}}
	compactionExperiment := step{"compaction experiment", func() error {
		return verifyCompactionExperiment(ctx, root, mode)
	}}
	gitWorkflowExperiment := step{"Git workflow experiment", func() error {
		return verifyGitWorkflowExperiment(ctx, root, mode)
	}}
	acceptanceScope := step{"acceptance endpoint scope", func() error {
		return command(
			ctx, root, productEnvironment,
			"go", "test", "-tags=spice_acceptance", "-shuffle=on", "-count=1", "./daemon/endpoint",
		)
	}}
	steps := []step{
		identity, diffHygiene, tests, permissionExperiment, sqliteRecoveryExperiment,
		twoWorkerExperiment, compactionExperiment, gitWorkflowExperiment,
	}
	if mode == "coverage" {
		steps = []step{identity, diffHygiene, {"coverage", func() error {
			return coverage(ctx, root, productEnvironment)
		}}}
	}
	if mode == "benchmark" {
		steps = []step{identity, diffHygiene, {"kernel runtime benchmarks", func() error {
			return kernelRuntimeBenchmarks(ctx, root, productEnvironment)
		}}}
	}
	if mode == "check" || mode == "verify" {
		steps = []step{
			identity,
			diffHygiene,
			{"formatting", func() error { return checkFormatting(ctx, root) }},
			{"module and vendor", func() error { return checkModule(ctx, root) }},
			{"protobuf", func() error { return checkProtocol(ctx, root) }},
			{"architecture", func() error { return checkArchitecture(root) }},
			{"go vet", func() error { return command(ctx, root, productEnvironment, "go", "vet", "./...") }},
			tests,
			permissionExperiment,
			sqliteRecoveryExperiment,
			twoWorkerExperiment,
			compactionExperiment,
			gitWorkflowExperiment,
			acceptanceScope,
		}
	}
	if mode == "verify" {
		steps = append(
			steps,
			step{"lint and nil safety", func() error { return lint(ctx, root) }},
			step{"security", func() error { return security(ctx, root) }},
			step{"race tests", func() error {
				return command(ctx, root, productEnvironment, "go", "test", "-race", "-shuffle=on", "-count=1", "./...")
			}},
			step{"acceptance endpoint scope race", func() error {
				return command(
					ctx, root, productEnvironment,
					"go", "test", "-race", "-tags=spice_acceptance", "-shuffle=on", "-count=1", "./daemon/endpoint",
				)
			}},
			step{"fuzz smoke", func() error { return fuzz(ctx, root, productEnvironment) }},
			step{"coverage", func() error { return coverage(ctx, root, productEnvironment) }},
			step{"offline vendor", func() error { return offline(ctx, root) }},
		)
	}
	if mode != "fast" && mode != "check" && mode != "coverage" && mode != "benchmark" && mode != "verify" {
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

func verifyPermissionExperiment(ctx context.Context, root, mode string) error {
	return verifyNestedExperiment(ctx, root, mode, "permission", "PermissionProof")
}

func verifySQLiteRecoveryExperiment(ctx context.Context, root, mode string) error {
	return verifyNestedExperiment(ctx, root, mode, "sqlite-recovery", "SQLiteRecoveryProof")
}

func verifyTwoWorkerExperiment(ctx context.Context, root, mode string) error {
	return verifyNestedExperiment(ctx, root, mode, "two-worker", "TwoWorkerProof")
}

func verifyCompactionExperiment(ctx context.Context, root, mode string) error {
	if err := verifyNestedExperiment(
		ctx, root, mode, "compaction", "CompactionProof",
	); err != nil {
		return err
	}
	if mode != "verify" {
		return nil
	}
	environment := map[string]string{
		"GOFLAGS": "-mod=vendor", "GOPROXY": "off", "GOTOOLCHAIN": "local", "GOWORK": "off",
	}
	return command(
		ctx, filepath.Join(root, "experiments", "compaction"), environment,
		"go", compactionFuzzArguments()...,
	)
}

func compactionFuzzArguments() []string {
	return []string{"test", "-run=^$", "-fuzz=^FuzzCompact$", "-fuzztime=100x", "."}
}

func verifyGitWorkflowExperiment(ctx context.Context, root, mode string) error {
	if err := verifyNestedExperiment(
		ctx, root, mode, "git-workflow", "GitWorkflowProof",
	); err != nil {
		return err
	}
	if mode != "verify" {
		return nil
	}
	environment := map[string]string{
		"GOFLAGS": "-mod=vendor", "GOPROXY": "off", "GOTOOLCHAIN": "local", "GOWORK": "off",
	}
	return command(
		ctx, filepath.Join(root, "experiments", "git-workflow"), environment,
		"go", gitWorkflowFuzzArguments()...,
	)
}

func gitWorkflowFuzzArguments() []string {
	return []string{"test", "-run=^$", "-fuzz=^FuzzDecodeCommitArguments$", "-fuzztime=100x", "."}
}

func verifyNestedExperiment(ctx context.Context, root, mode, name, target string) error {
	directory := filepath.Join(root, "experiments", name)
	if _, err := os.Stat(filepath.Join(directory, "go.mod")); err != nil {
		return fmt.Errorf("%s experiment module: %w", name, err)
	}
	environment := map[string]string{
		"GOFLAGS":     "-mod=vendor",
		"GOPROXY":     "off",
		"GOTOOLCHAIN": "local",
		"GOWORK":      "off",
	}
	if mode == "check" || mode == "verify" {
		if err := command(ctx, directory, environment, "go", "mod", "tidy", "-diff"); err != nil {
			return err
		}
		temporary, err := os.MkdirTemp("", "spice-agent-"+name+"-vendor-*")
		if err != nil {
			return fmt.Errorf("create %s vendor comparison directory: %w", name, err)
		}
		defer func() { _ = os.RemoveAll(temporary) }()
		candidate := filepath.Join(temporary, "vendor")
		if err = command(ctx, directory, environment, "go", "mod", "vendor", "-o", candidate); err != nil {
			return err
		}
		want, err := treeDigests(candidate)
		if err != nil {
			return err
		}
		got, err := treeDigests(filepath.Join(directory, "vendor"))
		if err != nil {
			return err
		}
		if !maps.Equal(got, want) {
			return fmt.Errorf("%s experiment vendor differs from a fresh go mod vendor result", name)
		}
		if err = command(
			ctx, directory, environment, "go", "tool",
			"github.com/spice-framework/toolchain/cmd/spice", "generate", "--check",
			"--target", target, ".", "./internal/composition",
		); err != nil {
			return err
		}
		if err = command(ctx, directory, environment, "go", "vet", "./..."); err != nil {
			return err
		}
	}
	if err := command(ctx, directory, environment, "go", "test", "-shuffle=on", "-count=1", "./..."); err != nil {
		return err
	}
	if mode == "verify" {
		if err := command(ctx, directory, environment, "go", "test", "-race", "-shuffle=on", "-count=1", "./..."); err != nil {
			return err
		}
		output, err := capture(ctx, directory, environment, "go", "test", "-cover", ".")
		if err != nil {
			return err
		}
		return validateExperimentCoverage(name, output)
	}
	return nil
}

func validatePermissionCoverage(output string) error {
	return validateExperimentCoverage("permission", output)
}

func validateSQLiteRecoveryCoverage(output string) error {
	return validateExperimentCoverage("SQLite recovery", output)
}

func validateTwoWorkerCoverage(output string) error {
	return validateExperimentCoverage("two-worker", output)
}

func validateCompactionCoverage(output string) error {
	return validateExperimentCoverage("compaction", output)
}

func validateGitWorkflowCoverage(output string) error {
	return validateExperimentCoverage("Git workflow", output)
}

func validateExperimentCoverage(name, output string) error {
	const marker = "coverage: "
	for line := range strings.Lines(output) {
		_, value, found := strings.Cut(line, marker)
		if !found {
			continue
		}
		percent, _, found := strings.Cut(value, "%")
		if !found {
			break
		}
		coverage, err := strconv.ParseFloat(strings.TrimSpace(percent), 64)
		if err != nil {
			return fmt.Errorf("parse %s experiment coverage: %w", name, err)
		}
		if coverage < minimumExperimentCoverage {
			return fmt.Errorf(
				"%s experiment coverage %.1f%% is below %.1f%%",
				name, coverage, minimumExperimentCoverage,
			)
		}
		return nil
	}
	return fmt.Errorf("%s experiment coverage output is missing", name)
}

func networkAllowed(mode string) bool { return mode == "tools-bootstrap" }

func kernelRuntimeBenchmarkArguments() []string {
	return []string{
		"test", "-run=^$", "-bench=^BenchmarkKernel", "-benchmem",
		"-benchtime=500x", "-count=5", "-cpu=1", "./agent",
	}
}

func kernelRuntimeBenchmarks(
	ctx context.Context,
	root string,
	environment map[string]string,
) error {
	output, err := capture(ctx, root, environment, "go", kernelRuntimeBenchmarkArguments()...)
	if err != nil {
		return err
	}
	if err = validateKernelRuntimeBenchmarkOutput(output); err != nil {
		return err
	}
	fmt.Print(output)
	return nil
}

func validateKernelRuntimeBenchmarkOutput(output string) error {
	required := [...]string{
		"BenchmarkKernelEngineConstruction",
		"BenchmarkKernelTextRun",
		"BenchmarkKernelToolRound",
		"BenchmarkKernelCancellation",
	}
	counts := make(map[string]int, len(required))
	for line := range strings.Lines(output) {
		line = strings.TrimSpace(line)
		for _, name := range required {
			if benchmarkOutputLineMatches(line, name) {
				counts[name]++
			}
		}
	}
	for _, name := range required {
		if counts[name] != 5 {
			return fmt.Errorf("kernel runtime benchmark %s produced %d samples; require 5", name, counts[name])
		}
	}
	return nil
}

func benchmarkOutputLineMatches(line, name string) bool {
	if !strings.HasPrefix(line, name) || len(line) == len(name) {
		return false
	}
	switch line[len(name)] {
	case '-', ' ', '\t':
		return true
	default:
		return false
	}
}

type bootstrapRunner func(context.Context, string, ...string) error

type moduleGraph struct {
	directory string
	optional  bool
}

func bootstrapDependencies(
	ctx context.Context,
	root string,
	runner bootstrapRunner,
) (returnErr error) {
	before, err := sourceTreeDigests(root)
	if err != nil {
		return fmt.Errorf("snapshot repository before bootstrap: %w", err)
	}
	defer func() {
		after, snapshotErr := sourceTreeDigests(root)
		if snapshotErr != nil {
			returnErr = errors.Join(
				returnErr,
				fmt.Errorf("snapshot repository after bootstrap: %w", snapshotErr),
			)
			return
		}
		if !maps.Equal(before, after) {
			returnErr = errors.Join(
				returnErr,
				errors.New("dependency bootstrap modified the repository"),
			)
		}
	}()

	graphs := []moduleGraph{
		{directory: root},
		{directory: filepath.Join(root, "tools"), optional: true},
		{directory: filepath.Join(root, "experiments", "permission"), optional: true},
		{directory: filepath.Join(root, "experiments", "sqlite-recovery"), optional: true},
		{directory: filepath.Join(root, "experiments", "two-worker"), optional: true},
		{directory: filepath.Join(root, "experiments", "compaction"), optional: true},
		{directory: filepath.Join(root, "experiments", "git-workflow"), optional: true},
	}
	for _, graph := range graphs {
		if err := bootstrapModuleGraph(ctx, graph, runner); err != nil {
			return err
		}
	}
	return nil
}

func bootstrapModuleGraph(
	ctx context.Context,
	graph moduleGraph,
	runner bootstrapRunner,
) (returnErr error) {
	moduleFile := filepath.Join(graph.directory, "go.mod")
	moduleContent, err := os.ReadFile(moduleFile) // #nosec G304 -- repository-owned module graph.
	if errors.Is(err, os.ErrNotExist) && graph.optional {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read %s: %w", moduleFile, err)
	}
	temporary, err := os.MkdirTemp("", "spice-agent-tools-bootstrap-*")
	if err != nil {
		return fmt.Errorf("create dependency bootstrap directory: %w", err)
	}
	defer func() { returnErr = errors.Join(returnErr, os.RemoveAll(temporary)) }()
	temporaryRoot, err := os.OpenRoot(temporary)
	if err != nil {
		return fmt.Errorf("open dependency bootstrap directory: %w", err)
	}
	defer func() { returnErr = errors.Join(returnErr, temporaryRoot.Close()) }()

	temporaryModule := filepath.Join(temporary, "graph.mod")
	if writeErr := temporaryRoot.WriteFile("graph.mod", moduleContent, 0o600); writeErr != nil {
		return fmt.Errorf("write temporary module file: %w", writeErr)
	}
	sumFile := filepath.Join(graph.directory, "go.sum")
	sumContent, err := os.ReadFile(sumFile) // #nosec G304 -- repository-owned module graph.
	if err == nil {
		if writeErr := temporaryRoot.WriteFile("graph.sum", sumContent, 0o600); writeErr != nil {
			return fmt.Errorf("write temporary checksum file: %w", writeErr)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read %s: %w", sumFile, err)
	}
	return runner(ctx, graph.directory, bootstrapDownloadArguments(temporaryModule)...)
}

func bootstrapDownloadArguments(moduleFile string) []string {
	return []string{"mod", "download", "-modfile=" + moduleFile, "all"}
}

func checkIdentity(root string) error {
	content, err := os.ReadFile(filepath.Join(root, "go.mod")) // #nosec G304 -- repository-owned path.
	if err != nil {
		return fmt.Errorf("read go.mod: %w", err)
	}
	text := strings.ReplaceAll(string(content), "\r\n", "\n")
	if !strings.Contains(text, "module "+modulePath+"\n") {
		return fmt.Errorf("go.mod does not declare %s", modulePath)
	}
	if strings.Contains(text, "\nreplace ") || strings.Contains(text, "\nreplace (") {
		return errors.New("committed go.mod must not contain replace directives")
	}
	if err := checkRepositoryPortability(root); err != nil {
		return err
	}
	if err := checkEngineProtocolCompatibility(root); err != nil {
		return err
	}
	return checkReleaseWorkflow(root)
}

func checkReleaseWorkflow(root string) error {
	content, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "release.yml")) // #nosec G304 -- repository-owned path.
	if err != nil {
		return fmt.Errorf("read release workflow: %w", err)
	}
	text := strings.ReplaceAll(string(content), "\r\n", "\n")
	for _, required := range []string{
		"permissions: {}",
		"contents: write",
		"id-token: write",
		"attestations: write",
		"artifact-metadata: write",
		"uses: spice-framework/.github/.github/workflows/go-module-release.yml@" + releaseWorkflowCommit,
		"module: " + modulePath,
		"workflow_commit: " + releaseWorkflowCommit,
	} {
		if strings.Count(text, required) != 1 {
			return fmt.Errorf("release workflow must contain exactly one %q", required)
		}
	}
	for _, forbidden := range []string{
		"library-release.yml",
		"secrets:",
		"secrets: inherit",
		"SPICE_LIBRARY_RELEASE_SIGNING_KEY",
		"steps:",
	} {
		if strings.Contains(text, forbidden) {
			return fmt.Errorf("release workflow contains forbidden %q", forbidden)
		}
	}
	if err := checkReleasePermissionCeiling(text); err != nil {
		return err
	}
	return checkSingleReleaseJob(text)
}

func checkReleasePermissionCeiling(workflow string) error {
	lines := strings.Split(workflow, "\n")
	permissionStart := -1
	permissionBlocks := 0
	for index, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "permissions:") {
			permissionBlocks++
		}
		if line == "    permissions:" {
			if permissionStart >= 0 {
				return errors.New("release workflow must contain one job permission block")
			}
			permissionStart = index + 1
		}
	}
	if permissionBlocks != 2 {
		return fmt.Errorf("release workflow must contain exactly two permission blocks, got %d", permissionBlocks)
	}
	if permissionStart < 0 {
		return errors.New("release workflow is missing the job permission block")
	}
	want := map[string]bool{
		"      contents: write":          false,
		"      id-token: write":          false,
		"      attestations: write":      false,
		"      artifact-metadata: write": false,
	}
	for _, line := range lines[permissionStart:] {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if !strings.HasPrefix(line, "      ") {
			break
		}
		if _, allowed := want[line]; !allowed {
			return fmt.Errorf("release workflow exceeds the permission ceiling with %q", strings.TrimSpace(line))
		}
		if want[line] {
			return fmt.Errorf("release workflow repeats permission %q", strings.TrimSpace(line))
		}
		want[line] = true
	}
	for permission, found := range want {
		if !found {
			return fmt.Errorf("release workflow is missing permission %q", strings.TrimSpace(permission))
		}
	}
	return nil
}

func checkSingleReleaseJob(workflow string) error {
	lines := strings.Split(workflow, "\n")
	inJobs := false
	jobs := make([]string, 0, 1)
	for _, line := range lines {
		if line == "jobs:" {
			inJobs = true
			continue
		}
		if !inJobs || strings.TrimSpace(line) == "" || strings.HasPrefix(line, "    ") {
			continue
		}
		if strings.HasPrefix(line, "  ") && strings.HasSuffix(line, ":") {
			jobs = append(jobs, strings.TrimSuffix(strings.TrimSpace(line), ":"))
			continue
		}
		if !strings.HasPrefix(line, " ") {
			break
		}
	}
	if !slices.Equal(jobs, []string{"release"}) {
		return fmt.Errorf("release workflow must contain only the release job, got %q", jobs)
	}
	return nil
}

func checkRepositoryPortability(root string) error {
	attributes, err := os.ReadFile(filepath.Join(root, ".gitattributes")) // #nosec G304 -- repository-owned path.
	if err != nil {
		return fmt.Errorf("read .gitattributes: %w", err)
	}
	if string(attributes) != "* text=auto eol=lf\n*.pb -text\n*.png -text\n" {
		return errors.New(".gitattributes must enforce LF text and preserve binary protocol/image files")
	}
	workflow, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "ci.yml")) // #nosec G304 -- repository-owned path.
	if err != nil {
		return fmt.Errorf("read CI workflow: %w", err)
	}
	return checkCIWorkflow(string(workflow))
}

func checkCIWorkflow(workflow string) error {
	text := strings.ReplaceAll(workflow, "\r\n", "\n")
	required := []string{
		"uses: spice-framework/.github/.github/workflows/go-verify.yml@" + verifyWorkflowCommit,
		"with: {go-version: 1.26.5}",
		"  quality:\n    runs-on: ubuntu-latest\n    timeout-minutes: 40\n",
		"    needs: [verify, quality]",
		`test "${{ needs.verify.result }}" = success && test "${{ needs.quality.result }}" = success`,
	}
	for _, contract := range required {
		if strings.Count(text, contract) != 1 {
			return fmt.Errorf("CI workflow must contain exactly one %q", contract)
		}
	}
	for _, forbidden := range []string{"strategy:", "matrix:", "windows-latest", "fail-fast:"} {
		if strings.Contains(text, forbidden) {
			return fmt.Errorf("CI full-quality job contains forbidden %q", forbidden)
		}
	}
	bootstrap := strings.Index(text, "go run ./internal/qualitygate -mode=tools-bootstrap")
	verify := strings.Index(text, "go run ./internal/qualitygate -mode=verify")
	if bootstrap < 0 || verify <= bootstrap ||
		strings.Count(text, "go run ./internal/qualitygate -mode=tools-bootstrap") != 1 ||
		strings.Count(text, "go run ./internal/qualitygate -mode=verify") != 1 {
		return errors.New("CI full-quality job must bootstrap pinned tools exactly once before offline verification")
	}
	lines := strings.Split(text, "\n")
	jobs := make([]string, 0, 3)
	inJobs := false
	for _, line := range lines {
		if line == "jobs:" {
			inJobs = true
			continue
		}
		if !inJobs {
			continue
		}
		if line != "" && !strings.HasPrefix(line, " ") {
			break
		}
		if strings.HasPrefix(line, "  ") && !strings.HasPrefix(line, "    ") && strings.HasSuffix(line, ":") {
			jobs = append(jobs, strings.TrimSuffix(strings.TrimSpace(line), ":"))
		}
	}
	if !slices.Equal(jobs, []string{"verify", "quality", "required"}) {
		return fmt.Errorf("CI workflow jobs must be verify, quality, and required, got %q", jobs)
	}
	return nil
}

func checkFormatting(ctx context.Context, root string) error {
	files, err := goFiles(root)
	if err != nil {
		return err
	}
	files = slices.DeleteFunc(files, func(path string) bool {
		return strings.HasSuffix(path, ".pb.go")
	})
	for _, name := range []string{"goimports", "gofumpt"} {
		executable, pathErr := toolPath(ctx, root, name)
		if pathErr != nil {
			return pathErr
		}
		for _, batch := range formattingBatches(files) {
			output, runErr := capture(ctx, root, nil, executable, append([]string{"-l"}, batch...)...)
			if runErr != nil {
				return runErr
			}
			if strings.TrimSpace(output) != "" {
				return fmt.Errorf("%s requires formatting: %s", name, strings.Join(strings.Fields(output), ", "))
			}
		}
	}
	return nil
}

const (
	maximumFormattingBatchFiles = 128
	maximumFormattingBatchBytes = 12 << 10
)

func formattingBatches(files []string) [][]string {
	result := make([][]string, 0, (len(files)+maximumFormattingBatchFiles-1)/maximumFormattingBatchFiles)
	current := make([]string, 0, min(len(files), maximumFormattingBatchFiles))
	currentBytes := len("-l")
	for _, file := range files {
		fileBytes := len(file) + 3 // argument separator plus a conservative pair of quotes.
		if len(current) > 0 && (len(current) == maximumFormattingBatchFiles ||
			currentBytes+fileBytes > maximumFormattingBatchBytes) {
			result = append(result, current)
			current = make([]string, 0, min(len(files), maximumFormattingBatchFiles))
			currentBytes = len("-l")
		}
		current = append(current, file)
		currentBytes += fileBytes
	}
	if len(current) > 0 {
		result = append(result, current)
	}
	return result
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

type bufGenerationTemplate struct {
	Version string                `json:"version"`
	Plugins []bufGenerationPlugin `json:"plugins"`
	Inputs  []bufGenerationInput  `json:"inputs"`
}

type bufGenerationPlugin struct {
	Local  []string `json:"local"`
	Out    string   `json:"out"`
	Option []string `json:"opt"`
}

type bufGenerationInput struct {
	Directory string `json:"directory"`
}

func checkProtocol(ctx context.Context, root string) (returnErr error) {
	buf, err := toolPath(ctx, root, "buf")
	if err != nil {
		return err
	}
	if err = command(ctx, root, nil, buf, "lint", "."); err != nil {
		return err
	}
	if err = command(ctx, root, nil, buf, "breaking", ".", "--against", "schema-baseline"); err != nil {
		return err
	}
	temporary, err := os.MkdirTemp("", "spice-agent-protobuf-*")
	if err != nil {
		return fmt.Errorf("create protobuf comparison directory: %w", err)
	}
	defer func() { returnErr = errors.Join(returnErr, os.RemoveAll(temporary)) }()
	if err = generateProtocol(ctx, root, temporary); err != nil {
		return err
	}
	want, err := generatedProtocolDigests(temporary)
	if err != nil {
		return err
	}
	got, err := generatedProtocolDigests(root)
	if err != nil {
		return err
	}
	if !maps.Equal(got, want) {
		return errors.New("generated Protobuf Go differs from repository schemas; run make proto")
	}
	return nil
}

func generateProtocol(ctx context.Context, root, output string) (returnErr error) {
	buf, err := toolPath(ctx, root, "buf")
	if err != nil {
		return err
	}
	temporary, err := os.MkdirTemp("", "spice-agent-buf-template-*")
	if err != nil {
		return fmt.Errorf("create Buf template directory: %w", err)
	}
	defer func() { returnErr = errors.Join(returnErr, os.RemoveAll(temporary)) }()
	template := bufGenerationTemplate{
		Version: "v2",
		Plugins: []bufGenerationPlugin{
			{
				Local:  []string{exactGoExecutable(), "-C", filepath.Join(root, "tools"), "tool", "protoc-gen-go"},
				Out:    output,
				Option: []string{"module=" + modulePath},
			},
			{
				Local:  []string{exactGoExecutable(), "-C", filepath.Join(root, "tools"), "tool", "protoc-gen-go-grpc"},
				Out:    output,
				Option: []string{"module=" + modulePath},
			},
		},
		Inputs: []bufGenerationInput{{Directory: filepath.Join(root, "proto")}},
	}
	content, err := json.Marshal(template)
	if err != nil {
		return fmt.Errorf("encode Buf generation template: %w", err)
	}
	temporaryRoot, err := os.OpenRoot(temporary)
	if err != nil {
		return fmt.Errorf("open Buf template directory: %w", err)
	}
	defer func() { returnErr = errors.Join(returnErr, temporaryRoot.Close()) }()
	if err = temporaryRoot.WriteFile("buf.gen.json", content, 0o600); err != nil {
		return fmt.Errorf("write Buf generation template: %w", err)
	}
	return command(ctx, root, nil, buf, "generate", "--template", filepath.Join(temporary, "buf.gen.json"))
}

func generatedProtocolDigests(root string) (map[string][sha256.Size]byte, error) {
	result := make(map[string][sha256.Size]byte)
	for _, directory := range []string{"common/v1", "engine/v1", "plugin/v1"} {
		path := filepath.Join(root, filepath.FromSlash(directory))
		entries, err := os.ReadDir(path)
		if err != nil {
			return nil, fmt.Errorf("read generated protocol directory %s: %w", directory, err)
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".pb.go") {
				continue
			}
			content, readErr := os.ReadFile(filepath.Join(path, entry.Name())) // #nosec G304 -- bounded generated paths.
			if readErr != nil {
				return nil, readErr
			}
			result[filepath.ToSlash(filepath.Join(directory, entry.Name()))] = sha256.Sum256(content)
		}
	}
	return result, nil
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

func fuzz(ctx context.Context, root string, environment map[string]string) error {
	for _, target := range fuzzTargets() {
		if err := command(ctx, root, environment, "go", fuzzArguments(target)...); err != nil {
			return err
		}
	}
	return nil
}

type fuzzTarget struct{ pkg, name string }

func fuzzArguments(target fuzzTarget) []string {
	return []string{"test", "-run=^$", "-fuzz=^" + target.name + "$", "-fuzztime=100x", target.pkg}
}

func fuzzTargets() []fuzzTarget {
	return []fuzzTarget{
		{"./message", "FuzzNewID"},
		{"./tool", "FuzzToolCall"},
		{"./process", "FuzzSpecValidation"},
		{"./process", "FuzzExitedOutcome"},
		{"./process", "FuzzLookupValidation"},
		{"./process", "FuzzSHA256"},
		{"./agent", "FuzzParseSnapshot"},
		{"./agent", "FuzzDecodeToolStartedOccurrence"},
		{"./agent", "FuzzDecodeToolTerminalOccurrence"},
		{"./annotation/agent", "FuzzToolHandler"},
		{"./common/v1", "FuzzCommonEnvelope"},
		{"./engine/v1", "FuzzEngineEnvelope"},
		{"./plugin/v1", "FuzzPluginEnvelope"},
		{"./plugin/v1", "FuzzBootstrap"},
		{"./daemon/endpoint", "FuzzDecodeMetadata"},
	}
}

func toolPath(ctx context.Context, root, name string) (string, error) {
	output, err := capture(ctx, root, nil, "go", "-C", "tools", "tool", "-n", name)
	if err != nil {
		return "", fmt.Errorf(
			"resolve tool %q offline; run make tools-bootstrap once: %w",
			name,
			err,
		)
	}
	path := strings.TrimSpace(output)
	if path == "" {
		return "", fmt.Errorf("resolve tool %q: empty path", name)
	}
	return path, nil
}

func treeDigests(root string) (map[string][sha256.Size]byte, error) {
	return digests(root, false)
}

func sourceTreeDigests(root string) (map[string][sha256.Size]byte, error) {
	return digests(root, true)
}

func digests(root string, excludeGit bool) (map[string][sha256.Size]byte, error) {
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
			if excludeGit && path == ".git" {
				return filepath.SkipDir
			}
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
		if strings.HasSuffix(path, "_test.go") || strings.Contains(path, string(filepath.Separator)+"internal"+string(filepath.Separator)+"qualitygate"+string(filepath.Separator)) {
			continue
		}
		content, readErr := os.ReadFile(path) // #nosec G304 -- paths come from the bounded repository walk.
		if readErr != nil {
			return readErr
		}
		for _, forbidden := range []string{
			"type RuntimeGraph", "type ServiceLocator", "type ExtensionRegistry",
			"reflect.Value", "plugin.Open(", "packages.Load(",
			"switch invocation.CanonicalName", "switch params.Descriptor.Name",
		} {
			if bytes.Contains(content, []byte(forbidden)) {
				return fmt.Errorf("%s contains forbidden compiled composition mechanism %q", path, forbidden)
			}
		}
		relative, relativeErr := filepath.Rel(root, path)
		if relativeErr != nil {
			return relativeErr
		}
		relative = filepath.ToSlash(relative)
		first, _, _ := strings.Cut(relative, "/")
		if slices.Contains([]string{"agent", "event", "interaction", "message", "model", "stage", "tool"}, first) {
			for _, forbiddenImport := range []string{
				`"google.golang.org/grpc`,
				`"google.golang.org/protobuf`,
				`"` + modulePath + `/common/v1"`,
				`"` + modulePath + `/engine/v1"`,
				`"` + modulePath + `/plugin/v1"`,
			} {
				if bytes.Contains(content, []byte(forbiddenImport)) {
					return fmt.Errorf("kernel file %s imports process-boundary package %s", relative, forbiddenImport)
				}
			}
		}
		if filepath.ToSlash(filepath.Dir(relative)) == "plugin/host" {
			for _, forbiddenImport := range []string{
				`"` + modulePath + `/agent`,
				`"` + modulePath + `/client`,
				`"` + modulePath + `/daemon`,
				`"` + modulePath + `/engine/v1`,
				`"` + modulePath + `/event`,
				`"` + modulePath + `/interaction`,
				`"` + modulePath + `/internal/`,
				`"` + modulePath + `/message`,
				`"` + modulePath + `/model`,
				`"golang.org/x/sys/unix"`,
				`"golang.org/x/sys/windows"`,
			} {
				if bytes.Contains(content, []byte(forbiddenImport)) {
					return fmt.Errorf("plugin host file %s imports forbidden package %s", relative, forbiddenImport)
				}
			}
		}
		if relative != "stage/plan.go" && bytes.Contains(content, []byte("ApplyToolDispatchDecorators(")) {
			return fmt.Errorf("%s bypasses terminal tool dispatch pipeline composition", relative)
		}
	}
	if _, statErr := os.Stat(filepath.Join(root, "go.mod")); statErr == nil {
		return checkToolDispatchBoundary(root)
	}
	return nil
}

func checkToolDispatchBoundary(root string) error {
	required := []struct {
		name      string
		fragments []string
	}{
		{name: "agent/agent.go", fragments: []string{
			"stage.NewToolDispatchScope(",
			"emitter.run.requester",
			"type runInteractionRequester struct",
			"return requester.run.Interact(ctx, request)",
			"NewToolStartedOccurrence(",
			"NewToolTerminalOccurrence(",
			"occurrence.Encode()",
			"dispatcher.Dispatch(ctx, scope, call, reporter)",
		}},
		{name: "agent/prepared_execution.go", fragments: []string{
			"run.requester = &runInteractionRequester{run: run}",
		}},
		{name: "agent/tool_started.go", fragments: []string{
			"ToolStartedOccurrenceVersion = \"spice.agent.tool-started/v1alpha1\"",
			"MaximumToolStartedOccurrenceBytes = 4096",
			"func DecodeToolStartedOccurrence(",
		}},
		{name: "agent/tool_terminal.go", fragments: []string{
			"ToolTerminalOccurrenceVersion = \"spice.agent.tool-terminal/v1alpha1\"",
			"MaximumToolTerminalOccurrenceBytes = 1024",
			"func DecodeToolTerminalOccurrence(",
			"kind == event.ToolCompleted || kind == event.ToolFailed",
		}},
		{name: "daemon/grpcserver/stream_events.go", fragments: []string{
			"agent.DecodeToolStartedOccurrence(payload)",
			"agent.DecodeToolTerminalOccurrence(kind, payload)",
			"problem = \"tool execution failed\"",
			"CallID string `json:\"call_id\"`",
			"Name   string `json:\"name\"`",
		}},
		{name: "plugin/host/host.go", fragments: []string{
			"Processes    process.VerifiedLauncher",
			"stage.SnapshotToolDispatcher(config.Compiled)",
			"stage.ApplyToolDispatchPipeline(base, guards, decorators)",
			"stage.ApplyToolDispatchPipeline(merged, host.guards, host.decorators)",
		}},
		{name: "process/verified_launcher.go", fragments: []string{
			"type VerifiedLauncher interface",
			"StartVerified(context.Context, *ExecutableLease, Spec) (Process, error)",
		}},
		{name: "process/verified_executable.go", fragments: []string{
			"func VerifyExecutable(",
			"func (lease *ExecutableLease) ValidateSpec(",
			"func (lease *ExecutableLease) DuplicateForLaunch(",
			"func (lease *ExecutableLease) Recheck(",
		}},
		{name: "process/materialized_executable.go", fragments: []string{
			"func (lease *ExecutableLease) MaterializeForLaunch(",
			"os.MkdirTemp(parent, materializedExecutableDirectoryPattern)",
			"destination.Sync()",
			"VerifyExecutable(ctx, path, lease.Digest())",
			"cleanupMaterializedExecutable(path, directory)",
		}},
		{name: "plugin/host/digest.go", fragments: []string{
			"type SHA256 = process.SHA256",
			"process.VerifyExecutable(ctx, executable.Path(), executable.SHA256())",
		}},
		{name: "plugin/host/launcher.go", fragments: []string{
			"processes process.VerifiedLauncher",
			"launcher.processes.StartVerified(ctx, candidate.lease, spec)",
			"recheckVerifiedExecutable(ctx, candidate.lease)",
		}},
		{name: "plugin/host/candidate.go", fragments: []string{
			"lease      *process.ExecutableLease",
			"closeVerifiedExecutable(candidate.lease)",
		}},
		{name: "plugin/host/autoconfigure/autoconfigure.go", fragments: []string{
			"launcher process.VerifiedLauncher",
		}},
		{name: "stage/dispatch_guard.go", fragments: []string{
			"type ToolDispatchScope struct",
			"interactionRequester *toolInteractionCapability",
			"func (scope ToolDispatchScope) RequestInteraction(",
			"scope.interactionRequester == other.interactionRequester",
			"type ToolDispatchGuard interface",
			"tool dispatch continuation is closed or was already invoked",
		}},
		{name: "internal/spicegen/compositionproof/spice_providers_gen.go", fragments: []string{
			"[]stage.ToolDispatchGuard{fixtureDispatchGuard}",
		}},
	}
	for _, source := range required {
		content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(source.name))) // #nosec G304 -- fixed repository paths.
		if err != nil {
			return fmt.Errorf("tool dispatch boundary source %s: %w", source.name, err)
		}
		for _, fragment := range source.fragments {
			if !bytes.Contains(content, []byte(fragment)) {
				return fmt.Errorf("tool dispatch boundary source %s is missing %q", source.name, fragment)
			}
		}
	}
	return nil
}

func coverage(ctx context.Context, root string, environment map[string]string) (returnErr error) {
	profile, err := os.CreateTemp("", "spice-agent-coverage-*.out")
	if err != nil {
		return err
	}
	path := profile.Name()
	if err = profile.Close(); err != nil {
		return err
	}
	defer func() { returnErr = errors.Join(returnErr, os.Remove(path)) }()
	packageOutput, err := capture(ctx, root, environment, "go", "list", "./...")
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
	if err = command(ctx, root, environment, "go", arguments...); err != nil {
		return err
	}
	if err = excludeGeneratedCoverage(path); err != nil {
		return err
	}
	report, err := capture(ctx, root, environment, "go", "tool", "cover", "-func="+path)
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

func excludeGeneratedCoverage(path string) error {
	content, err := os.ReadFile(path) // #nosec G304 -- path is the gate-owned temporary profile.
	if err != nil {
		return fmt.Errorf("read coverage profile: %w", err)
	}
	lines := strings.Split(string(content), "\n")
	if len(lines) == 0 || !strings.HasPrefix(lines[0], "mode: ") {
		return errors.New("coverage profile has no mode header")
	}
	filtered := make([]string, 0, len(lines))
	filtered = append(filtered, lines[0])
	generatedPrefix := modulePath + "/internal/spicegen/"
	for _, line := range lines[1:] {
		if line == "" || strings.Contains(line, generatedPrefix) ||
			(strings.HasPrefix(line, modulePath+"/") && strings.Contains(line, ".pb.go:")) {
			continue
		}
		filtered = append(filtered, line)
	}
	// #nosec G304,G703 -- path is the gate-owned temporary profile created by coverage.
	return os.WriteFile(path, []byte(strings.Join(filtered, "\n")+"\n"), 0o600)
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
	executable = qualityExecutable(executable)
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

func networkCommand(
	ctx context.Context,
	directory string,
	arguments ...string,
) error {
	// #nosec G204,G702 -- only exact copied module graphs are downloaded.
	process := exec.CommandContext(ctx, exactGoExecutable(), arguments...)
	process.Dir = directory
	process.Env = commandEnvironment(true, nil)
	process.Stdout = os.Stdout
	process.Stderr = os.Stderr
	if err := process.Run(); err != nil {
		return fmt.Errorf("go %s: %w", strings.Join(arguments, " "), err)
	}
	return nil
}

func qualityExecutable(executable string) string {
	if executable == "go" {
		return exactGoExecutable()
	}
	return executable
}

func exactGoExecutable() string {
	//nolint:staticcheck // Gate runs in place under the selected exact toolchain.
	return filepath.Join(runtime.GOROOT(), "bin", goExecutableName(runtime.GOOS))
}

func goExecutableName(goos string) string {
	if goos == "windows" {
		return "go.exe"
	}
	return "go"
}

func mergedEnvironment(overrides map[string]string) []string {
	return commandEnvironment(false, overrides)
}

func commandEnvironment(network bool, overrides map[string]string) []string {
	values := map[string]string{
		"GOFLAGS":     "",
		"GOTOOLCHAIN": "local",
		"GOWORK":      "off",
	}
	if network {
		values["GOAUTH"] = "off"
		values["GONOPROXY"] = ""
		values["GONOSUMDB"] = ""
		values["GOPRIVATE"] = ""
		values["GOPROXY"] = "https://proxy.golang.org"
		values["GOSUMDB"] = "sum.golang.org"
	} else {
		values["GOPROXY"] = "off"
	}
	for key, value := range overrides {
		values[strings.ToUpper(key)] = value
	}
	result := make([]string, 0, len(os.Environ())+len(values))
	for _, entry := range os.Environ() {
		key, _, found := strings.Cut(entry, "=")
		if found {
			upperKey := strings.ToUpper(key)
			if sensitiveEnvironmentKey(upperKey) {
				continue
			}
			if _, replaced := values[upperKey]; !replaced {
				result = append(result, entry)
			}
		}
	}
	for key, value := range values {
		result = append(result, key+"="+value)
	}
	slices.Sort(result)
	return result
}

func sensitiveEnvironmentKey(key string) bool {
	return strings.Contains(key, "TOKEN") ||
		strings.Contains(key, "PASSWORD") ||
		strings.Contains(key, "SECRET") ||
		strings.HasSuffix(key, "API_KEY") ||
		strings.HasSuffix(key, "ACCESS_KEY") ||
		strings.HasSuffix(key, "PRIVATE_KEY")
}
