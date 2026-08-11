package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

type releasedWorkspace struct {
	path string
	root *os.Root
}

func newReleasedWorkspace(path string) (*releasedWorkspace, error) {
	if !filepath.IsAbs(path) {
		return nil, errors.New("released workspace path must be absolute")
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, fmt.Errorf("open released compatibility workspace: %w", err)
	}
	return &releasedWorkspace{path: path, root: root}, nil
}

func (workspace *releasedWorkspace) Path() string {
	if workspace == nil || workspace.root == nil {
		return ""
	}
	return workspace.path
}

func (workspace *releasedWorkspace) Close() error {
	if workspace == nil || workspace.root == nil {
		return nil
	}
	root := workspace.root
	path := workspace.path
	workspace.root = nil
	workspace.path = ""
	walkErr := fs.WalkDir(root.FS(), ".", func(relative string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		mode := fs.FileMode(0o600)
		if entry.IsDir() {
			mode = 0o700
		}
		if err := root.Chmod(relative, mode); err != nil { // #nosec G302 -- owned directories require search permission before deletion.
			return fmt.Errorf("reclaim released workspace entry: %w", err)
		}
		return nil
	})
	closeErr := root.Close()
	removeErr := os.RemoveAll(path)
	return errors.Join(walkErr, closeErr, removeErr)
}
