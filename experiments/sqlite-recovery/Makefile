.PHONY: fast check verify benchmark generate

export GOWORK := off
export GOTOOLCHAIN := local
export GOFLAGS := -mod=vendor
export GOPROXY := off

generate:
	go tool github.com/spice-framework/toolchain/cmd/spice generate --target SQLiteRecoveryProof . ./internal/composition

fast:
	go test -shuffle=on -count=1 ./...

check:
	go mod tidy -diff
	go tool github.com/spice-framework/toolchain/cmd/spice generate --check --target SQLiteRecoveryProof . ./internal/composition
	go vet ./...
	go test -shuffle=on -count=1 ./...

verify: check
	go test -race -shuffle=on -count=1 ./...
	go test -cover .

benchmark:
	go test -run=^$$ -bench=^Benchmark -benchmem -benchtime=50x -count=5 -cpu=1 .
