package client

import (
	"context"
	"errors"
	"testing"
)

func TestSessionAndStreamsPreserveCallerCancellation(t *testing.T) {
	t.Parallel()
	cause := errors.New("caller stopped")
	ctx, cancel := context.WithCancelCause(context.Background())
	cancel(cause)
	session := cancellationSession{}
	input, _ := NewInput("message", "hello")
	start, _ := NewStartRequest(mustOperation(t, "start"), mustDefinitionRef(t, "agent", "v1"), input)
	if _, err := session.Start(ctx, start); !errors.Is(err, cause) {
		t.Fatalf("start error = %v", err)
	}
	if _, err := session.Health(ctx); !errors.Is(err, cause) {
		t.Fatalf("health error = %v", err)
	}
	if _, err := (cancellationEventStream{}).Next(ctx); !errors.Is(err, cause) {
		t.Fatalf("event stream error = %v", err)
	}
	if _, err := (cancellationInteractionStream{}).Next(ctx); !errors.Is(err, cause) {
		t.Fatalf("interaction stream error = %v", err)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	if err := session.Close(); err != nil {
		t.Fatalf("idempotent close = %v", err)
	}

	var _ Session = session
	var _ EventStream = cancellationEventStream{}
	var _ InteractionStream = cancellationInteractionStream{}
}

type cancellationSession struct{}

func (cancellationSession) Connection() Connection { return Connection{} }
func (cancellationSession) Start(ctx context.Context, _ StartRequest) (StartResult, error) {
	return StartResult{}, context.Cause(ctx)
}

func (cancellationSession) Events(ctx context.Context, _ Cursor, _ EventStreamOptions) (EventStream, error) {
	return nil, context.Cause(ctx)
}

func (cancellationSession) Interactions(ctx context.Context, _ InteractionStreamOptions) (InteractionStream, error) {
	return nil, context.Cause(ctx)
}

func (cancellationSession) Cancel(ctx context.Context, _ CancelRequest) (CancelResult, error) {
	return CancelResult{}, context.Cause(ctx)
}

func (cancellationSession) Respond(ctx context.Context, _ RespondRequest) (RespondResult, error) {
	return RespondResult{}, context.Cause(ctx)
}

func (cancellationSession) Suspend(ctx context.Context, _ RunMutation) (SuspendResult, error) {
	return SuspendResult{}, context.Cause(ctx)
}

func (cancellationSession) Resume(ctx context.Context, _ RunMutation) (ResumeResult, error) {
	return ResumeResult{}, context.Cause(ctx)
}

func (cancellationSession) Export(ctx context.Context, _ RunRef) (Snapshot, error) {
	return Snapshot{}, context.Cause(ctx)
}

func (cancellationSession) Import(ctx context.Context, _ ImportRequest) (ImportResult, error) {
	return ImportResult{}, context.Cause(ctx)
}

func (cancellationSession) Health(ctx context.Context) (Health, error) {
	return Health{}, context.Cause(ctx)
}
func (cancellationSession) Close() error { return nil }

type cancellationEventStream struct{}

func (cancellationEventStream) Next(ctx context.Context) (EventFrame, error) {
	return EventFrame{}, context.Cause(ctx)
}
func (cancellationEventStream) Close() error { return nil }

type cancellationInteractionStream struct{}

func (cancellationInteractionStream) Next(ctx context.Context) (InteractionFrame, error) {
	return InteractionFrame{}, context.Cause(ctx)
}
func (cancellationInteractionStream) Close() error { return nil }
