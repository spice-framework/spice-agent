package localclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/spice-framework/spice-agent/client"
	"github.com/spice-framework/spice-agent/daemon/endpoint"
)

func TestConnectorIsLazyConcurrentAndSharesOneOwnedChannel(t *testing.T) {
	t.Parallel()
	metadata := currentMetadataFixture(t)
	connection := connectionFixture(t, metadata)
	var opened, initialized, closed atomic.Int32
	connector, err := newConnector(metadata, func(got endpoint.Metadata) (openedConnector, error) {
		if got.Address() != metadata.Address() || !got.Token().Equal(metadata.Token()) {
			t.Error("opener received changed endpoint metadata")
		}
		opened.Add(1)
		return openedConnector{
			connector: fixtureConnector{initialize: func(
				context.Context,
				client.InitializeRequest,
			) (client.Session, error) {
				initialized.Add(1)
				return &fixtureSession{connection: connection}, nil
			}},
			close: func() error { closed.Add(1); return nil },
		}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if opened.Load() != 0 {
		t.Fatal("New performed I/O")
	}
	request := initializeRequestFixture(t, metadata)
	const count = 16
	var wait sync.WaitGroup
	wait.Add(count)
	for range count {
		go func() {
			defer wait.Done()
			session, initializeErr := connector.Initialize(t.Context(), request)
			if initializeErr != nil {
				t.Error(initializeErr)
				return
			}
			if closeErr := session.Close(); closeErr != nil {
				t.Error(closeErr)
			}
			if closeErr := session.Close(); closeErr != nil {
				t.Error(closeErr)
			}
		}()
	}
	wait.Wait()
	if opened.Load() != 1 || initialized.Load() != count || closed.Load() != 0 {
		t.Fatalf("open/initialize/close = %d/%d/%d", opened.Load(), initialized.Load(), closed.Load())
	}
	if err = connector.Close(); err != nil {
		t.Fatal(err)
	}
	if err = connector.Close(); err != nil || closed.Load() != 1 {
		t.Fatalf("idempotent connector close = %v, count %d", err, closed.Load())
	}
}

func TestInitializeRejectsInvalidStateAndPreservesCleanupFailures(t *testing.T) {
	t.Parallel()
	metadata := currentMetadataFixture(t)
	request := initializeRequestFixture(t, metadata)
	if _, err := (*Connector)(nil).Initialize(t.Context(), request); err == nil {
		t.Fatal("nil Connector initialized")
	}
	connector, err := newConnector(metadata, func(endpoint.Metadata) (openedConnector, error) {
		return openedConnector{}, errFixture
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = initializeWithNilContext(connector, request); err == nil {
		t.Fatal("nil context initialized")
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err = connector.Initialize(cancelled, request); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-cancelled error = %v", err)
	}
	if _, err = connector.Initialize(t.Context(), request); !errors.Is(err, errFixture) {
		t.Fatalf("open error = %v", err)
	}

	closeFailure := errors.New("close channel")
	initializeFailure := errors.New("initialize")
	connector, err = newConnector(metadata, func(endpoint.Metadata) (openedConnector, error) {
		return openedConnector{
			connector: fixtureConnector{initialize: func(context.Context, client.InitializeRequest) (client.Session, error) {
				return nil, initializeFailure
			}},
			close: func() error { return closeFailure },
		}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = connector.Initialize(t.Context(), request); !errors.Is(err, initializeFailure) {
		t.Fatalf("initialize error = %v", err)
	}
	if err = connector.Close(); !errors.Is(err, closeFailure) {
		t.Fatalf("shared channel cleanup error = %v", err)
	}

	connector, err = newConnector(metadata, func(endpoint.Metadata) (openedConnector, error) {
		return openedConnector{close: func() error { return closeFailure }}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = connector.Initialize(t.Context(), request); !errors.Is(err, closeFailure) {
		t.Fatalf("incomplete ownership error = %v", err)
	}
}

func TestInitializeRejectsNilSessionIdentityMismatchAndLateCancellation(t *testing.T) {
	t.Parallel()
	metadata := currentMetadataFixture(t)
	request := initializeRequestFixture(t, metadata)
	var closes atomic.Int32
	var connectors []*Connector
	makeConnector := func(initialize func(context.Context) (client.Session, error)) *Connector {
		result, err := newConnector(metadata, func(endpoint.Metadata) (openedConnector, error) {
			return openedConnector{
				connector: fixtureConnector{initialize: func(ctx context.Context, _ client.InitializeRequest) (client.Session, error) {
					return initialize(ctx)
				}},
				close: func() error { closes.Add(1); return nil },
			}, nil
		})
		if err != nil {
			t.Fatal(err)
		}
		connectors = append(connectors, result)
		return result
	}
	if _, err := makeConnector(func(context.Context) (client.Session, error) {
		return nil, nil //nolint:nilnil // corrupt dependency result is the boundary under test.
	}).Initialize(t.Context(), request); err == nil {
		t.Fatal("nil session accepted")
	}
	wrong := endpointFixture(t, metadata.Address(), metadata.Transport())
	wrongBuild, buildErr := client.NewBuild("wrong-daemon", "test", "commit", "go1.26.5")
	if buildErr != nil {
		t.Fatal(buildErr)
	}
	wrong, buildErr = endpoint.NewMetadata(
		wrong.Transport(), wrong.Address(), wrong.Token(), wrongBuild, wrong.Protocol(), wrong.Process(),
	)
	if buildErr != nil {
		t.Fatal(buildErr)
	}
	if _, err := makeConnector(func(context.Context) (client.Session, error) {
		return &fixtureSession{connection: connectionFixture(t, wrong)}, nil
	}).Initialize(t.Context(), request); err == nil || !strings.Contains(err.Error(), "server identity") {
		t.Fatalf("identity mismatch error = %v", err)
	}
	late, cancel := context.WithCancel(t.Context())
	if _, err := makeConnector(func(context.Context) (client.Session, error) {
		cancel()
		return &fixtureSession{connection: connectionFixture(t, metadata)}, nil
	}).Initialize(late, request); !errors.Is(err, context.Canceled) {
		t.Fatalf("late cancellation error = %v", err)
	}
	for _, connector := range connectors {
		_ = connector.Close()
	}
	if closes.Load() != 3 {
		t.Fatalf("channel closes = %d, want 3", closes.Load())
	}
}

func initializeWithNilContext(
	connector *Connector,
	request client.InitializeRequest,
) (client.Session, error) {
	return connector.Initialize(nil, request) //nolint:staticcheck // deliberate nil API-boundary regression.
}

func TestInitializeValidatesRequestBeforeOpeningAndAcceptsOlderCompatibleProtocol(t *testing.T) {
	t.Parallel()
	metadata := currentMetadataFixture(t)
	var opened atomic.Int32
	connector, err := newConnector(metadata, func(endpoint.Metadata) (openedConnector, error) {
		opened.Add(1)
		older, versionErr := client.NewProtocolVersion(
			metadata.Protocol().Major(), metadata.Protocol().Minor()-1, metadata.Protocol().Patch(),
		)
		if versionErr != nil {
			return openedConnector{}, versionErr
		}
		olderMetadata, metadataErr := endpoint.NewMetadata(
			metadata.Transport(), metadata.Address(), metadata.Token(), metadata.Server(), older, metadata.Process(),
		)
		if metadataErr != nil {
			return openedConnector{}, metadataErr
		}
		return openedConnector{
			connector: fixtureConnector{initialize: func(context.Context, client.InitializeRequest) (client.Session, error) {
				return &fixtureSession{connection: connectionFixture(t, olderMetadata)}, nil
			}},
			close: func() error { return nil },
		}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = connector.Initialize(t.Context(), client.InitializeRequest{}); err == nil {
		t.Fatal("invalid request accepted")
	}
	if opened.Load() != 0 {
		t.Fatalf("invalid request opened %d channels", opened.Load())
	}
	request := initializeRequestFixture(t, metadata)
	session, err := connector.Initialize(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if session.Connection().Protocol().Minor() != metadata.Protocol().Minor()-1 {
		t.Fatalf("selected protocol = %d", session.Connection().Protocol().Minor())
	}
	if err = session.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestConnectorCloseAggregatesSessionAndTransportFailures(t *testing.T) {
	t.Parallel()
	sessionFailure := errors.New("session close")
	transportFailure := errors.New("transport close")
	var sessionCloses, transportCloses atomic.Int32
	owner := &Connector{
		sessions: make(map[*ownedSession]struct{}),
		opened:   &openedConnector{close: func() error { transportCloses.Add(1); return transportFailure }},
	}
	session := &ownedSession{
		Session: &fixtureSession{close: func() error { sessionCloses.Add(1); return sessionFailure }}, owner: owner,
	}
	owner.sessions[session] = struct{}{}
	for range 8 {
		if err := owner.Close(); !errors.Is(err, sessionFailure) || !errors.Is(err, transportFailure) {
			t.Fatalf("Close error = %v", err)
		}
	}
	if sessionCloses.Load() != 1 || transportCloses.Load() != 1 {
		t.Fatalf("cleanup counts = %d/%d", sessionCloses.Load(), transportCloses.Load())
	}
	if err := (*ownedSession)(nil).Close(); err != nil {
		t.Fatal(err)
	}
	if err := (*Connector)(nil).Close(); err != nil {
		t.Fatal(err)
	}
}

func TestFormattingNeverLeaksEndpointMaterial(t *testing.T) {
	t.Parallel()
	metadata := currentMetadataFixture(t)
	connector, err := New(metadata)
	if err != nil {
		t.Fatal(err)
	}
	session := &ownedSession{}
	authorization, err := metadata.Token().AuthorizationValue()
	if err != nil {
		t.Fatal(err)
	}
	secrets := []string{metadata.Address(), authorization, strings.TrimPrefix(authorization, endpoint.BearerPrefix)}
	for _, value := range []any{connector, session} {
		formatted := []string{
			fmt.Sprintf("%v", value), fmt.Sprintf("%+v", value), fmt.Sprintf("%#v", value),
			fmt.Sprintf("%s", value), fmt.Sprintf("%q", value), fmt.Sprintf("%x", value),
		}
		encoded, marshalErr := json.Marshal(value)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		formatted = append(formatted, string(encoded))
		var output bytes.Buffer
		slog.New(slog.NewJSONHandler(&output, nil)).Info("value", "adapter", value)
		formatted = append(formatted, output.String())
		for _, candidate := range formatted {
			for _, secret := range secrets {
				if strings.Contains(candidate, secret) {
					t.Fatalf("secret %q leaked in %q", secret, candidate)
				}
			}
		}
	}
}

func TestNewRejectsInvalidMetadataAndNilOpener(t *testing.T) {
	t.Parallel()
	if _, err := New(endpoint.Metadata{}); err == nil {
		t.Fatal("zero metadata accepted")
	}
	if _, err := newConnector(currentMetadataFixture(t), nil); err == nil {
		t.Fatal("nil opener accepted")
	}
}
