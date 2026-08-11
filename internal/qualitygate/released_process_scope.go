package main

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
)

type releasedProcessScope struct {
	root    string
	address string
}

func newReleasedProcessScope(kind string) (*releasedProcessScope, error) {
	if kind != "engine" && kind != "plugin" {
		return nil, errors.New("released process kind is invalid")
	}
	base := ""
	if runtime.GOOS == "windows" {
		cache, err := os.UserCacheDir()
		if err != nil {
			return nil, fmt.Errorf("resolve released process cache: %w", err)
		}
		base = filepath.Join(cache, "spice-agent-released-matrix-tests")
		if err = os.MkdirAll(base, 0o700); err != nil {
			return nil, fmt.Errorf("create released process cache: %w", err)
		}
		if err = os.Chmod(base, 0o700); err != nil { // #nosec G302 -- a private directory requires owner execute/search permission.
			return nil, fmt.Errorf("protect released process cache: %w", err)
		}
	}
	root, err := os.MkdirTemp(base, "spice-agent-rg-"+kind+"-")
	if err != nil {
		return nil, fmt.Errorf("create released process scope: %w", err)
	}
	if err = os.Chmod(root, 0o700); err != nil { // #nosec G302 -- a private directory requires owner execute/search permission.
		_ = os.RemoveAll(root)
		return nil, fmt.Errorf("protect released process scope: %w", err)
	}
	random := make([]byte, 8)
	if _, err = io.ReadFull(rand.Reader, random); err != nil {
		_ = os.RemoveAll(root)
		return nil, errors.New("create released process address")
	}
	name := hex.EncodeToString(random)
	address := filepath.Join(root, kind+"-"+name+".sock")
	if runtime.GOOS == "windows" {
		address = `\\.\pipe\spice-agent-released-` + kind + "-" + name
	}
	return &releasedProcessScope{root: root, address: address}, nil
}

func (scope *releasedProcessScope) Address() string {
	if scope == nil || scope.root == "" {
		return ""
	}
	return scope.address
}

func (scope *releasedProcessScope) AuthorityDirectory() string {
	if scope == nil || scope.root == "" {
		return ""
	}
	return filepath.Join(scope.root, "authority")
}

func (scope *releasedProcessScope) Authorization() (string, error) {
	value := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, value); err != nil {
		return "", errors.New("create released engine authorization")
	}
	defer clear(value)
	return "Bearer " + base64.RawURLEncoding.EncodeToString(value), nil
}

func (scope *releasedProcessScope) Secret() (string, error) {
	value := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, value); err != nil {
		return "", errors.New("create released plugin secret")
	}
	defer clear(value)
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func (scope *releasedProcessScope) Close() error {
	if scope == nil || scope.root == "" {
		return nil
	}
	root := scope.root
	scope.root = ""
	scope.address = ""
	return os.RemoveAll(root)
}
