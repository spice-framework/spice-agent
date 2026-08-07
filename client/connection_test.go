package client

import (
	"slices"
	"strings"
	"testing"
)

func TestConnectionContractIsCanonicalAndImmutable(t *testing.T) {
	t.Parallel()
	limits := testLimits(t)
	firstRef := mustDefinitionRef(t, "assistant", "v1")
	secondRef := mustDefinitionRef(t, "coding", "v2")
	first := mustDefinition(t, firstRef, "gpt-test", 10)
	second := mustDefinition(t, secondRef, "gpt-code", 20)
	inputDefinitions := []Definition{second, first}
	catalog, err := NewCatalog("catalog-v1", inputDefinitions, limits)
	if err != nil {
		t.Fatal(err)
	}
	inputDefinitions[0] = Definition{}
	definitions := catalog.Definitions()
	if definitions[0].Ref() != firstRef || definitions[1].Ref() != secondRef {
		t.Fatalf("catalog order = %#v", definitions)
	}
	definitions[0] = Definition{}
	if found, ok := catalog.Find(firstRef); !ok || found.Model() != "gpt-test" {
		t.Fatalf("catalog find = %#v, %t", found, ok)
	}

	reasons := []string{"provider slow", "disk pressure"}
	health, err := NewHealth(HealthDegraded, reasons, 1, limits)
	if err != nil {
		t.Fatal(err)
	}
	reasons[0] = "mutated"
	if got := health.Reasons(); !slices.Equal(got, []string{"disk pressure", "provider slow"}) {
		t.Fatalf("health reasons = %v", got)
	}
	returnedReasons := health.Reasons()
	returnedReasons[0] = "mutated"
	if health.Reasons()[0] != "disk pressure" {
		t.Fatal("health exposed its reason storage")
	}

	version, err := NewProtocolVersion(1, 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	build := testBuild(t)
	if build.Version() != "v0.1.0" || build.Commit() != "deadbeef" || build.GoVersion() != "go1.26.5" {
		t.Fatalf("build provenance = %#v", build)
	}
	if version.Major() != 1 || version.Minor() != 1 || version.Patch() != 0 {
		t.Fatalf("protocol version = %#v", version)
	}
	capabilities := []string{"snapshots", "events"}
	connection, err := NewConnection(ConnectionSpec{
		Protocol: version, Server: build, Capabilities: capabilities,
		Limits: limits, Health: health, ClientID: "client-1",
		OwnershipEpoch: 2, Catalog: catalog,
	})
	if err != nil {
		t.Fatal(err)
	}
	capabilities[0] = "mutated"
	if got := connection.Capabilities(); !slices.Equal(got, []string{"events", "snapshots"}) {
		t.Fatalf("capabilities = %v", got)
	}
	returned := connection.Capabilities()
	returned[0] = "mutated"
	if connection.Capabilities()[0] != "events" {
		t.Fatal("connection exposed its capability storage")
	}
	if err := connection.Validate(); err != nil {
		t.Fatal(err)
	}
	if connection.Protocol() != version || connection.Server() != build || connection.ClientID() != "client-1" ||
		connection.OwnershipEpoch() != 2 || connection.Limits() != limits || connection.Health().State() != HealthDegraded ||
		connection.Catalog().Revision() != "catalog-v1" {
		t.Fatalf("connection accessors returned inconsistent values: %#v", connection)
	}
	if health.Limits() != limits {
		t.Fatalf("health limits = %#v", health.Limits())
	}
	if limits.MessageBytes() != 1<<20 || limits.CollectionItems() != 1024 || limits.ReplayEvents() != 128 ||
		limits.ReplayBytes() != 1<<20 || limits.ConcurrentStreams() != 8 || health.ActiveRuns() != 1 {
		t.Fatalf("limits or health accessors returned inconsistent values: %#v %#v", limits, health)
	}
	if protocolRange, rangeErr := NewProtocolRange(version, version); rangeErr != nil ||
		protocolRange.Minimum() != version || protocolRange.Maximum() != version {
		t.Fatalf("protocol range = %#v, err=%v", protocolRange, rangeErr)
	}
	if firstRef.ID() != "assistant" || firstRef.Revision() != "v1" || first.MaxTurns() != 10 {
		t.Fatalf("definition accessors = %#v %#v", firstRef, first)
	}
	serverLimits, _ := NewLimits(2<<20, 2048, 256, 2<<20, 16, 8)
	serverHealth, _ := NewHealth(HealthReady, nil, 0, serverLimits)
	if _, err := NewConnection(ConnectionSpec{
		Protocol: version, Server: build, Capabilities: capabilities,
		Limits: limits, Health: serverHealth, ClientID: "client-2",
		OwnershipEpoch: 1, Catalog: catalog,
	}); err != nil {
		t.Fatalf("server health capacity may exceed negotiated client limits: %v", err)
	}
}

func TestConnectionValuesRejectInvalidBoundaries(t *testing.T) {
	t.Parallel()
	validVersion, _ := NewProtocolVersion(1, 0, 0)
	newerVersion, _ := NewProtocolVersion(1, 1, 0)
	otherMajor, _ := NewProtocolVersion(2, 0, 0)
	if _, err := NewProtocolVersion(0, 0, 0); err == nil {
		t.Fatal("zero protocol major accepted")
	}
	for _, values := range []struct {
		minimum ProtocolVersion
		maximum ProtocolVersion
	}{
		{minimum: newerVersion, maximum: validVersion},
		{minimum: validVersion, maximum: otherMajor},
		{minimum: ProtocolVersion{}, maximum: validVersion},
	} {
		if _, err := NewProtocolRange(values.minimum, values.maximum); err == nil {
			t.Fatalf("invalid protocol range accepted: %#v", values)
		}
	}
	if _, err := NewLimits(0, 1, 1, 1, 1, 1); err == nil {
		t.Fatal("zero limits accepted")
	}
	if _, err := NewLimits(1, 1, 2, 1, 1, 1); err == nil {
		t.Fatal("inconsistent replay limits accepted")
	}
	for _, fields := range [][4]string{
		{"", "v", "c", "go"},
		{"component", " v", "c", "go"},
		{"component", "v", "", "go"},
		{"component", "v", "c", string([]byte{0xff})},
	} {
		if _, err := NewBuild(fields[0], fields[1], fields[2], fields[3]); err == nil {
			t.Fatalf("invalid build accepted: %q", fields)
		}
	}
	for _, control := range []string{"\x00", "\r", "\n", "\t"} {
		if _, err := NewBuild("component"+control+"suffix", "v", "c", "go"); err == nil {
			t.Fatalf("build component containing %q accepted", control)
		}
	}

	limits := testLimits(t)
	if _, err := NewHealth("unknown", nil, 0, limits); err == nil {
		t.Fatal("unknown health accepted")
	}
	if _, err := NewHealth(HealthReady, []string{"unexpected"}, 0, limits); err == nil {
		t.Fatal("ready health with degradation accepted")
	}
	if _, err := NewHealth(HealthDegraded, nil, 0, limits); err == nil {
		t.Fatal("degraded health without reason accepted")
	}
	if _, err := NewHealth(HealthReady, nil, 9, limits); err == nil {
		t.Fatal("health active-run overflow accepted")
	}
	if _, err := NewHealth(HealthDegraded, []string{strings.Repeat("x", maximumStatusBytes+1)}, 0, limits); err == nil {
		t.Fatal("oversized health reason accepted")
	}

	ref := mustDefinitionRef(t, "agent", "v1")
	definition := mustDefinition(t, ref, "model", 1)
	if _, err := NewDefinition(DefinitionRef{}, "model", 1); err == nil {
		t.Fatal("zero definition ref accepted")
	}
	if _, err := NewDefinition(ref, "", 1); err == nil {
		t.Fatal("empty model accepted")
	}
	if _, err := NewDefinition(ref, "model", 0); err == nil {
		t.Fatal("zero turns accepted")
	}
	if _, err := NewCatalog("revision", nil, limits); err == nil {
		t.Fatal("empty catalog accepted")
	}
	if _, err := NewCatalog("revision", []Definition{definition, definition}, limits); err == nil {
		t.Fatal("duplicate catalog definition accepted")
	}
	smallLimits, _ := NewLimits(1024, 1, 1, 1024, 1, 1)
	other := mustDefinition(t, mustDefinitionRef(t, "other", "v1"), "model", 1)
	if _, err := NewCatalog("revision", []Definition{definition, other}, smallLimits); err == nil {
		t.Fatal("catalog exceeded negotiated collection limit")
	}
}

func testLimits(t *testing.T) Limits {
	t.Helper()
	value, err := NewLimits(1<<20, 1024, 128, 1<<20, 8, 4)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func testBuild(t *testing.T) Build {
	t.Helper()
	value, err := NewBuild("client", "v0.1.0", "deadbeef", "go1.26.5")
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func mustDefinitionRef(t *testing.T, id, revision string) DefinitionRef {
	t.Helper()
	value, err := NewDefinitionRef(id, revision)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func mustDefinition(t *testing.T, ref DefinitionRef, model string, maxTurns uint32) Definition {
	t.Helper()
	value, err := NewDefinition(ref, model, maxTurns)
	if err != nil {
		t.Fatal(err)
	}
	return value
}
