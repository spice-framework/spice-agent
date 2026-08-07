package localendpoint

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spice-framework/spice-agent/daemon/localipc"
)

func TestParseLaunchIdentityRequiresCanonicalNonzeroValue(t *testing.T) {
	valid := identityFor(t, "valid")
	tests := map[string]string{
		"empty":       "",
		"short":       valid[:len(valid)-1],
		"long":        valid + "0",
		"uppercase":   strings.ToUpper(valid),
		"punctuation": strings.Repeat("g", len(valid)),
		"zero":        strings.Repeat("0", len(valid)),
	}
	if _, err := parseLaunchIdentity(valid); err != nil {
		t.Fatalf("parse valid launch identity: %v", err)
	}
	for name, input := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := parseLaunchIdentity(input)
			if !errors.Is(err, ErrInvalidLaunchIdentity) {
				t.Fatalf("parseLaunchIdentity() error = %v, want ErrInvalidLaunchIdentity", err)
			}
			if err != nil && strings.Contains(err.Error(), input) && input != "" {
				t.Fatal("validation error disclosed the launch identity")
			}
		})
	}
}

func TestFactoryOpenIsDeterministicUniqueAndDoesNotListen(t *testing.T) {
	factory := NewFactory()
	firstIdentity := identityFor(t, "first")
	secondIdentity := identityFor(t, "second")
	first, err := factory.Open(context.Background(), firstIdentity)
	if err != nil {
		t.Fatalf("Open(first): %v", err)
	}
	defer func() {
		if closeErr := first.Close(); closeErr != nil {
			t.Errorf("Close(first): %v", closeErr)
		}
	}()
	repeated, err := factory.Open(context.Background(), firstIdentity)
	if err != nil {
		t.Fatalf("Open(repeated): %v", err)
	}
	defer func() { _ = repeated.Close() }()
	second, err := factory.Open(context.Background(), secondIdentity)
	if err != nil {
		t.Fatalf("Open(second): %v", err)
	}
	defer func() { _ = second.Close() }()
	if first.Address() == "" || first.Address() != repeated.Address() {
		t.Fatal("equal launch identities did not derive one stable nonempty address")
	}
	if first.Address() == second.Address() {
		t.Fatal("different launch identities derived the same address")
	}

	dialContext, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	connection, err := first.Dial(dialContext)
	if connection != nil {
		_ = connection.Close()
		t.Fatal("Open unexpectedly created a listener")
	}
	if err == nil {
		t.Fatal("Dial succeeded before the plugin created its listener")
	}
	assertRedacted(t, err.Error(), first.Address(), firstIdentity)
}

func TestFactoryOpenHonorsContextAndRedactsInvalidInput(t *testing.T) {
	factory := NewFactory()
	//nolint:staticcheck // Deliberately verifies the public nil-context boundary.
	if endpoint, err := factory.Open(nil, identityFor(t, "nil")); err == nil || endpoint != nil {
		t.Fatalf("Open(nil) = (%v, %v), want nil endpoint and error", endpoint, err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if endpoint, err := factory.Open(canceled, identityFor(t, "canceled")); !errors.Is(err, context.Canceled) || endpoint != nil {
		t.Fatalf("Open(canceled) = (%v, %v), want context.Canceled", endpoint, err)
	}
	sensitive := "0123456789abcdef0123456789abcdeG"
	if endpoint, err := factory.Open(context.Background(), sensitive); err == nil || endpoint != nil {
		t.Fatalf("Open(invalid) = (%v, %v), want nil endpoint and error", endpoint, err)
	} else {
		assertRedacted(t, err.Error(), sensitive)
	}
}

func TestEndpointFormattingAndSerializationAreRedacted(t *testing.T) {
	identity := identityFor(t, "format")
	owned, err := NewFactory().Open(context.Background(), identity)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = owned.Close() }()
	encoded, err := json.Marshal(owned)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	values := []string{
		fmt.Sprint(owned),
		fmt.Sprintf("%s", owned),
		fmt.Sprintf("%q", owned),
		fmt.Sprintf("%v", owned),
		fmt.Sprintf("%+v", owned),
		fmt.Sprintf("%#v", owned),
		string(encoded),
	}
	for _, value := range values {
		if !strings.Contains(value, "REDACTED") {
			t.Fatalf("format %q is not visibly redacted", value)
		}
		assertRedacted(t, value, owned.Address(), identity)
	}
	concrete, ok := owned.(*ownedEndpoint)
	if !ok || !strings.Contains(concrete.String(), "REDACTED") ||
		!strings.Contains(concrete.GoString(), "REDACTED") {
		t.Fatal("direct endpoint formatting is not visibly redacted")
	}
}

func TestEndpointNilSafetyAndSafeErrorClassification(t *testing.T) {
	t.Parallel()
	var absent *ownedEndpoint
	if absent.Address() != "" || absent.Close() != nil {
		t.Fatal("nil endpoint is not inert")
	}
	if connection, err := absent.Dial(context.Background()); connection != nil || !errors.Is(err, ErrClosed) {
		t.Fatalf("nil endpoint Dial = (%v, %v), want ErrClosed", connection, err)
	}
	if connection, err := absent.Dial(nil); connection != nil || err == nil { //nolint:staticcheck // Deliberate nil boundary.
		t.Fatalf("nil-context Dial = (%v, %v), want error", connection, err)
	}

	tests := []struct {
		cause error
		want  error
	}{
		{context.Canceled, context.Canceled},
		{context.DeadlineExceeded, context.DeadlineExceeded},
		{localipc.ErrUnsafeEndpoint, localipc.ErrUnsafeEndpoint},
		{localipc.ErrEndpointInUse, localipc.ErrEndpointInUse},
		{errors.New("private operating-system detail"), ErrUnavailable},
	}
	for _, test := range tests {
		err := safeOperationError("test local endpoint", ErrUnavailable, test.cause)
		if !errors.Is(err, test.want) || strings.Contains(err.Error(), "private operating-system detail") {
			t.Fatalf("safeOperationError(%v) = %v, want safe %v", test.cause, err, test.want)
		}
	}
	if safeOperationError("test", ErrUnavailable, nil) != nil {
		t.Fatal("nil operation failure produced an error")
	}
}

func TestEndpointDialHonorsCallerCancellation(t *testing.T) {
	owned, err := NewFactory().Open(context.Background(), identityFor(t, "dial-canceled"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = owned.Close() }()
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	connection, err := owned.Dial(canceled)
	if connection != nil {
		_ = connection.Close()
		t.Fatal("Dial returned a connection for a canceled context")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Dial error = %v, want context.Canceled", err)
	}
}

func TestEndpointDialUsesExactLocalAddress(t *testing.T) {
	owned, err := NewFactory().Open(context.Background(), identityFor(t, "real-dial"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	listener, err := localipc.Listen(owned.Address())
	if err != nil {
		_ = owned.Close()
		t.Fatalf("Listen: %v", err)
	}
	serverDone := make(chan error, 1)
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			serverDone <- acceptErr
			return
		}
		defer func() { _ = connection.Close() }()
		buffer := make([]byte, 4)
		if _, acceptErr = io.ReadFull(connection, buffer); acceptErr == nil && string(buffer) != "ping" {
			acceptErr = errors.New("server received an unexpected request")
		}
		if acceptErr == nil {
			_, acceptErr = connection.Write([]byte("pong"))
		}
		serverDone <- acceptErr
	}()

	dialContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	connection, err := owned.Dial(dialContext)
	if err != nil {
		_ = listener.Close()
		_ = owned.Close()
		t.Fatalf("Dial: %v", err)
	}
	if _, err = connection.Write([]byte("ping")); err != nil {
		t.Errorf("write request: %v", err)
	}
	response := make([]byte, 4)
	if _, err = io.ReadFull(connection, response); err != nil {
		t.Errorf("read response: %v", err)
	} else if string(response) != "pong" {
		t.Errorf("response = %q, want pong", response)
	}
	_ = connection.Close()
	if err = <-serverDone; err != nil {
		t.Errorf("server: %v", err)
	}
	if err = listener.Close(); err != nil {
		t.Errorf("listener Close: %v", err)
	}
	if err = owned.Close(); err != nil {
		t.Errorf("endpoint Close: %v", err)
	}
	if connection, err = owned.Dial(context.Background()); !errors.Is(err, ErrClosed) || connection != nil {
		t.Fatalf("Dial after Close = (%v, %v), want ErrClosed", connection, err)
	}
}

func TestEndpointCloseIsConcurrentAndIdempotent(t *testing.T) {
	owned, err := NewFactory().Open(context.Background(), identityFor(t, "concurrent-close"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	const callers = 32
	errorsByCaller := make([]error, callers)
	var wait sync.WaitGroup
	wait.Add(callers)
	for index := range callers {
		go func() {
			defer wait.Done()
			errorsByCaller[index] = owned.Close()
		}()
	}
	wait.Wait()
	for index, closeErr := range errorsByCaller {
		if closeErr != nil {
			t.Errorf("Close caller %d: %v", index, closeErr)
		}
	}
	if err = owned.Close(); err != nil {
		t.Fatalf("repeated Close: %v", err)
	}
}

func identityFor(t *testing.T, discriminator string) string {
	t.Helper()
	digest := sha256.Sum256([]byte(t.Name() + "/" + discriminator))
	return hex.EncodeToString(digest[:16])
}

func assertRedacted(t *testing.T, value string, secrets ...string) {
	t.Helper()
	for _, secret := range secrets {
		if secret != "" && strings.Contains(value, secret) {
			t.Fatalf("value %q disclosed sensitive endpoint data", value)
		}
	}
}
