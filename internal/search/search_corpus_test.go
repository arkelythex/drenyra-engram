// Corpus-policy tests for the search tokenize fuzz corpus (spec FR-2,
// AC-1/AC-2; design D-10 WU-4) plus the named tokenizer seed regressions
// (task 1.5 TRIANGULATE): emoji, NUL/control bytes and invalid UTF-8 must
// never panic the tokenizer and must yield deterministic, non-empty,
// separator-free tokens. No monetary value is involved (tokenizer input is
// free text; the repository keeps all money in whole int64 cents elsewhere).
package search

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// corpusHeader is the go fuzz corpus file header (go.dev corpus format, same
// one the go tool reads and writes).
const corpusHeader = "go test fuzz v1"

// searchCorpusEntries is the frozen committed corpus of FuzzSearchTokenize.
// Entries are permanent regressions: deleting one while its bug class exists
// fails TestFuzzSearchCorpusNeverDeleted (spec FR-2 / AC-2).
var searchCorpusEntries = []string{
	"seed_empty.bin",
	"seed_one_byte.bin",
	"seed_spanish_text.txt",
	"seed_emoji_and_punct.txt",
	"seed_nul_and_control.bin",
	"seed_invalid_utf8.bin",
	"seed_cap_max.bin",
	"seed_cap_just_below.bin",
}

func searchCorpusDir() string {
	return filepath.Join("testdata", "fuzz", "FuzzSearchTokenize")
}

// readSearchCorpusEntry reads one committed search corpus entry and decodes it
// from the go fuzz corpus format into the raw tokenizer input bytes.
func readSearchCorpusEntry(entry string) ([]byte, error) {
	data, err := os.ReadFile(filepath.Join(searchCorpusDir(), entry))
	if err != nil {
		return nil, err
	}
	lines := strings.Split(string(data), "\n")
	if len(lines) < 2 || lines[0] != corpusHeader {
		return nil, fmt.Errorf("corpus file is not in go fuzz corpus format")
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

// TestFuzzSearchCorpusNeverDeleted freezes the committed search corpus and
// proves every entry passes the frozen tokenize invariants. The same entries
// also replay through `go test ./...` seed mode.
func TestFuzzSearchCorpusNeverDeleted(t *testing.T) {
	for _, entry := range searchCorpusEntries {
		data, err := readSearchCorpusEntry(entry)
		if err != nil {
			t.Fatalf("corpus entry %s deleted or unreadable: %v", filepath.Join(searchCorpusDir(), entry), err)
		}
		checkTokenizeInvariants(t, data)
	}
	// The package-level fuzz README index is part of the committed corpus
	// (design D-10; it lives outside the target dir because the go tool parses
	// every file inside testdata/fuzz/<Target>/ as a corpus entry).
	if _, err := os.Stat(filepath.Join("testdata", "fuzz", "README.md")); err != nil {
		t.Fatalf("package fuzz README index missing: %v", err)
	}
}

// TestSearchTokenizeKnownBehavior is the named TRIANGULATE regression for the
// tokenizer's known edge classes: lowercased deterministic tokens, separator
// handling across emoji/NUL/invalid-UTF-8, and the empty-input boundary.
func TestSearchTokenizeKnownBehavior(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  []string
	}{
		{"empty input yields no tokens", "", nil},
		{"lowercased deterministic tokens", "café CAFÉ", []string{"café", "café"}},
		{"punctuation and digits are separated", "S/ 1,284.30 #2026-01", []string{"s", "1", "284", "30", "2026", "01"}},
		{"emoji and dash separators", "💸 transferencia — #2026-01 · confirmada ✅", []string{"transferencia", "2026", "01", "confirmada"}},
		{"nul and control separators", "a\x00b\x01c", []string{"a", "b", "c"}},
		{"invalid utf8 is a separator, never a token", string([]byte{0xff, 0xfe, 'a', 0xc3, 0x28, 'b'}), []string{"a", "b"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tokenize(tc.input)
			if len(got) != len(tc.want) {
				t.Fatalf("tokenize(%q) = %v, want %v", tc.input, got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("tokenize(%q) = %v, want %v", tc.input, got, tc.want)
				}
			}
			// The frozen invariants must hold for every known edge class.
			checkTokenizeInvariants(t, []byte(tc.input))
		})
	}
}
