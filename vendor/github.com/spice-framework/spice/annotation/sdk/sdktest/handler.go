// Package sdktest provides deterministic black-box tests for annotation SDK
// descriptors and handlers. It uses only the public SDK contract, so the same
// harness works for official and third-party annotations.
package sdktest

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/spice-framework/spice/annotation/sdk"
)

const defaultHandlerTimeout = 5 * time.Second

// HandlerCase describes one bounded descriptor-handler invocation.
type HandlerCase struct {
	Name                 string
	Invocation           sdk.Invocation
	Timeout              time.Duration
	Canceled             bool
	WantKinds            []sdk.ContributionKind
	WantDiagnostics      []sdk.HandlerDiagnostic
	WantErrorContains    string
	SkipDeterminismCheck bool
	Check                func(*testing.T, sdk.Result)
}

// RunHandlerCases validates definition, invokes its typed handler, validates
// every returned contribution and diagnostic, and checks deterministic output.
func RunHandlerCases(
	t *testing.T,
	definition sdk.Definition,
	cases ...HandlerCase,
) {
	t.Helper()
	if err := definition.Validate(); err != nil {
		t.Fatalf("definition.Validate() error = %v", err)
	}
	for _, test := range cases {
		t.Run(test.Name, func(t *testing.T) {
			t.Helper()
			runHandlerCase(t, definition, test)
		})
	}
}

func runHandlerCase(
	t *testing.T,
	definition sdk.Definition,
	test HandlerCase,
) {
	t.Helper()
	if strings.TrimSpace(test.Name) == "" {
		t.Fatal("handler case name is required")
	}
	if !slices.Contains(
		definition.Targets,
		test.Invocation.Declaration.Target,
	) {
		t.Fatalf(
			"invocation target %q is not allowed by %s",
			test.Invocation.Declaration.Target,
			definition.Name,
		)
	}
	timeout := test.Timeout
	if timeout == 0 {
		timeout = defaultHandlerTimeout
	}
	if timeout < 0 || timeout > time.Minute {
		t.Fatalf("handler timeout %s must be between 1ns and 1m", timeout)
	}

	invocation := cloneInvocation(t, test.Invocation)
	result, err := invokeHandler(
		definition.Implementation.Handler,
		invocation,
		timeout,
		test.Canceled,
	)
	checkHandlerError(t, err, test.WantErrorContains)
	validateHandlerResult(t, result)
	if !reflect.DeepEqual(contributionKinds(result), test.WantKinds) {
		t.Fatalf(
			"contribution kinds = %v, want %v",
			contributionKinds(result),
			test.WantKinds,
		)
	}
	if !reflect.DeepEqual(result.Diagnostics, test.WantDiagnostics) {
		t.Fatalf(
			"diagnostics = %#v, want %#v",
			result.Diagnostics,
			test.WantDiagnostics,
		)
	}
	if test.Check != nil {
		test.Check(t, result)
	}
	if test.SkipDeterminismCheck {
		return
	}

	second, secondErr := invokeHandler(
		definition.Implementation.Handler,
		cloneInvocation(t, test.Invocation),
		timeout,
		test.Canceled,
	)
	if !sameError(err, secondErr) || !reflect.DeepEqual(result, second) {
		t.Fatalf(
			"handler output changed between identical invocations:\nfirst:  result=%#v error=%v\nsecond: result=%#v error=%v",
			result,
			err,
			second,
			secondErr,
		)
	}
}

func invokeHandler(
	handler sdk.Handler,
	invocation sdk.Invocation,
	timeout time.Duration,
	canceled bool,
) (sdk.Result, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	if canceled {
		cancel()
	} else {
		defer cancel()
	}
	return handler(ctx, invocation)
}

func validateHandlerResult(t *testing.T, result sdk.Result) {
	t.Helper()
	seen := make(map[sdk.ContributionKind]struct{}, len(result.Contributions))
	for index, contribution := range result.Contributions {
		if err := contribution.Validate(); err != nil {
			t.Fatalf("contribution %d is invalid: %v", index, err)
		}
		if _, duplicate := seen[contribution.Kind]; duplicate {
			t.Fatalf(
				"handler returned duplicate %q contributions",
				contribution.Kind,
			)
		}
		seen[contribution.Kind] = struct{}{}
	}
	for index, diagnostic := range result.Diagnostics {
		if strings.TrimSpace(diagnostic.Code) == "" ||
			strings.TrimSpace(diagnostic.Severity) == "" ||
			strings.TrimSpace(diagnostic.Message) == "" {
			t.Fatalf(
				"diagnostic %d requires code, severity, and message: %#v",
				index,
				diagnostic,
			)
		}
	}
}

func contributionKinds(result sdk.Result) []sdk.ContributionKind {
	if len(result.Contributions) == 0 {
		return nil
	}
	kinds := make([]sdk.ContributionKind, len(result.Contributions))
	for index, contribution := range result.Contributions {
		kinds[index] = contribution.Kind
	}
	return kinds
}

func checkHandlerError(
	t *testing.T,
	err error,
	wantContains string,
) {
	t.Helper()
	if wantContains == "" {
		if err != nil {
			t.Fatalf("handler error = %v", err)
		}
		return
	}
	if err == nil || !strings.Contains(err.Error(), wantContains) {
		t.Fatalf("handler error = %v, want substring %q", err, wantContains)
	}
}

func sameError(first, second error) bool {
	switch {
	case first == nil:
		return second == nil
	case second == nil:
		return false
	default:
		return first.Error() == second.Error()
	}
}

func cloneInvocation(t *testing.T, invocation sdk.Invocation) sdk.Invocation {
	t.Helper()
	content, err := json.Marshal(invocation)
	if err != nil {
		t.Fatalf("marshal invocation: %v", err)
	}
	var clone sdk.Invocation
	if err := json.Unmarshal(content, &clone); err != nil {
		t.Fatalf("unmarshal invocation: %v", err)
	}
	return clone
}

// Invocation returns a normalized invocation for the supplied descriptor
// declaration. Tests can append arguments and facts explicitly.
func Invocation(
	descriptorPackage string,
	descriptorSymbol string,
	canonicalName string,
	declaration sdk.Declaration,
) sdk.Invocation {
	return sdk.Invocation{
		DescriptorPackage: descriptorPackage,
		DescriptorSymbol:  descriptorSymbol,
		CanonicalName:     canonicalName,
		Declaration:       declaration,
	}
}

// StringArgument creates one normalized string invocation argument.
func StringArgument(name, value string, positional bool) sdk.InvocationArgument {
	content, err := json.Marshal(value)
	if err != nil {
		panic(fmt.Sprintf("marshal string annotation argument: %v", err))
	}
	return sdk.InvocationArgument{
		Name:       name,
		Kind:       sdk.KindString,
		Positional: positional,
		Value:      content,
	}
}
