package runauthority

import (
	"bytes"
	"testing"
)

func FuzzSignedRecord(f *testing.F) {
	store := &Store{authorityGeneration: 1}
	copy(store.scopeID[:], bytes.Repeat([]byte{1}, len(store.scopeID)))
	copy(store.key[:], bytes.Repeat([]byte{2}, len(store.key)))
	valid, err := store.encodeRecord("run", store.baseRecord("run", 1, PhaseActive))
	if err != nil {
		f.Fatal(err)
	}
	f.Add(valid)
	f.Add([]byte(`{"record":{},"hmac_sha256":"00"}`))
	f.Fuzz(func(_ *testing.T, wire []byte) {
		_, _ = store.decodeRecord("run", wire)
	})
}
