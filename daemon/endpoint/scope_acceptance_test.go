//go:build spice_acceptance

package endpoint

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAcceptanceUserScopeIsAnIsolatedValidatedChild(t *testing.T) {
	current, err := CurrentUserScope()
	if err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(
		current.Directory(),
		fmt.Sprintf("acceptance-scope-%d-%d", os.Getpid(), time.Now().UnixNano()),
	)
	t.Cleanup(func() {
		if removeErr := os.RemoveAll(directory); removeErr != nil {
			t.Error(removeErr)
		}
	})
	scope, err := AcceptanceUserScope(directory)
	if err != nil {
		t.Fatal(err)
	}
	if scope.Directory() != directory || scope.Transport() != current.Transport() ||
		scope.Address() == current.Address() {
		t.Fatalf(
			"acceptance scope = directory %q transport %q address %q",
			scope.Directory(), scope.Transport(), scope.Address(),
		)
	}
	if err = scope.Validate(); err != nil {
		t.Fatalf("validate acceptance scope: %v", err)
	}
}

func TestAcceptanceUserScopeRejectsUnsafeDirectories(t *testing.T) {
	current, err := CurrentUserScope()
	if err != nil {
		t.Fatal(err)
	}
	for _, directory := range []string{
		"relative",
		current.Directory(),
		filepath.Dir(current.Directory()),
		current.Directory() + string(filepath.Separator) + "child" + string(filepath.Separator) + ".." + string(filepath.Separator) + "other",
		current.Directory() + string(filepath.Separator),
	} {
		t.Run(strings.ReplaceAll(directory, string(filepath.Separator), "_"), func(t *testing.T) {
			if _, scopeErr := AcceptanceUserScope(directory); scopeErr == nil {
				t.Fatalf("unsafe directory %q was accepted", directory)
			}
		})
	}
}
