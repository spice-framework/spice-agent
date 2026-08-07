//go:build windows

package localipc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

var pipeFixtureSequence atomic.Uint64

func TestWindowsListenerIsCurrentUserOnlyAndConnectable(t *testing.T) {
	t.Parallel()
	address := windowsPipeFixture(t)
	descriptor, err := currentUserSecurityDescriptor()
	if err != nil {
		t.Fatal(err)
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(descriptor, "D:P") || !strings.Contains(descriptor, user.User.Sid.String()) {
		t.Fatalf("current-user descriptor = %q", descriptor)
	}
	if _, err = winio.SddlToSecurityDescriptor(descriptor); err != nil {
		t.Fatalf("parse current-user descriptor: %v", err)
	}

	listener, err := Listen(address)
	if err != nil {
		t.Fatal(err)
	}
	accepted := make(chan error, 1)
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			accepted <- acceptErr
			return
		}
		defer func() { _ = connection.Close() }()
		buffer := make([]byte, 4)
		_, acceptErr = io.ReadFull(connection, buffer)
		if acceptErr == nil {
			_, acceptErr = connection.Write(buffer)
		}
		accepted <- acceptErr
	}()
	connection, err := Dial(t.Context(), address)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = connection.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	response := make([]byte, 4)
	if _, err = io.ReadFull(connection, response); err != nil || string(response) != "ping" {
		t.Fatalf("echo response = %q, %v", response, err)
	}
	_ = connection.Close()
	if err = <-accepted; err != nil {
		t.Fatal(err)
	}
	if err = listener.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestWindowsEndpointRejectsRemoteAndAmbiguousNames(t *testing.T) {
	t.Parallel()
	for _, address := range []string{
		"", `\\server\pipe\agent`, `\\localhost\pipe\agent`, `\\.\pipe\`,
		`\\.\pipe\agent`, `\\.\pipe\spice-agent-`, `\\.\pipe\spice-agent-nested\agent`,
		`\\.\pipe\..`, `\\?\pipe\agent`, ` \\.\pipe\spice-agent-test`,
	} {
		if listener, err := Listen(address); !errors.Is(err, ErrUnsafeEndpoint) || listener != nil {
			t.Fatalf("unsafe listener %q = %#v, %v", address, listener, err)
		}
		if connection, err := Dial(t.Context(), address); !errors.Is(err, ErrUnsafeEndpoint) || connection != nil {
			t.Fatalf("unsafe dial %q = %#v, %v", address, connection, err)
		}
	}
}

func TestWindowsAddressValidationBoundary(t *testing.T) {
	t.Parallel()
	maximumSuffix := strings.Repeat("a", maximumWindowsPipeNameLength-len(windowsSpicePipePrefix))
	if err := validateWindowsAddress(windowsPipePrefix + windowsSpicePipePrefix + maximumSuffix); err != nil {
		t.Fatalf("maximum compatible Windows pipe name: %v", err)
	}
	if err := validateWindowsAddress(windowsPipePrefix + windowsSpicePipePrefix + maximumSuffix + "a"); err == nil {
		t.Fatal("accepted a Windows pipe name beyond the metadata boundary")
	}
}

func TestWindowsDialDeadlineAndListenerCleanup(t *testing.T) {
	t.Parallel()
	address := windowsPipeFixture(t)
	listener, err := Listen(address)
	if err != nil {
		t.Fatal(err)
	}
	if duplicate, duplicateErr := Listen(address); duplicateErr == nil || duplicate != nil {
		t.Fatalf("duplicate pipe listener = %#v, %v", duplicate, duplicateErr)
	}
	// Listen reserves the name, but go-winio creates a connectable instance
	// only when Accept begins. Dial must honor its deadline while the reserved
	// local pipe is deliberately busy.
	deadline, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	if connection, dialErr := Dial(deadline, address); !errors.Is(dialErr, context.DeadlineExceeded) || connection != nil {
		t.Fatalf("busy pipe dial = %#v, %v", connection, dialErr)
	}
	if err = listener.Close(); err != nil {
		t.Fatal(err)
	}
	cleanupContext, cancelCleanup := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancelCleanup()
	if connection, dialErr := Dial(cleanupContext, address); dialErr == nil || connection != nil {
		t.Fatalf("closed pipe dial = %#v, %v", connection, dialErr)
	}

	canceled, cancelDial := context.WithCancel(t.Context())
	cancelDial()
	if connection, dialErr := Dial(canceled, address); !errors.Is(dialErr, context.Canceled) || connection != nil {
		t.Fatalf("canceled dial = %#v, %v", connection, dialErr)
	}
	var missingContext context.Context
	if connection, dialErr := Dial(missingContext, address); dialErr == nil || connection != nil {
		t.Fatalf("nil-context dial = %#v, %v", connection, dialErr)
	}
}

func windowsPipeFixture(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf(
		`\\.\pipe\spice-agent-localipc-%d-%d`, os.Getpid(), pipeFixtureSequence.Add(1),
	)
}
