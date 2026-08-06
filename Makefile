.PHONY: tools-bootstrap proto fast check coverage verify verify-release

tools-bootstrap:
	go run ./internal/qualitygate -mode=tools-bootstrap

proto:
	go run ./internal/qualitygate -mode=proto

fast:
	go run ./internal/qualitygate -mode=fast

check:
	go run ./internal/qualitygate -mode=check

coverage:
	go run ./internal/qualitygate -mode=coverage

verify:
	go run ./internal/qualitygate -mode=verify

verify-release: verify
