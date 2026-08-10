package composition_test

import (
	"context"
	"testing"

	spicegen "github.com/spice-framework/spice-agent/experiments/sqlite-recovery/internal/spicegen/sqliterecoveryproof"
)

func TestGeneratedApplicationInjectsStoreFactory(t *testing.T) {
	application, err := spicegen.NewApplication(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !application.Components().SqliteRecoveryProof.HasFactory() {
		t.Fatal("generated application omitted the recovery store factory")
	}
	if err = application.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
}
