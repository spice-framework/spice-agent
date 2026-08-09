PYTHON_FIXTURE_DIR := testdata/runtimeplugin/python

.PHONY: tools-bootstrap proto fast check coverage benchmark verify verify-python verify-release

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

benchmark:
	go run ./internal/qualitygate -mode=benchmark

verify:
	go run ./internal/qualitygate -mode=verify

verify-python: export SPICE_AGENT_PYTHON_CONFORMANCE=1
verify-python:
	uv lock --check --directory $(PYTHON_FIXTURE_DIR)
	uv sync --frozen --offline --directory $(PYTHON_FIXTURE_DIR)
	uv run --frozen --offline --directory $(PYTHON_FIXTURE_DIR) python generate_protocol.py
	uv run --frozen --offline --directory $(PYTHON_FIXTURE_DIR) python -W error::ResourceWarning -m unittest discover -s tests -v
	uv run --frozen --offline --directory $(PYTHON_FIXTURE_DIR) python -m compileall -q src tests generate_protocol.py
	go test ./internal/pluginconformanceacceptance -run TestIndependentPythonFixturePassesPublicConformance -count=1

verify-release: verify
