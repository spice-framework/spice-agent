package client

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestInitializationAttemptIDCanonicalComparableAndImmutable(t *testing.T) {
	t.Parallel()
	const encoded = "00112233445566778899aabbccddeeff"
	id, err := ParseInitializationAttemptID(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if id.String() != encoded || id.Validate() != nil {
		t.Fatalf("attempt ID = %q, validation = %v", id.String(), id.Validate())
	}
	if parsed, parseErr := ParseInitializationAttemptID(id.String()); parseErr != nil || parsed != id {
		t.Fatalf("canonical round trip = %v, %v", parsed, parseErr)
	}
	values := map[InitializationAttemptID]string{id: "comparable"}
	if values[id] != "comparable" {
		t.Fatal("attempt ID is not a stable comparable map key")
	}
	wireCopy := id.Bytes()
	wireCopy[0] = 0xff
	if wireCopy == id.Bytes() || id.String() != encoded {
		t.Fatalf("mutating byte copy changed attempt ID to %q", id.String())
	}
	text, err := id.MarshalText()
	if err != nil || string(text) != encoded {
		t.Fatalf("MarshalText = %q, %v", text, err)
	}
}

func TestInitializationAttemptIDGenerationRetriesZeroAndRedactsFailures(t *testing.T) {
	t.Parallel()
	valid := bytes.Repeat([]byte{0x5a}, InitializationAttemptIDBytes)
	source := bytes.NewReader(append(make([]byte, InitializationAttemptIDBytes), valid...))
	id, err := generateInitializationAttemptID(source)
	if err != nil || id.Bytes() != [InitializationAttemptIDBytes]byte{
		0x5a, 0x5a, 0x5a, 0x5a, 0x5a, 0x5a, 0x5a, 0x5a,
		0x5a, 0x5a, 0x5a, 0x5a, 0x5a, 0x5a, 0x5a, 0x5a,
	} {
		t.Fatalf("generated ID = %v, %v", id, err)
	}

	if _, err = generateInitializationAttemptID(errorReader{}); err == nil || strings.Contains(err.Error(), "sensitive") {
		t.Fatalf("entropy failure = %v, want redacted error", err)
	}
	if _, err = generateInitializationAttemptID(bytes.NewReader(make(
		[]byte,
		InitializationAttemptIDBytes*initializationAttemptGenerateTries,
	))); err == nil {
		t.Fatal("repeated zero entropy identities were accepted")
	}
	generated, err := NewInitializationAttemptID()
	if err != nil || generated.Validate() != nil {
		t.Fatalf("secure generated ID = %v, %v", generated, err)
	}
}

func TestInitializationAttemptIDRejectsNoncanonicalValues(t *testing.T) {
	t.Parallel()
	for _, value := range []string{
		"", "0011", "00112233445566778899AABBCCDDEEFF",
		"00112233-4455-6677-8899-aabbccddeeff",
		"gg112233445566778899aabbccddeeff",
		"00000000000000000000000000000000",
	} {
		if _, err := ParseInitializationAttemptID(value); err == nil {
			t.Fatalf("ParseInitializationAttemptID(%q) succeeded", value)
		}
	}
	if _, err := (InitializationAttemptID{}).MarshalText(); err == nil {
		t.Fatal("zero attempt ID marshaled")
	}
}

func TestInitializationReplayErrorCarriesOnlyExactRetryIdentity(t *testing.T) {
	t.Parallel()
	id, err := ParseInitializationAttemptID("00112233445566778899aabbccddeeff")
	if err != nil {
		t.Fatal(err)
	}
	facts, err := NewErrorFacts("replay the exact initialization", true, nil)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := NewInitializationReplayError(facts, id)
	if err != nil || replay.AttemptID() != id || !replay.Retryable() || replay.Facts() != facts {
		t.Fatalf("initialization replay error = %#v, %v", replay, err)
	}
	if _, present := replay.Operation(); present {
		t.Fatal("initialization replay unexpectedly carried an operation ID")
	}

	nonretryable, err := NewErrorFacts("not retryable", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = NewInitializationReplayError(nonretryable, id); err == nil {
		t.Fatal("non-retryable initialization replay facts were accepted")
	}
	operation, err := NewOperationID("operation")
	if err != nil {
		t.Fatal(err)
	}
	operationFacts, err := NewErrorFacts("wrong correlation", true, &operation)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = NewInitializationReplayError(operationFacts, id); err == nil {
		t.Fatal("operation-correlated initialization replay facts were accepted")
	}
	if _, err = NewInitializationReplayError(facts, InitializationAttemptID{}); err == nil {
		t.Fatal("zero initialization replay identity was accepted")
	}

	var absent *InitializationReplayError
	if absent.Error() == "" || absent.Facts() != (ErrorFacts{}) || absent.Retryable() ||
		absent.AttemptID() != (InitializationAttemptID{}) {
		t.Fatalf("nil initialization replay accessors are unsafe: %#v", absent)
	}
	if value, present := absent.Operation(); present || value != (OperationID{}) {
		t.Fatalf("nil initialization replay operation = %v, %t", value, present)
	}
}

type errorReader struct{}

func (errorReader) Read([]byte) (int, error) {
	return 0, errors.New("sensitive entropy implementation detail")
}
