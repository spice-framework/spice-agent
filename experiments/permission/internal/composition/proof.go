package composition

import (
	"context"

	permission "github.com/spice-framework/spice-agent/experiments/permission"
	"github.com/spice-framework/spice-agent/stage"
)

// @import { Bean } from "github.com/spice-framework/spice/annotation/core"

// Proof captures the generated guard collection.
type Proof struct{ guards []stage.ToolDispatchGuard }

// NewProofPolicy contributes the application-owned policy.
//
// @Bean(name="permissionPolicy")
func NewProofPolicy() permission.Policy {
	return permission.PolicyFunc(func(context.Context, permission.Facts) (permission.Decision, error) {
		return permission.DecisionAllow, nil
	})
}

// NewProofGuard contributes the policy as the normal terminal guard bean.
//
// @Bean(name="permissionGuard")
func NewProofGuard(policy permission.Policy) (stage.ToolDispatchGuard, error) {
	return permission.NewGuard(policy, permission.Options{})
}

// NewProof proves generated collection injection without a registry.
//
// @Bean(name="proof")
func NewProof(guards []stage.ToolDispatchGuard) *Proof {
	return &Proof{guards: append([]stage.ToolDispatchGuard(nil), guards...)}
}

// GuardCount reports the generated collection size.
func (proof *Proof) GuardCount() int { return len(proof.guards) }
