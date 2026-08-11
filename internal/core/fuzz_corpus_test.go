// Corpus-policy tests for the W1 fuzz seed corpora (spec FR-2, AC-1/AC-2,
// NFR-2; design D-10 WU-4): the committed corpus is a permanent regression —
// entries must never be deleted while their bug class exists, every entry must
// stay green under the frozen invariants, and the corpus must contain no
// credentials, tokens, real customer data, or oversized files beyond the
// documented boundary seeds. No monetary value is computed here (all sizes are
// byte counts; the only money-adjacent seed is the PayableAmount
// trailing-garbage fixture, whose regression asserts the whole int64 cents
// total fails closed instead of being silently guessed).
package core

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// corpusHeader is the go fuzz corpus file header (go.dev corpus format, same
// one the go tool reads and writes).
const corpusHeader = "go test fuzz v1"

// coreCorpusTargets are the two core fuzz targets and their committed corpus
// manifests (spec FR-2 seed policy). Entries are frozen: deleting one while
// its bug class exists fails TestFuzzCoreCorpusManifestNeverDeleted.
var coreCorpusTargets = map[string][]string{
	"FuzzParseComprobanteXML": {
		"seed_valid_invoice.xml",
		"seed_empty.bin",
		"seed_one_byte.bin",
		"seed_truncated_invoice.xml",
		"seed_wrong_encoding.bin",
		"seed_deep_nesting.xml",
		"seed_amount_trailing_garbage.xml",
	},
	"FuzzCanonicalReceiptPayload": {
		"seed_valid_payload_v04.json",
		"seed_valid_payload_v09.json",
		"seed_empty.bin",
		"seed_one_byte.bin",
		"seed_partial_json.bin",
		"seed_not_json.bin",
	},
}

// coreCorpusDir returns the package-relative corpus directory of a core fuzz
// target (testdata/fuzz/<FuzzTargetName>/ — go.dev convention, design D-10).
func coreCorpusDir(target string) string {
	return filepath.Join("testdata", "fuzz", target)
}

// readCorpusFile reads one committed corpus entry from a core target and
// decodes it from the go fuzz corpus format into the raw input bytes — the
// exact inverse of what the go tool does when it replays the seed.
func readCorpusFile(target, entry string) ([]byte, error) {
	data, err := os.ReadFile(filepath.Join(coreCorpusDir(target), entry))
	if err != nil {
		return nil, err
	}
	return decodeCorpusFile(data)
}

// decodeCorpusFile decodes one corpus file from the go fuzz corpus format
// (header "go test fuzz v1" + one []byte("…") value line, as written by the go
// tool) into the raw fuzz input bytes.
func decodeCorpusFile(data []byte) ([]byte, error) {
	lines := strings.Split(string(data), "\n")
	if len(lines) < 2 || lines[0] != corpusHeader {
		return nil, fmt.Errorf("corpus file is not in go fuzz corpus format (%q header expected)", corpusHeader)
	}
	value := lines[1]
	if !strings.HasPrefix(value, "[]byte(") || !strings.HasSuffix(value, ")") {
		return nil, fmt.Errorf("corpus value is not a []byte literal: %q", value)
	}
	quoted := value[len("[]byte(") : len(value)-1]
	raw, err := strconv.Unquote(quoted)
	if err != nil {
		return nil, fmt.Errorf("corpus value does not unquote: %v", err)
	}
	return []byte(raw), nil
}

// mustReadCorpusSeedDir lists the committed entries of a core corpus target.
func mustReadCorpusSeedDir(t testing.TB, target string) []string {
	t.Helper()
	entries, err := os.ReadDir(coreCorpusDir(target))
	if err != nil {
		t.Fatalf("corpus dir %s unreadable: %v", coreCorpusDir(target), err)
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		out = append(out, e.Name())
	}
	return out
}

// TestFuzzCoreCorpusManifestNeverDeleted freezes the committed corpus and
// proves every entry still passes the frozen invariants (spec FR-2 / AC-2:
// corpus entries are permanent regressions — deleting one turns CI red). The
// same entries also replay through `go test ./...` seed mode.
func TestFuzzCoreCorpusManifestNeverDeleted(t *testing.T) {
	for target, entries := range coreCorpusTargets {
		t.Run(target, func(t *testing.T) {
			dir := coreCorpusDir(target)
			for _, entry := range entries {
				data, err := readCorpusFile(target, entry)
				if err != nil {
					t.Fatalf("corpus entry %s deleted or unreadable: %v", filepath.Join(dir, entry), err)
				}
				switch target {
				case "FuzzParseComprobanteXML":
					checkComprobanteInvariants(t, data)
				case "FuzzCanonicalReceiptPayload":
					if !jsonValidPayload(data) {
						// Non-payload seeds (empty, 1 byte, non-JSON) are
						// rejected deterministically by the harness.
						continue
					}
					var p ReceiptPayload
					if err := json.Unmarshal(data, &p); err != nil {
						t.Fatalf("seed %s does not unmarshal: %v", entry, err)
					}
					checkReceiptInvariants(t, p)
				}
			}
			// The package-level fuzz README index is part of the committed
			// corpus contract (design D-10: "a short README/index per target";
			// it lives outside the target dir because the go tool parses every
			// file inside testdata/fuzz/<Target>/ as a corpus entry).
			if _, err := os.Stat(filepath.Join("testdata", "fuzz", "README.md")); err != nil {
				t.Fatalf("package fuzz README index missing: %v", err)
			}
		})
	}
}

// TestFuzzCorpusSecuritySweep is the W1 security sweep (task 1.7, NFR-2):
// decoded seeds and READMEs must contain no credentials, tokens, or secret
// material, and oversized committed files must be exactly the documented
// boundary seeds (seed_cap_max.bin, seed_cap_just_below.bin in the search
// tokenize corpus).
func TestFuzzCorpusSecuritySweep(t *testing.T) {
	dirs := []string{
		coreCorpusDir("FuzzParseComprobanteXML"),
		coreCorpusDir("FuzzCanonicalReceiptPayload"),
		filepath.Join("..", "search", "testdata", "fuzz", "FuzzSearchTokenize"),
	}

	credentialPatterns := []*regexp.Regexp{
		regexp.MustCompile(`(?i)-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----`),
		regexp.MustCompile(`(?i)(secret|password|passwd|bearer)\s*[:=]\s*["']?\S+`),
		regexp.MustCompile(`(?i)api[_-]?key\s*[:=]\s*["']?\S+`),
		regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`),
		regexp.MustCompile(`\bsk-[A-Za-z0-9]{20,}\b`),
		regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{20,}\.[A-Za-z0-9_-]{20,}\.[A-Za-z0-9_-]{10,}\b`), // JWT
	}

	// Oversized-file policy: any committed corpus input larger than 512 KiB
	// must be one of the documented boundary seeds (NFR-2, WU-4 security). The
	// two boundary seeds are documented in the search corpus README.
	const oversizedThreshold = 512 << 10 // 512 KiB
	oversizedAllowed := map[string]bool{
		filepath.Join("..", "search", "testdata", "fuzz", "FuzzSearchTokenize", "seed_cap_max.bin"):        true,
		filepath.Join("..", "search", "testdata", "fuzz", "FuzzSearchTokenize", "seed_cap_just_below.bin"): true,
	}

	scanContent := func(name string, content []byte) {
		for _, re := range credentialPatterns {
			if re.Match(content) {
				t.Fatalf("credential pattern %q matched in %s", re.String(), name)
			}
		}
	}

	seen := make(map[string]bool)
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("sweep dir %s unreadable: %v", dir, err)
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			path := filepath.Join(dir, e.Name())
			seen[path] = true
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("sweep read %s: %v", path, err)
			}
			data, err := decodeCorpusFile(raw)
			if err != nil {
				t.Fatalf("corpus file %s is not in go fuzz corpus format: %v", path, err)
			}
			scanContent(path, data)
			if len(data) > oversizedThreshold && !oversizedAllowed[path] {
				t.Fatalf("oversized committed corpus input %s (%d bytes) is not a documented boundary seed", path, len(data))
			}
		}
	}

	// The package-level README indexes are also part of the sweep.
	scanContent("internal/core/testdata/fuzz/README.md", mustRead(t, filepath.Join("testdata", "fuzz", "README.md")))
	scanContent("internal/search/testdata/fuzz/README.md", mustRead(t, filepath.Join("..", "search", "testdata", "fuzz", "README.md")))

	// The two documented boundary seeds must be present and oversized.
	for path := range oversizedAllowed {
		if !seen[path] {
			t.Fatalf("documented boundary seed %s is missing from the corpus", path)
		}
		data, err := decodeCorpusFile(mustRead(t, path))
		if err != nil {
			t.Fatalf("decode boundary seed %s: %v", path, err)
		}
		if len(data) <= oversizedThreshold {
			t.Fatalf("boundary seed %s is %d decoded bytes, want > %d", path, len(data), oversizedThreshold)
		}
	}

	// The exact at-cap/just-below-cap sizes must be pinned (1 MiB cap).
	atCap := decodeSeed(t, "FuzzSearchTokenize", "seed_cap_max.bin")
	if len(atCap) != 1<<20 {
		t.Fatalf("at-cap boundary seed is %d decoded bytes, want exactly %d (1 MiB)", len(atCap), 1<<20)
	}
	justBelow := decodeSeed(t, "FuzzSearchTokenize", "seed_cap_just_below.bin")
	if len(justBelow) != 1<<20-1 {
		t.Fatalf("just-below boundary seed is %d decoded bytes, want exactly %d (1 MiB - 1)", len(justBelow), 1<<20-1)
	}
}

// decodeSeed decodes a corpus entry of a target outside this package (the
// search tokenizer corpus) and fails the test on any error.
func decodeSeed(t testing.TB, target, entry string) []byte {
	t.Helper()
	raw := mustRead(t, filepath.Join("..", "search", "testdata", "fuzz", target, entry))
	data, err := decodeCorpusFile(raw)
	if err != nil {
		t.Fatalf("decode %s/%s: %v", target, entry, err)
	}
	return data
}

func mustRead(t testing.TB, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return data
}

// jsonValidPayload reports whether data is well-formed JSON beginning with an
// object (the harness's accept condition for receipt payload documents).
func jsonValidPayload(data []byte) bool {
	var probe any
	if err := json.Unmarshal(data, &probe); err != nil {
		return false
	}
	return strings.HasPrefix(strings.TrimSpace(string(data)), "{")
}
