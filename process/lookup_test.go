package process_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/spice-framework/spice-agent/process"
)

func TestLookupIsImmutableCanonicalAndRedacted(t *testing.T) {
	t.Parallel()
	root := filepath.Clean(t.TempDir())
	environment := []string{"TOKEN=private-value", "PATH=private-path"}
	lookup, err := process.NewLookup("private-tool", root, environment)
	if err != nil {
		t.Fatal(err)
	}
	environment[0] = "MUTATED=yes"
	if err = lookup.Validate(); err != nil {
		t.Fatal(err)
	}
	if lookup.RequestedExecutable() != "private-tool" || lookup.WorkingDirectory() != root ||
		!slices.Equal(lookup.Environment(), []string{"PATH=private-path", "TOKEN=private-value"}) {
		t.Fatalf("lookup = %#v", lookup)
	}
	returned := lookup.Environment()
	returned[0] = "MUTATED=again"
	if !slices.Equal(lookup.Environment(), lookup.Clone().Environment()) {
		t.Fatal("lookup exposed environment storage")
	}
	encoded, err := json.Marshal(lookup)
	if err != nil {
		t.Fatal(err)
	}
	for _, rendered := range []string{
		fmt.Sprint(lookup), fmt.Sprintf("%#v", lookup), fmt.Sprintf("%+v", lookup), string(encoded),
	} {
		for _, secret := range []string{"private-tool", "private-value", "private-path"} {
			if strings.Contains(rendered, secret) {
				t.Fatalf("lookup formatting leaked %q: %q", secret, rendered)
			}
		}
	}
}

func TestLookupAcceptsNameRelativeAndAbsoluteRequests(t *testing.T) {
	t.Parallel()
	root := filepath.Clean(t.TempDir())
	for _, requested := range []string{"git", filepath.Join("bin", "tool"), filepath.Join(root, "tool")} {
		if _, err := process.NewLookup(requested, root, nil); err != nil {
			t.Fatalf("request %q: %v", requested, err)
		}
	}
}

func TestLookupRejectsInvalidInputsWithoutLeakingThem(t *testing.T) {
	t.Parallel()
	root := filepath.Clean(t.TempDir())
	tests := map[string]struct {
		requested   string
		workdir     string
		environment []string
		field       string
		problem     process.SpecProblem
	}{
		"missing request":       {workdir: root, field: "requested_executable", problem: process.ProblemRequired},
		"request nul":           {requested: "private\x00tool", workdir: root, field: "requested_executable", problem: process.ProblemContainsNUL},
		"request invalid utf8":  {requested: string([]byte{0xff}), workdir: root, field: "requested_executable", problem: process.ProblemInvalidUTF8},
		"relative workdir":      {requested: "tool", workdir: "private", field: "working_directory", problem: process.ProblemNotAbsolute},
		"malformed environment": {requested: "tool", workdir: root, environment: []string{"PRIVATE"}, field: "environment", problem: process.ProblemMalformed},
		"duplicate environment": {requested: "tool", workdir: root, environment: []string{"Private=1", "PRIVATE=2"}, field: "environment", problem: process.ProblemDuplicate},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := process.NewLookup(test.requested, test.workdir, test.environment)
			var lookupErr *process.LookupError
			if !errors.As(err, &lookupErr) || lookupErr.Field() != test.field ||
				lookupErr.Problem() != test.problem {
				t.Fatalf("error = %T %v", err, err)
			}
			if strings.Contains(strings.ToLower(err.Error()), "private") {
				t.Fatalf("lookup error leaked input: %v", err)
			}
		})
	}
	if err := (process.Lookup{}).Validate(); err == nil {
		t.Fatal("zero lookup validated")
	}
}

func TestLookupBounds(t *testing.T) {
	t.Parallel()
	root := filepath.Clean(t.TempDir())
	if _, err := process.NewLookup(strings.Repeat("x", process.MaximumValueBytes), root, nil); err != nil {
		t.Fatalf("maximum request: %v", err)
	}
	if _, err := process.NewLookup(strings.Repeat("x", process.MaximumValueBytes+1), root, nil); err == nil {
		t.Fatal("oversized request succeeded")
	}
	environment := make([]string, process.MaximumEnvironment+1)
	if _, err := process.NewLookup("tool", root, environment); err == nil {
		t.Fatal("oversized environment count succeeded")
	}
}

func TestResolverFuncReceivesExactContextAndImmutableLookup(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	root := filepath.Clean(t.TempDir())
	lookup, err := process.NewLookup("git", root, []string{"PATH=value"})
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, "git")
	resolver := process.ResolverFunc(func(received context.Context, value process.Lookup) (string, error) {
		if received != ctx || value.RequestedExecutable() != "git" || value.WorkingDirectory() != root {
			t.Fatalf("resolver inputs = %#v", value)
		}
		return want, nil
	})
	resolved, err := resolver.Resolve(ctx, lookup)
	if err != nil || resolved != want {
		t.Fatalf("resolve = %q, %v", resolved, err)
	}
	cancel()
	if resolved != want {
		t.Fatal("resolution changed after caller cancellation")
	}
}
