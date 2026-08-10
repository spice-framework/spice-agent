package composition

import (
	"context"
	"slices"

	gitworkflow "github.com/spice-framework/spice-agent/experiments/git-workflow"
	"github.com/spice-framework/spice-agent/stage"
	"github.com/spice-framework/spice-agent/tool"
)

// @import { Bean } from "github.com/spice-framework/spice/annotation/core"

const proofStagedDigest = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// Proof captures generated tool-map and terminal-guard collection injection.
type Proof struct {
	tools  map[string]tool.Tool
	guards []stage.ToolDispatchGuard
}

type proofBackend struct{}

func (*proofBackend) Inspect(context.Context) (gitworkflow.Inspection, error) {
	return gitworkflow.Inspection{Status: "# branch.head proof\n", StagedDigest: proofStagedDigest}, nil
}

func (*proofBackend) StagedDigest(context.Context) (string, error) { return proofStagedDigest, nil }

func (*proofBackend) CommitStaged(context.Context, string, string) error { return nil }

// NewProofBackend contributes the exact application-owned Git boundary.
//
// @Bean(name="gitBackend")
func NewProofBackend() gitworkflow.Backend { return &proofBackend{} }

// NewProofAuthorityStore contributes the shared single-use authority ledger.
//
// @Bean(name="gitAuthorityStore")
func NewProofAuthorityStore() *gitworkflow.AuthorityStore { return gitworkflow.NewAuthorityStore() }

// NewProofInspectTool contributes git.inspect through exact interface output.
//
// @Bean(name="git.inspect")
func NewProofInspectTool(backend gitworkflow.Backend) (tool.Tool, error) {
	return gitworkflow.NewInspectTool(backend)
}

// NewProofCommitTool contributes guarded git.commit_staged.
//
// @Bean(name="git.commit_staged")
func NewProofCommitTool(
	backend gitworkflow.Backend,
	store *gitworkflow.AuthorityStore,
) (tool.Tool, error) {
	return gitworkflow.NewCommitStagedTool(backend, store)
}

// NewProofCommitGuard contributes the terminal authority guard.
//
// @Bean(name="gitCommitGuard")
func NewProofCommitGuard(
	backend gitworkflow.Backend,
	store *gitworkflow.AuthorityStore,
) (stage.ToolDispatchGuard, error) {
	return gitworkflow.NewCommitGuard(backend, store)
}

// NewProof proves ordinary generated map and collection injection.
//
// @Bean(name="proof")
func NewProof(tools map[string]tool.Tool, guards []stage.ToolDispatchGuard) *Proof {
	return &Proof{
		tools:  tools,
		guards: append([]stage.ToolDispatchGuard(nil), guards...),
	}
}

// ToolNames returns the deterministically generated tool identities.
func (proof *Proof) ToolNames() []string {
	if proof == nil {
		return nil
	}
	result := make([]string, 0, len(proof.tools))
	for name := range proof.tools {
		result = append(result, name)
	}
	slices.Sort(result)
	return result
}

// GuardCount reports the generated terminal guard count.
func (proof *Proof) GuardCount() int {
	if proof == nil {
		return 0
	}
	return len(proof.guards)
}

var _ gitworkflow.Backend = (*proofBackend)(nil)
