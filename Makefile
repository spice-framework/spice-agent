.PHONY: fast check coverage verify verify-release

fast:
	go run ./internal/qualitygate -mode=fast

check:
	go run ./internal/qualitygate -mode=check

coverage:
	go run ./internal/qualitygate -mode=coverage

verify:
	go run ./internal/qualitygate -mode=verify

verify-release: verify
