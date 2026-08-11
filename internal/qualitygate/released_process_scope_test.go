package main

import (
	"encoding/base64"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestReleasedProcessScopeOwnsBoundedSecretSafeState(t *testing.T) {
	t.Parallel()
	if _, err := newReleasedProcessScope("other"); err == nil {
		t.Fatal("unsupported released process scope succeeded")
	}
	for _, kind := range []string{"engine", "plugin"} {
		scope, err := newReleasedProcessScope(kind)
		if err != nil {
			t.Fatal(err)
		}
		if scope.Address() == "" || scope.AuthorityDirectory() == "" {
			t.Fatal("released process scope omitted owned paths")
		}
		if runtime.GOOS != "windows" &&
			(!filepath.IsAbs(scope.Address()) || filepath.Clean(scope.Address()) != scope.Address() || len(scope.Address()) > 100) {
			t.Fatalf("released Unix address is not clean, absolute, and bounded: %q", scope.Address())
		}
		authorization, err := scope.Authorization()
		if err != nil {
			t.Fatal(err)
		}
		encoded, present := strings.CutPrefix(authorization, "Bearer ")
		if !present {
			t.Fatal("released process authorization is not canonical")
		}
		decoded, err := base64.RawURLEncoding.DecodeString(encoded)
		if err != nil || len(decoded) != 32 {
			t.Fatal("released process authorization has an invalid size")
		}
		secret, err := scope.Secret()
		if err != nil {
			t.Fatal(err)
		}
		decoded, err = base64.RawURLEncoding.DecodeString(secret)
		if err != nil || len(decoded) != 32 {
			t.Fatal("released process secret has an invalid size")
		}
		if err = scope.Close(); err != nil {
			t.Fatal(err)
		}
		if err = scope.Close(); err != nil || scope.Address() != "" || scope.AuthorityDirectory() != "" {
			t.Fatal("released process scope close is not idempotent")
		}
	}
}
