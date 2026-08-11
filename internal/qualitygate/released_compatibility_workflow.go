package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

type releasedCompatibilityWorkflow struct{}

func (releasedCompatibilityWorkflow) Validate(root string) error {
	content, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "released-compatibility.yml")) // #nosec G304 -- repository-owned workflow path.
	if err != nil {
		return errors.New("released compatibility workflow is missing")
	}
	workflow := strings.ReplaceAll(string(content), "\r\n", "\n")
	want := (releasedCompatibilityWorkflow{}).Expected()
	if workflow != want {
		return errors.New("released compatibility workflow differs from the reviewed Linux/Windows contract")
	}
	return nil
}

func (releasedCompatibilityWorkflow) Expected() string {
	return `name: Released compatibility
on:
  push: {branches: [main]}
  pull_request: {branches: [main]}
permissions: {contents: read}
env:
  GOWORK: off
  GOTOOLCHAIN: local
jobs:
  linux:
    runs-on: ubuntu-latest
    timeout-minutes: 30
    steps:
      - uses: actions/checkout@d23441a48e516b6c34aea4fa41551a30e30af803
        with: {persist-credentials: false}
      - uses: actions/setup-go@924ae3a1cded613372ab5595356fb5720e22ba16
        with: {go-version: 1.26.5, cache: false}
      - run: go run -mod=vendor ./internal/qualitygate -mode=released-compatibility
  windows:
    runs-on: windows-latest
    timeout-minutes: 30
    steps:
      - uses: actions/checkout@d23441a48e516b6c34aea4fa41551a30e30af803
        with: {persist-credentials: false}
      - uses: actions/setup-go@924ae3a1cded613372ab5595356fb5720e22ba16
        with: {go-version: 1.26.5, cache: false}
      - run: go run -mod=vendor ./internal/qualitygate -mode=released-compatibility
  required:
    if: always()
    needs: [linux, windows]
    runs-on: ubuntu-latest
    steps:
      - run: test "${{ needs.linux.result }}" = success && test "${{ needs.windows.result }}" = success
`
}
