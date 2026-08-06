package agent

import "testing"

func TestFirstFunctionResultHandlesProviderFormsAndBoundaries(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name     string
		typeID   string
		want     string
		wantFail bool
	}{
		{"simple", "func() example.com/Output", "example.com/Output", false},
		{"nested function", "func(callback func(context.Context) error) example.com/Output", "example.com/Output", false},
		{"generic result", "func() example.com/Stage[example.com/In, example.com/Out]", "example.com/Stage[example.com/In, example.com/Out]", false},
		{"error result", "func() (example.com/Output, error)", "example.com/Output", false},
		{"named results", "func() (output example.com/Output, err error)", "output example.com/Output", false},
		{"generic function", "func[T any]() T", "", true},
		{"no result", "func()", "", true},
		{"incomplete", "func(value int", "", true},
		{"not function", "example.com/Output", "", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, err := firstFunctionResult(test.typeID)
			if (err != nil) != test.wantFail || got != test.want {
				t.Fatalf("firstFunctionResult(%q) = %q, %v", test.typeID, got, err)
			}
		})
	}
}
