package compositionfixture

import (
	"context"
	"errors"

	"github.com/spice-framework/spice-agent/model"
)

// @import { ModelProvider } from "github.com/spice-framework/spice-agent/annotation/agent"
// @import { Primary } from "github.com/spice-framework/spice/annotation/core"

type providerStub struct {
	name string
}

func (provider *providerStub) Stream(context.Context, model.Request) (model.Stream, error) {
	return nil, errors.New("composition fixture provider does not perform network I/O")
}

// ProviderName exposes the selected fixture implementation without widening
// the production model.Provider interface.
func ProviderName(provider ProviderAlias) string {
	typed, ok := provider.(*providerStub)
	if !ok || typed == nil {
		return "external"
	}
	return typed.name
}

// NewProviderStub constructs an application-owned test provider.
func NewProviderStub(name string) ProviderAlias {
	return &providerStub{name: name}
}

// NewFallbackProvider constructs the replaceable default model provider.
//
// @ModelProvider(name="fallback", fallback=true)
func NewFallbackProvider() ProviderAlias {
	return &providerStub{name: "fallback"}
}

// NewAlphaProvider constructs the first normal model provider.
//
// @ModelProvider(name="alpha")
func NewAlphaProvider() ProviderAlias {
	return &providerStub{name: "alpha"}
}

// NewBetaProvider constructs the explicitly preferred normal model provider.
//
// @ModelProvider(name="beta")
// @Primary
func NewBetaProvider() ProviderAlias {
	return &providerStub{name: "beta"}
}
