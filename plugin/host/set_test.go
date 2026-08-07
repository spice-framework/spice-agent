package pluginhost

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
)

func TestSetIsCanonicalImmutableAndComplete(t *testing.T) {
	t.Parallel()
	second := executableNamed(t, "second", "manifest.second")
	first := executableNamed(t, "first", "manifest.first")
	input := []Executable{second, first}
	set, err := NewSet(input)
	if err != nil {
		t.Fatal(err)
	}
	input[0] = Executable{}

	got := set.Executables()
	if set.Len() != 2 || !slices.EqualFunc(got, []Executable{first, second}, func(left, right Executable) bool {
		return left.ID() == right.ID()
	}) {
		t.Fatalf("canonical set = %#v", got)
	}
	got[0] = Executable{}
	if set.Executables()[0].ID() != "first" {
		t.Fatal("set exposed mutable executable storage")
	}
	if err = set.Validate(); err != nil {
		t.Fatal(err)
	}

	empty, err := NewSet(nil)
	if err != nil || empty.Len() != 0 || empty.Executables() == nil {
		t.Fatalf("empty set = %#v, %v", empty, err)
	}
}

func TestSetRejectsInvalidAndDuplicateConfigurations(t *testing.T) {
	t.Parallel()
	valid := executableNamed(t, "first", "manifest.first")
	duplicateID := executableNamed(t, "first", "manifest.second")
	duplicateManifest := executableNamed(t, "second", "manifest.first")
	for name, values := range map[string][]Executable{
		"invalid":            {{}, valid},
		"duplicate id":       {valid, duplicateID},
		"duplicate manifest": {duplicateManifest, valid},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if set, err := NewSet(values); err == nil || set.Len() != 0 {
				t.Fatalf("NewSet() = %#v, %v", set, err)
			}
		})
	}
	tooMany := make([]Executable, MaximumExecutables+1)
	if set, err := NewSet(tooMany); err == nil || set.Len() != 0 {
		t.Fatalf("oversized NewSet() = %#v, %v", set, err)
	}
}

func TestSetFormattingExposesOnlyCount(t *testing.T) {
	t.Parallel()
	executable := executableNamed(t, "private-plugin-id", "private.manifest")
	set, err := NewSet([]Executable{executable})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(set)
	if err != nil {
		t.Fatal(err)
	}
	for _, output := range []string{
		set.String(), set.GoString(), fmt.Sprint(set), fmt.Sprintf("%+v", set),
		fmt.Sprintf("%#v", set), string(encoded),
	} {
		for _, private := range []string{
			executable.ID(), executable.ManifestName(), executable.Path(),
			executable.SHA256().String(), executable.Environment()[0],
		} {
			if strings.Contains(output, private) {
				t.Fatalf("set formatting %q exposed private configuration", output)
			}
		}
	}
	if string(encoded) != `{"count":1}` {
		t.Fatalf("set JSON = %s", encoded)
	}
}

func executableNamed(t *testing.T, id, manifest string) Executable {
	t.Helper()
	config := validExecutableConfig(t)
	config.ID = id
	config.ManifestName = manifest
	executable, err := NewExecutable(config)
	if err != nil {
		t.Fatal(err)
	}
	return executable
}
