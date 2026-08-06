package compositionfixture

import (
	"context"
	"strings"
)

// @import { Stage } from "github.com/spice-framework/spice-agent/annotation/agent"

type trimStage struct{}

func (*trimStage) Process(_ context.Context, input string) (string, error) {
	return strings.TrimSpace(input), nil
}

type suffixStage struct{}

func (*suffixStage) Process(_ context.Context, input string) (string, error) {
	return input + "!", nil
}

type namedStage struct {
	name string
}

func (current *namedStage) Name() string {
	return current.name
}

// NewTrimStage constructs the first ordered text stage.
//
// @Stage(name="trim", aliases=["normalize"], order=-10)
func NewTrimStage() TextStage {
	return &trimStage{}
}

// NewSuffixStage constructs the second ordered text stage.
//
// @Stage(name="suffix", order=20)
func NewSuffixStage() TextStage {
	return &suffixStage{}
}

// NewFallbackOnlyStage constructs the only candidate for FallbackStage.
//
// @Stage(name="fallback-only", fallback=true)
func NewFallbackOnlyStage() FallbackStage {
	return &namedStage{name: "fallback-only"}
}

// NewReplaceableDefaultStage constructs the suppressed fallback candidate.
//
// @Stage(name="replaceable-default", fallback=true)
func NewReplaceableDefaultStage() ReplaceableStage {
	return &namedStage{name: "replaceable-default"}
}

// NewReplaceableNormalStage constructs the selected normal candidate.
//
// @Stage(name="replaceable-normal")
func NewReplaceableNormalStage() ReplaceableStage {
	return &namedStage{name: "replaceable-normal"}
}
