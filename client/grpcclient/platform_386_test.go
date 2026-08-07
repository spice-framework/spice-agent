//go:build 386

package grpcclient

import (
	"context"
	"errors"
	"math"
	"sync/atomic"
	"testing"

	"github.com/spice-framework/spice-agent/client"
	commonv1 "github.com/spice-framework/spice-agent/common/v1"
	enginev1 "github.com/spice-framework/spice-agent/engine/v1"
)

func TestInitializeRejectsPlatformUnrepresentableLimitsBeforeRPC(t *testing.T) {
	t.Parallel()
	version, err := client.NewProtocolVersion(
		commonv1.ProtocolMajor,
		commonv1.ProtocolMinor,
		commonv1.ProtocolPatch,
	)
	if err != nil {
		t.Fatal(err)
	}
	protocol, err := client.NewProtocolRange(version, version)
	if err != nil {
		t.Fatal(err)
	}
	build, err := client.NewBuild("platform-386-client", "test", "commit", "go1.26.5")
	if err != nil {
		t.Fatal(err)
	}
	limits, err := client.NewLimits(
		1<<20,
		math.MaxUint32,
		math.MaxUint32,
		math.MaxUint32,
		math.MaxUint32,
		math.MaxUint32,
	)
	if err != nil {
		t.Fatal(err)
	}
	request, err := client.NewInitializeRequest(protocol, build, nil, nil, limits)
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	connector := reconnectConnector(t, &unaryEngineClient{
		initialize: func(context.Context, *enginev1.InitializeRequest) (*enginev1.InitializeResponse, error) {
			calls.Add(1)
			return nil, errors.New("unrepresentable request reached RPC")
		},
	})

	session, initializeErr := connector.Initialize(t.Context(), request)
	statusErr, ok := errors.AsType[*client.StatusError](initializeErr)
	if session != nil || !ok || statusErr.Code() != client.ErrorInvalidArgument {
		t.Fatalf("initialize = %#v, %T %v, want local invalid argument", session, initializeErr, initializeErr)
	}
	if calls.Load() != 0 {
		t.Fatalf("initialize RPC calls = %d, want 0", calls.Load())
	}
}
