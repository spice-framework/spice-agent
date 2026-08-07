package process_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/spice-framework/spice-agent/process"
)

func TestOutcomeContracts(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		outcome    process.Outcome
		kind       process.OutcomeKind
		successful bool
		code       int64
		hasCode    bool
	}{
		{"success", mustExited(t, 0), process.OutcomeExited, true, 0, true},
		{"failure", mustExited(t, process.MaximumExitCode), process.OutcomeExited, false, process.MaximumExitCode, true},
		{"signaled", process.NewSignaledOutcome(), process.OutcomeSignaled, false, 0, false},
		{"unknown", process.NewUnknownOutcome(), process.OutcomeUnknown, false, 0, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if err := test.outcome.Validate(); err != nil {
				t.Fatal(err)
			}
			code, hasCode := test.outcome.ExitCode()
			if test.outcome.Kind() != test.kind || test.outcome.Successful() != test.successful ||
				code != test.code || hasCode != test.hasCode {
				t.Fatalf("outcome = %#v, %d, %t", test.outcome, code, hasCode)
			}
		})
	}
	for _, code := range []int64{-1, process.MaximumExitCode + 1} {
		if _, err := process.NewExitedOutcome(code); err == nil {
			t.Fatalf("invalid exit code %d succeeded", code)
		}
	}
	if err := (process.Outcome{}).Validate(); err == nil {
		t.Fatal("zero outcome validated")
	}
}

func TestFailureIsTypedRedactedAndPreservesCancellationIdentity(t *testing.T) {
	t.Parallel()
	secret := errors.New("secret-token-and-command")
	failure := process.NewFailure(process.OperationLaunch, fmt.Errorf("platform: %w", secret))
	var typed *process.Failure
	if !errors.As(failure, &typed) || typed.Operation() != process.OperationLaunch || !errors.Is(failure, secret) {
		t.Fatalf("failure identity = %T %v", failure, failure)
	}
	encoded, err := json.Marshal(failure)
	if err != nil {
		t.Fatal(err)
	}
	for _, rendered := range []string{fmt.Sprint(failure), fmt.Sprintf("%+v", failure), fmt.Sprintf("%#v", failure), string(encoded)} {
		if strings.Contains(rendered, "secret-token-and-command") {
			t.Fatalf("formatted failure leaked cause: %q", rendered)
		}
	}
	canceled := process.NewFailure(process.OperationWait, context.Canceled)
	if !errors.Is(canceled, context.Canceled) {
		t.Fatal("cancellation identity was lost")
	}
	if process.NewFailure(process.OperationWait, nil) != nil {
		t.Fatal("nil cause produced a failure")
	}
	unknown := process.NewFailure("private-operation", secret)
	if !errors.As(unknown, &typed) || typed.Operation() != "" || strings.Contains(unknown.Error(), "private") {
		t.Fatalf("unknown operation was not redacted: %v", unknown)
	}
	if !typed.Retryable() {
		t.Fatal("unclassified cause unexpectedly disabled a safe re-proof attempt")
	}
	nonRetryable := process.NewFailure(
		process.OperationWait,
		classifiedFailure{message: "private terminal cause", retryable: false},
	)
	if typedFailure(t, nonRetryable).Retryable() {
		t.Fatal("explicit terminal classification was not preserved")
	}
	for name, cause := range map[string]error{
		"terminal first": errors.Join(
			classifiedFailure{message: "terminal", retryable: false},
			classifiedFailure{message: "retryable", retryable: true},
		),
		"terminal last": errors.Join(
			classifiedFailure{message: "retryable", retryable: true},
			classifiedFailure{message: "terminal", retryable: false},
		),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			joined := process.NewFailure(process.OperationWait, cause)
			if typedFailure(t, joined).Retryable() {
				t.Fatal("retryable sibling masked terminal classification")
			}
		})
	}
	panicking := process.NewFailure(process.OperationWait, panickingRetryability{})
	if typedFailure(t, panicking).Retryable() {
		t.Fatal("panicking retry classifier did not fail closed")
	}
}

func TestLauncherContextBoundsLaunchOnly(t *testing.T) {
	t.Parallel()
	launchContext, cancelLaunch := context.WithCancel(context.Background())
	rootDone := make(chan struct{})
	fixture := &contractProcess{done: rootDone, outcome: mustExited(t, 0)}
	launcher := process.LauncherFunc(func(ctx context.Context, _ process.Spec) (process.Process, error) {
		if ctx != launchContext {
			t.Fatal("launcher did not receive exact launch context")
		}
		return fixture, nil
	})
	launched, err := launcher.Start(launchContext, process.Spec{})
	if err != nil || launched != fixture {
		t.Fatalf("launch = %T %v", launched, err)
	}
	cancelLaunch()
	select {
	case <-launched.Done():
		t.Fatal("launch-context cancellation changed process lifetime")
	default:
	}
	close(rootDone)
	if result, resultErr := launched.Result(); resultErr != nil || !result.Successful() {
		t.Fatalf("root result = %#v, %v", result, resultErr)
	}
}

func TestWaitFailureCanBeRetriedIndependentlyOfRootOutcome(t *testing.T) {
	t.Parallel()
	joinFailure := errors.New("private-containment-detail")
	fixture := &contractProcess{
		done: closedChannel(), outcome: mustExited(t, 7), waitFailures: []error{joinFailure, nil},
	}
	result, err := fixture.Result()
	if err != nil {
		t.Fatal(err)
	}
	code, ok := result.ExitCode()
	if !ok || code != 7 {
		t.Fatalf("root outcome = %#v", result)
	}
	if err = fixture.Wait(context.Background()); !errors.Is(err, joinFailure) {
		t.Fatalf("first wait = %v", err)
	}
	if err = fixture.Wait(context.Background()); err != nil {
		t.Fatalf("retry wait = %v", err)
	}
	if fixture.waitCalls != 2 {
		t.Fatalf("wait calls = %d", fixture.waitCalls)
	}
}

type contractProcess struct {
	done         <-chan struct{}
	outcome      process.Outcome
	waitFailures []error
	waitCalls    int
}

type classifiedFailure struct {
	message   string
	retryable bool
}

func (failure classifiedFailure) Error() string   { return failure.message }
func (failure classifiedFailure) Retryable() bool { return failure.retryable }

type panickingRetryability struct{}

func (panickingRetryability) Error() string   { return "private panic" }
func (panickingRetryability) Retryable() bool { panic("private panic") }

func (fixture *contractProcess) Done() <-chan struct{}            { return fixture.done }
func (fixture *contractProcess) Result() (process.Outcome, error) { return fixture.outcome, nil }
func (*contractProcess) RequestStop(context.Context) error        { return nil }
func (*contractProcess) ForceKill(context.Context) error          { return nil }
func (fixture *contractProcess) Wait(context.Context) error {
	index := fixture.waitCalls
	fixture.waitCalls++
	if index >= len(fixture.waitFailures) {
		return nil
	}
	return fixture.waitFailures[index]
}

func mustExited(tb testing.TB, code int64) process.Outcome {
	tb.Helper()
	outcome, err := process.NewExitedOutcome(code)
	if err != nil {
		tb.Fatal(err)
	}
	return outcome
}

func typedFailure(tb testing.TB, err error) *process.Failure {
	tb.Helper()
	var failure *process.Failure
	if !errors.As(err, &failure) {
		tb.Fatalf("error = %T, want *process.Failure", err)
	}
	return failure
}

func closedChannel() <-chan struct{} {
	closed := make(chan struct{})
	close(closed)
	return closed
}
