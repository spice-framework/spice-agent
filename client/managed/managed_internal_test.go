package managed

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/spice-framework/spice-agent/client"
)

func TestFinishInitializeSuppressesSessionAfterShutdownLinearizes(t *testing.T) {
	t.Parallel()

	operationContext, cancel := context.WithCancelCause(context.Background())
	connector := &Connector{
		initializeGate: make(chan struct{}, 1),
		closed:         true,
		activeCancel:   cancel,
	}
	session := &lateSession{}
	got, err := connector.finishInitialize(operationContext, cancel, session, nil)
	if got != nil || !errors.Is(err, ErrClosed) || session.closes.Load() != 1 {
		t.Fatalf("late initialization = %#v, %v; closes=%d", got, err, session.closes.Load())
	}
	select {
	case <-connector.initializeGate:
	default:
		t.Fatal("initialization gate was released before shutdown could join")
	}
}

type lateSession struct {
	client.Session
	closes atomic.Int32
}

func (session *lateSession) Close() error {
	session.closes.Add(1)
	return nil
}
