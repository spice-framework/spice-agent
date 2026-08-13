# Authoring compiled extensions

Spice Agent extensions are ordinary versioned Go modules. They use public Go
contracts, compile into the application through Spice, and add no runtime
registry or package scan.

## Supported workflow

1. Start a separate Go module and pin released Agent and Toolchain versions.
   Do not use a workspace, `replace`, pseudo-version, or maintainer checkout.
2. Scaffold with Toolchain profile
   `compiled-tool-autoconfigure/v1alpha1-preview6`. The generated ownership
   manifest uses schema 6.
3. Implement a `tool.Tool` and an opt-in auto-configuration package using only
   public annotation and SDK contracts.
4. To install and configure the extension, run the generated debugger-style
   test, ordinary tests, a trimpath package
   build, and generation-current checks with a fresh cache. Disable the proxy
   before the final test and build.
5. Package a normal Go module release. Record the module and `go.mod` SumDB
   sums and authenticate its source commit.
6. To delete it, remove the activation import, tidy, regenerate, and
   checking that the extension module, sums, owned generated sources, and tool
   identity all disappear.

The three canonical released examples are
[`spice-agent-tool-text`](https://github.com/spice-framework/spice-agent-tool-text),
[`spice-agent-tool-json`](https://github.com/spice-framework/spice-agent-tool-json),
and
[`spice-agent-tool-integer`](https://github.com/spice-framework/spice-agent-tool-integer).
Their immutable versions, sums, workflows, platforms, and exact operational
steps are recorded in
[`compatibility/public-authoring.json`](../../compatibility/public-authoring.json)
and the
[clean-room evidence](../implementation/evidence/phase8-clean-room-public-authoring.md).

## Security and ownership

Compiled extensions are trusted supply-chain components and run with the
application's privileges. They must not hide network, filesystem, process, or
credential access. Tools declare effect, replay safety, and capabilities;
application policy remains responsible for granting authority. Generated files
belong only to the Toolchain ownership manifest and must never be hand-edited.
See the [threat model](../threat-model.md) and
[dependency review](../dependencies.md).
