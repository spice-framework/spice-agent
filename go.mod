module github.com/spice-framework/spice-agent

go 1.26.0

toolchain go1.26.5

tool (
	github.com/spice-framework/spice-agent/cmd/spice-agent-annotations
	github.com/spice-framework/toolchain/cmd/spice
	github.com/spice-framework/toolchain/cmd/spice-annotation-core
)

require (
	github.com/Microsoft/go-winio v0.6.2
	github.com/spice-framework/spice v0.1.0-preview.2
	golang.org/x/sys v0.47.0
	google.golang.org/grpc v1.83.0
	google.golang.org/protobuf v1.36.11
)

require (
	github.com/spice-framework/toolchain v0.1.0-preview.2 // indirect
	golang.org/x/mod v0.38.0 // indirect
	golang.org/x/net v0.57.0 // indirect
	golang.org/x/sync v0.22.0 // indirect
	golang.org/x/text v0.40.0 // indirect
	golang.org/x/tools v0.48.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260526163538-3dc84a4a5aaa // indirect
)
