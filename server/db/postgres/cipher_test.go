//go:build postgres
// +build postgres

package postgres

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"
)

// roundtrip: encodeContent then decodeContent must return the original Drafty.
func TestCipherRoundtrip(t *testing.T) {
	if msgAEAD == nil {
		t.Skip("TINODE_MSG_KEY not set; run with the key to exercise encryption")
	}
	cases := []interface{}{
		"hello world",
		map[string]interface{}{"txt": "hi", "fmt": []interface{}{map[string]interface{}{"at": float64(0), "len": float64(2)}}},
		map[string]interface{}{"ent": []interface{}{map[string]interface{}{"tp": "IM"}}},
	}
	for i, orig := range cases {
		plain, _ := json.Marshal(orig)
		stored := encodeContent(plain)
		if !bytes.HasPrefix(stored, encPrefix) {
			t.Fatalf("case %d: stored value is not wrapped: %s", i, stored)
		}
		if bytes.Contains(stored, plain) {
			t.Fatalf("case %d: plaintext leaked into stored value", i)
		}
		got := decodeContent(stored)
		if !reflect.DeepEqual(got, orig) {
			t.Fatalf("case %d: roundtrip mismatch\n want %#v\n got  %#v", i, orig, got)
		}
	}
}

// legacy plaintext (no wrapper) must pass through untouched, whether or not a key
// is configured — no migration needed.
func TestCipherLegacyPassthrough(t *testing.T) {
	for _, orig := range []interface{}{"old message", map[string]interface{}{"txt": "legacy"}} {
		plain, _ := json.Marshal(orig)
		got := decodeContent(plain)
		if !reflect.DeepEqual(got, orig) {
			t.Fatalf("legacy passthrough mismatch\n want %#v\n got %#v", orig, got)
		}
	}
}

// NULL content decodes to nil.
func TestCipherNil(t *testing.T) {
	if decodeContent(nil) != nil {
		t.Fatal("nil content must decode to nil")
	}
}
