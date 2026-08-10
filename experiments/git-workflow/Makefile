.PHONY: fast check verify benchmark generate

export GOWORK := off
export GOTOOLCHAIN := local
export GOFLAGS := -mod=vendor
export GOPROXY := off

generate:
	go tool github.com/spice-framework/toolchain/cmd/spice generate --target GitWorkflowProof . ./internal/composition

fast:
	go test -shuffle=on -count=1 ./...

check:
	go mod tidy -diff
	go tool github.com/spice-framework/toolchain/cmd/spice generate --check --target GitWorkflowProof . ./internal/composition
	go vet ./...
	go test -shuffle=on -count=1 ./...

verify: check
	go test -race -shuffle=on -count=1 ./...
	go test -run=^$$ -fuzz=^FuzzDecodeCommitArguments$$ -fuzztime=100x .
	go test -cover ./...

benchmark:
	go test -run=^$$ -bench=. -benchmem -benchtime=500x -count=5 -cpu=1 .
