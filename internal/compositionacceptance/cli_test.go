package compositionacceptance_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

const spiceCommand = "github.com/spice-framework/toolchain/cmd/spice"

func TestCompositionCLIReadOnlyDiagnosticsAndNavigation(t *testing.T) {
	t.Parallel()
	root := repositoryRoot(t)

	stdout, stderr, err := runSpice(t, root, "annotations", "doctor", "./internal/compositionfixture")
	if err != nil || stderr != "" || !strings.Contains(stdout, "9 descriptor(s), 2 tool(s)") {
		t.Fatalf("annotations doctor = %q, %q, %v", stdout, stderr, err)
	}

	stdout, stderr, err = runSpice(t, root, "beans", "--explain", "--format=json", "./internal/compositionfixture")
	if err != nil || stderr != "" {
		t.Fatalf("beans --explain = %q, %q, %v", stdout, stderr, err)
	}
	for _, expected := range []string{
		`"name": "beta"`, `"primary": true`, `"name": "fallback"`,
		`"fallback": true`, `"name": "read"`, `"name": "write"`,
	} {
		if !strings.Contains(stdout, expected) {
			t.Fatalf("beans --explain missing %q: %s", expected, stdout)
		}
	}

	stdout, stderr, err = runSpice(t, root, "modules", "--format=json", "./...")
	if err != nil || stderr != "" ||
		!strings.Contains(stdout, `"id": "github.com/spice-framework/spice-agent"`) ||
		!strings.Contains(stdout, `"name": "kernel"`) {
		t.Fatalf("modules --format=json = %q, %q, %v", stdout, stderr, err)
	}

	sourcePath, sourceLine, generatedPath, generatedLine := mappingEvidence(t, root)
	stdout, stderr, err = runSpice(
		t,
		root,
		"generated", "--source", sourcePath, "--line", fmt.Sprint(sourceLine),
		"--target", "compositionproof", "--format", "json",
	)
	if err != nil || stderr != "" ||
		!strings.Contains(stdout, `"direction": "source-to-generated"`) ||
		!strings.Contains(stdout, filepath.ToSlash(generatedPath)) {
		t.Fatalf("generated source lookup = %q, %q, %v", stdout, stderr, err)
	}

	stdout, stderr, err = runSpice(
		t,
		root,
		"generated", "--generated", generatedPath, "--line", fmt.Sprint(generatedLine),
		"--target", "compositionproof", "--format", "json",
	)
	if err != nil || stderr != "" ||
		!strings.Contains(stdout, `"direction": "generated-to-source"`) ||
		!strings.Contains(stdout, filepath.ToSlash(sourcePath)) {
		t.Fatalf("generated reverse lookup = %q, %q, %v", stdout, stderr, err)
	}

	stdout, stderr, err = runSpice(
		t,
		root,
		"test", "--module=github.com/spice-framework/spice-agent", "--count=1",
		"--run=GeneratedComposition", "./...",
	)
	if err != nil || stderr != "" || !strings.Contains(stdout, "Spice module tests passed") {
		t.Fatalf("spice test = %q, %q, %v", stdout, stderr, err)
	}
}

func TestCompositionNegativeGraphsAreCompilerOwnedAndDeterministic(t *testing.T) {
	t.Parallel()
	root := repositoryRoot(t)
	var baseline string
	for iteration := range 3 {
		stdout, stderr, err := runSpice(t, root, "verify", "./testdata/composition_ambiguity")
		if err == nil || stdout != "" {
			t.Fatalf("ambiguity iteration %d = %q, %q, %v", iteration, stdout, stderr, err)
		}
		for _, expected := range []string{
			"spice.graph.ambiguous-dependency", "multiple explicit providers match",
			"composition_ambiguity.First", "composition_ambiguity.Second",
		} {
			if !strings.Contains(stderr, expected) {
				t.Fatalf("ambiguity missing %q: %s", expected, stderr)
			}
		}
		if strings.Index(stderr, "composition_ambiguity.First") >
			strings.Index(stderr, "composition_ambiguity.Second") {
			t.Fatalf("ambiguity candidates are not deterministic: %s", stderr)
		}
		if iteration == 0 {
			baseline = stderr
		} else if stderr != baseline {
			t.Fatalf("ambiguity changed between runs:\nfirst=%s\nnext=%s", baseline, stderr)
		}
	}

	stdout, stderr, err := runSpice(t, root, "verify", "./testdata/composition_wrong_output")
	if err == nil || stdout != "" ||
		!strings.Contains(stderr, "spice.graph.missing-dependency") ||
		!strings.Contains(stderr, "requires exact type") {
		t.Fatalf("wrong concrete output = %q, %q, %v", stdout, stderr, err)
	}

	for _, testCase := range []struct {
		path string
		want string
	}{
		{"./testdata/composition_wrong_model", "@ModelProvider factory result must be exact"},
		{"./testdata/composition_wrong_stage", "@Stage factory result string must be a named Go interface"},
		{"./testdata/composition_wrong_tool", "@Tool factory result must be exact"},
	} {
		stdout, stderr, err = runSpice(t, root, "verify", testCase.path)
		if err == nil || stdout != "" || !strings.Contains(stderr, testCase.want) {
			t.Fatalf("wrong agent annotation %s = %q, %q, %v", testCase.path, stdout, stderr, err)
		}
	}
}

func TestCompositionGenerationIsDeterministicAndOwnershipGuarded(t *testing.T) {
	t.Parallel()
	root := repositoryRoot(t)
	copyRoot := t.TempDir()
	copyFixtureModule(t, root, copyRoot)

	before := generatedDigests(t, copyRoot)
	for _, arguments := range [][]string{
		{"generate", "--target", "CompositionProof", ".", "./internal/compositionfixture"},
		{"generate", "--check", "--target", "CompositionProof", ".", "./internal/compositionfixture"},
		{"generate", "--diff", "--target", "CompositionProof", ".", "./internal/compositionfixture"},
	} {
		stdout, stderr, err := runSpice(t, copyRoot, arguments...)
		if err != nil || stderr != "" || !strings.Contains(stdout, "generation is current") {
			t.Fatalf("spice %v = %q, %q, %v", arguments, stdout, stderr, err)
		}
	}
	after := generatedDigests(t, copyRoot)
	if !mapsEqual(before, after) {
		t.Fatalf("byte-identical regeneration changed output:\nbefore=%v\nafter=%v", before, after)
	}

	generated := filepath.Join(
		copyRoot,
		"internal", "spicegen", "compositionproof", "spice_providers_gen.go",
	)
	original, err := os.ReadFile(generated)
	if err != nil {
		t.Fatal(err)
	}
	edited := append(append([]byte(nil), original...), []byte("\n// manual edit\n")...)
	if err = os.WriteFile(generated, edited, 0o600); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, err := runSpice(
		t,
		copyRoot,
		"generate", "--target", "CompositionProof", ".", "./internal/compositionfixture",
	)
	if err == nil || stdout != "" || !strings.Contains(stderr, "owned generated file was modified") {
		t.Fatalf("manual-edit generation = %q, %q, %v", stdout, stderr, err)
	}
	preserved, readErr := os.ReadFile(generated)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !bytes.Equal(preserved, edited) {
		t.Fatal("generation overwrote a manually edited owned file")
	}
}

func TestGeneratedCompositionUsesOnlyDirectCalls(t *testing.T) {
	t.Parallel()
	content, err := os.ReadFile(filepath.Join(
		repositoryRoot(t),
		"internal", "spicegen", "compositionproof", "spice_providers_gen.go",
	))
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"ConstructBeta", "ConstructProof",
		`map[string]compositionfixture.ToolAlias{"read": read, "write": write}`,
	} {
		if !bytes.Contains(content, []byte(expected)) {
			t.Fatalf("generated providers missing direct-call evidence %q", expected)
		}
	}
	for _, forbidden := range []string{"reflect.", "RuntimeGraph", "ServiceLocator", "ExtensionRegistry"} {
		if bytes.Contains(content, []byte(forbidden)) {
			t.Fatalf("generated providers contain forbidden mechanism %q", forbidden)
		}
	}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	directory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		content, readErr := os.ReadFile(filepath.Join(directory, "go.mod"))
		if readErr == nil && bytes.Contains(
			content,
			[]byte("module github.com/spice-framework/spice-agent\n"),
		) {
			return directory
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			t.Fatal("locate spice-agent module root")
		}
		directory = parent
	}
}

func runSpice(t *testing.T, root string, arguments ...string) (string, string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	commandArguments := append([]string{"tool", spiceCommand}, arguments...)
	command := exec.CommandContext(ctx, "go", commandArguments...)
	command.Dir = root
	command.Env = append(
		os.Environ(),
		"GOWORK=off", "GOPROXY=off", "GOFLAGS=-mod=vendor", "GOTOOLCHAIN=local",
	)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	if ctx.Err() != nil {
		t.Fatalf("spice %v timed out: %v", arguments, ctx.Err())
	}
	return stdout.String(), stderr.String(), err
}

type manifest struct {
	Files []struct {
		Path     string `json:"path"`
		Mappings []struct {
			Source struct {
				Path string `json:"path"`
				Line int    `json:"line"`
			} `json:"source"`
			Generated struct {
				StartLine int `json:"start_line"`
			} `json:"generated"`
		} `json:"mappings"`
	} `json:"files"`
}

func mappingEvidence(t *testing.T, root string) (string, int, string, int) {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(root, ".spice", "compositionproof.manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var decoded manifest
	if err = json.Unmarshal(content, &decoded); err != nil {
		t.Fatal(err)
	}
	for _, file := range decoded.Files {
		if strings.HasSuffix(file.Path, "tools_spice_gen.go") && len(file.Mappings) != 0 {
			mapping := file.Mappings[0]
			return mapping.Source.Path, mapping.Source.Line, file.Path, mapping.Generated.StartLine
		}
	}
	t.Fatal("tools source mapping is missing")
	return "", 0, "", 0
}

func copyFixtureModule(t *testing.T, source, destination string) {
	t.Helper()
	for _, name := range []string{"go.mod", "go.sum", "doc.go", "manifest.go"} {
		copyFile(t, filepath.Join(source, name), filepath.Join(destination, name))
	}
	for _, name := range []string{
		".spice", "annotation", "cmd", "internal/annotationtool",
		"internal/compositionfixture", "internal/spicegen", "message", "model",
		"stage", "tool", "vendor",
	} {
		copyTree(t, filepath.Join(source, filepath.FromSlash(name)), filepath.Join(destination, filepath.FromSlash(name)))
	}
}

func copyTree(t *testing.T, source, destination string) {
	t.Helper()
	err := filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o750)
		}
		return copyFileError(path, target)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func copyFile(t *testing.T, source, destination string) {
	t.Helper()
	if err := copyFileError(source, destination); err != nil {
		t.Fatal(err)
	}
}

func copyFileError(source, destination string) error {
	content, err := os.ReadFile(source) // #nosec G304 -- source is a repository-owned test fixture.
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(destination), 0o750); err != nil {
		return err
	}
	return os.WriteFile(destination, content, 0o600)
}

func generatedDigests(t *testing.T, root string) map[string][sha256.Size]byte {
	t.Helper()
	result := make(map[string][sha256.Size]byte)
	for _, directory := range []string{".spice", "internal/spicegen"} {
		base := filepath.Join(root, directory)
		err := filepath.WalkDir(base, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				return nil
			}
			content, err := os.ReadFile(path) // #nosec G304 -- path is bounded by the copied fixture tree.
			if err != nil {
				return err
			}
			relative, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			result[filepath.ToSlash(relative)] = sha256.Sum256(content)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return result
}

func mapsEqual(left, right map[string][sha256.Size]byte) bool {
	if len(left) != len(right) {
		return false
	}
	keys := make([]string, 0, len(left))
	for key := range left {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	for _, key := range keys {
		if right[key] != left[key] {
			return false
		}
	}
	return true
}
