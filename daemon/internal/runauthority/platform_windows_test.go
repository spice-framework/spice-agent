//go:build windows

package runauthority

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	enginev1 "github.com/spice-framework/spice-agent/engine/v1"
	"golang.org/x/sys/windows"
)

func TestWindowsRejectsIntermediateReparsePoint(t *testing.T) {
	realDirectory := filepath.Join(authorityTestRoot(t), "real")
	if err := os.Mkdir(realDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(authorityTestRoot(t), "link")
	if err := os.Symlink(realDirectory, link); err != nil {
		t.Skipf("creating Windows test reparse point requires developer mode or privilege: %v", err)
	}
	if _, err := Open(Config{Directory: filepath.Join(link, "authority")}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("reparse path = %v", err)
	}
}

func TestWindowsRejectsHardLinkedAuthorityFile(t *testing.T) {
	directory := filepath.Join(authorityTestRoot(t), "authority")
	store, err := Open(Config{Directory: directory})
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(filepath.Join(directory, "identity.key"), filepath.Join(directory, "identity-copy.key")); err != nil {
		t.Skipf("hard links unavailable: %v", err)
	}
	if _, err := Open(Config{Directory: directory}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("hard-linked identity = %v", err)
	}
}

func TestWindowsHandleValidationIsBoundToExpectedPath(t *testing.T) {
	directory := filepath.Join(authorityTestRoot(t), "authority")
	store, err := Open(Config{Directory: directory})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "identity.key")
	file, err := openWindowsFile(path, windows.GENERIC_READ, windows.OPEN_EXISTING)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	if err = validateWindowsHandle(windows.Handle(file.Fd()), filepath.Join(directory, "other.key")); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("mismatched opened path = %v", err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestWindowsAncestryValidationIsBoundToRetainedDirectory(t *testing.T) {
	root := authorityTestRoot(t)
	first, err := Open(Config{Directory: filepath.Join(root, "first")})
	if err != nil {
		t.Fatal(err)
	}
	second, err := Open(Config{Directory: filepath.Join(root, "second")})
	if err != nil {
		t.Fatal(err)
	}
	if err = validateWindowsAncestry(second.directoryPath, first.directory.handle); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("mismatched ancestry identity = %v", err)
	}
	if err = second.Close(); err != nil {
		t.Fatal(err)
	}
	if err = first.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestWindowsAncestryIncludesVolumeRoot(t *testing.T) {
	path := filepath.Join(authorityTestRoot(t), "authority")
	paths, err := windowsAncestryPaths(path)
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.VolumeName(path) + string(filepath.Separator)
	if len(paths) < 2 || !filepath.IsAbs(paths[0]) || !strings.EqualFold(paths[0], root) || paths[len(paths)-1] != path {
		t.Fatalf("ancestry paths = %v", paths)
	}
}

func TestWindowsBoundDirectorySurvivesLeafAndAncestorSubstitution(t *testing.T) {
	for _, test := range []struct {
		name string
		move func(string, string, string) (string, error)
	}{
		{
			name: "leaf",
			move: func(root, ancestor, directory string) (string, error) {
				moved := filepath.Join(ancestor, "authority-original")
				if err := os.Rename(directory, moved); err != nil {
					return "", err
				}
				return moved, os.Mkdir(directory, 0o700)
			},
		},
		{
			name: "writable ancestor",
			move: func(root, ancestor, directory string) (string, error) {
				moved := filepath.Join(root, "writable-original")
				if err := os.Rename(ancestor, moved); err != nil {
					return "", err
				}
				if err := os.Mkdir(ancestor, 0o700); err != nil {
					return "", err
				}
				return filepath.Join(moved, filepath.Base(directory)), os.Mkdir(directory, 0o700)
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := authorityTestRoot(t)
			ancestor := filepath.Join(root, "writable")
			directory := filepath.Join(ancestor, "authority")
			store := openTestStore(t, directory)
			active, err := store.Start(t.Context(), "bound-run")
			if err != nil {
				t.Fatal(err)
			}
			snapshot := internalSnapshot(t, active, "bound-run", enginev1.SnapshotLifecycle_SNAPSHOT_LIFECYCLE_SUSPENDED)
			if err = active.Close(); err != nil {
				t.Fatal(err)
			}
			original, err := test.move(root, ancestor, directory)
			if err != nil {
				if test.name == "writable ancestor" && errors.Is(err, os.ErrPermission) {
					t.Skipf("this Windows filesystem prevents ancestor rename with an open descendant: %v", err)
				}
				t.Fatalf("substitute path: %v", err)
			}
			transaction, err := store.PrepareImport(t.Context(), snapshot)
			if err != nil {
				t.Fatalf("prepare through retained handle: %v", err)
			}
			if err = transaction.Consume(t.Context()); err != nil {
				t.Fatalf("consume through retained handle: %v", err)
			}
			if err = transaction.Abort(); !errors.Is(err, ErrUncertain) {
				t.Fatalf("abort consumed import = %v", err)
			}
			value, err := store.readRecord("bound-run")
			if err != nil || value.Phase != PhaseImporting {
				t.Fatalf("bound record = %#v, %v", value, err)
			}
			if _, err = os.Stat(onlyStatePath(t, original)); err != nil {
				t.Fatalf("original authority state: %v", err)
			}
			entries, err := os.ReadDir(directory)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 0 {
				t.Fatalf("substituted directory was modified: %v", entries)
			}
		})
	}
}

func onlyStatePath(t *testing.T, directory string) string {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) == ".state" {
			return filepath.Join(directory, entry.Name())
		}
	}
	t.Fatalf("no state file in %s", directory)
	return ""
}

func TestWindowsReopenRejectsRollbackThroughDeleteChildAncestor(t *testing.T) {
	root := authorityTestRoot(t)
	ancestor := filepath.Join(root, "ancestor")
	if err := os.Mkdir(ancestor, 0o700); err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(ancestor, "authority")
	store, err := Open(Config{Directory: directory})
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	if err = grantWorldDeleteChild(ancestor); err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(directory, filepath.Join(ancestor, "authority-rollback")); err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err = Open(Config{Directory: directory}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("reopen through delete-child ancestor = %v", err)
	}
}

func grantWorldDeleteChild(path string) error {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return err
	}
	descriptor, err := windows.SecurityDescriptorFromString(
		"O:" + user.User.Sid.String() + "D:P(A;;GA;;;" + user.User.Sid.String() + ")(A;;DC;;;WD)",
	)
	if err != nil {
		return err
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		return err
	}
	err = windows.SetNamedSecurityInfo(
		path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, dacl, nil,
	)
	runtime.KeepAlive(descriptor)
	return err
}
