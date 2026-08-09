package stage_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/spice-framework/spice-agent/interaction"
	"github.com/spice-framework/spice-agent/stage"
)

type testRequesterFunc func(context.Context, interaction.Request) (interaction.Response, error)

func (requester testRequesterFunc) Request(
	ctx context.Context,
	request interaction.Request,
) (interaction.Response, error) {
	return requester(ctx, request)
}

func testToolDispatchScope(t *testing.T) stage.ToolDispatchScope {
	t.Helper()
	return testToolDispatchScopeForPlan(t, stage.PlanID("generation:test"))
}

func TestToolDispatchScopeValidatesEveryAuthorityFact(t *testing.T) {
	authority, _ := interaction.NewScope("run-test")
	valid := func(runID string, turn uint32, planID stage.PlanID, plan, workspace string, scope interaction.Scope) error {
		_, err := stage.NewToolDispatchScope(
			runID, turn, planID, plan, workspace, scope, interaction.UnavailableRequester{},
		)
		return err
	}
	digest := "sha256:" + strings.Repeat("a", 64)
	if err := valid("run-test", 2, "generation:test", digest, digest, authority); err != nil {
		t.Fatal(err)
	}
	otherAuthority, _ := interaction.NewScope("other-run")
	for name, err := range map[string]error{
		"run":         valid(" bad ", 2, "generation:test", digest, digest, authority),
		"turn":        valid("run-test", 0, "generation:test", digest, digest, authority),
		"plan id":     valid("run-test", 2, "", digest, digest, authority),
		"plan digest": valid("run-test", 2, "generation:test", "sha256:bad", digest, authority),
		"workspace":   valid("run-test", 2, "generation:test", digest, "SHA256:"+strings.Repeat("a", 64), authority),
		"authority":   valid("run-test", 2, "generation:test", digest, digest, otherAuthority),
	} {
		if err == nil {
			t.Errorf("%s authority succeeded", name)
		}
	}
	scope := testToolDispatchScope(t)
	if scope.RunID() != "run-test" || scope.Turn() != 1 || scope.ToolPlanID() != "generation:test" ||
		scope.InteractionAuthority().RunID() != "run-test" {
		t.Fatalf("scope accessors = %#v", scope)
	}
}

func TestToolDispatchScopeInteractionRequesterValidatesDelegatesAndContainsFailure(t *testing.T) {
	request, _ := interaction.NewRequest("approval", "confirm", "Continue?", json.RawMessage(`{}`))
	response, _ := interaction.NewResponse(request.ID(), json.RawMessage(`true`))
	var calls atomic.Uint32
	requester := testRequesterFunc(func(_ context.Context, got interaction.Request) (interaction.Response, error) {
		calls.Add(1)
		if got.ID() != request.ID() || got.Kind() != request.Kind() {
			t.Fatalf("request = %#v", got)
		}
		return response, nil
	})
	scope := testToolDispatchScopeWithRequester(t, stage.PlanID("generation:test"), requester)
	got, err := scope.RequestInteraction(t.Context(), request)
	if err != nil || got.ID() != response.ID() || calls.Load() != 1 {
		t.Fatalf("interaction response = %#v, calls=%d, error=%v", got, calls.Load(), err)
	}
	value := got.Value()
	value[0] = 'X'
	if string(got.Value()) != "true" {
		t.Fatal("interaction response was mutable")
	}

	if _, err = stage.NewToolDispatchScope(
		"run-test", 1, "generation:test", "sha256:"+strings.Repeat("0", 64), "",
		scope.InteractionAuthority(), nil,
	); err == nil {
		t.Fatal("nil interaction requester succeeded")
	}
	var nilContext context.Context
	if _, err = scope.RequestInteraction(nilContext, request); err == nil {
		t.Fatal("nil interaction context succeeded")
	}
	if _, err = scope.RequestInteraction(t.Context(), interaction.Request{}); err == nil {
		t.Fatal("invalid interaction request succeeded")
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err = scope.RequestInteraction(cancelled, request); !errors.Is(err, context.Canceled) || calls.Load() != 1 {
		t.Fatalf("cancelled interaction = %v, calls=%d", err, calls.Load())
	}

	panicScope := testToolDispatchScopeWithRequester(t, "generation:test", testRequesterFunc(func(context.Context, interaction.Request) (interaction.Response, error) {
		panic("SECRET requester panic")
	}))
	if _, err = panicScope.RequestInteraction(t.Context(), request); err == nil || strings.Contains(err.Error(), "SECRET") {
		t.Fatalf("requester panic = %v", err)
	}

	boundaryContext, boundaryCancel := context.WithCancel(t.Context())
	boundaryScope := testToolDispatchScopeWithRequester(t, "generation:test", testRequesterFunc(func(context.Context, interaction.Request) (interaction.Response, error) {
		boundaryCancel()
		return response, nil
	}))
	if _, err = boundaryScope.RequestInteraction(boundaryContext, request); !errors.Is(err, context.Canceled) {
		t.Fatalf("boundary cancellation = %v", err)
	}

	wrong, _ := interaction.NewResponse("wrong", json.RawMessage(`true`))
	wrongScope := testToolDispatchScopeWithRequester(t, "generation:test", testRequesterFunc(func(context.Context, interaction.Request) (interaction.Response, error) {
		return wrong, nil
	}))
	if _, err = wrongScope.RequestInteraction(t.Context(), request); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("mismatched response = %v", err)
	}
}

func testToolDispatchScopeForPlan(t *testing.T, planID stage.PlanID) stage.ToolDispatchScope {
	t.Helper()
	return testToolDispatchScopeWithRequester(t, planID, interaction.UnavailableRequester{})
}

func testToolDispatchScopeWithRequester(
	t *testing.T,
	planID stage.PlanID,
	requester interaction.Requester,
) stage.ToolDispatchScope {
	t.Helper()
	authority, err := interaction.NewScope("run-test")
	if err != nil {
		t.Fatal(err)
	}
	scope, err := stage.NewToolDispatchScope(
		"run-test", 1, planID, "sha256:"+strings.Repeat("0", 64), "", authority,
		requester,
	)
	if err != nil {
		t.Fatal(err)
	}
	return scope
}
