module github.com/spice-framework/spice-agent/experiments/compaction

go 1.26.0

toolchain go1.26.5

tool (
	github.com/spice-framework/spice-agent/cmd/spice-agent-annotations
	github.com/spice-framework/toolchain/cmd/spice
	github.com/spice-framework/toolchain/cmd/spice-annotation-core
)

require (
	github.com/spice-framework/spice v0.1.0-preview.2
	github.com/spice-framework/spice-agent v0.1.0-preview.5
)

require (
	github.com/spice-framework/toolchain v0.1.0-preview.2 // indirect
	golang.org/x/mod v0.38.0 // indirect
	golang.org/x/sync v0.22.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/tools v0.48.0 // indirect
)
