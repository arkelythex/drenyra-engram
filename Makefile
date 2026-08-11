# Drenyra Engram — root build orchestration.
#
# The only CI-owned target here is fuzz-ci: exactly three frozen fuzz targets,
# each bounded to 30 seconds per target (spec FR-3 / design D-10). Unbounded
# fuzzing is an operator-local option and MUST never be a CI gate. The
# Makefile-contract test (internal/core/fuzz_ci_contract_test.go) pins these
# three invocations and forbids any unbudgeted fuzz invocation from being added.

.PHONY: fuzz-ci

fuzz-ci:
	go test ./internal/core -run '^$$' -fuzz='^FuzzParseComprobanteXML$$' -fuzztime=30s
	go test ./internal/core -run '^$$' -fuzz='^FuzzCanonicalReceiptPayload$$' -fuzztime=30s
	go test ./internal/search -run '^$$' -fuzz='^FuzzSearchTokenize$$' -fuzztime=30s
