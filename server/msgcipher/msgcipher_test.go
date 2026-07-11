package msgcipher

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"io"
	"reflect"
	"testing"
)

func mkAEAD(t *testing.T, b byte) cipher.AEAD {
	t.Helper()
	blk, err := aes.NewCipher(bytes.Repeat([]byte{b}, 32))
	if err != nil {
		t.Fatal(err)
	}
	gcm, err := cipher.NewGCM(blk)
	if err != nil {
		t.Fatal(err)
	}
	return gcm
}

// withRing swaps the package ring for the duration of a test.
func withRing(t *testing.T, k map[string]cipher.AEAD, cur string) {
	t.Helper()
	sk, sc, si := keys, current, currentID
	t.Cleanup(func() { keys, current, currentID = sk, sc, si })
	keys, currentID, current = k, cur, k[cur]
}

// sealAs reproduces a stored wrapper under key `aead` with id `kid` (pass "" to omit
// the id, i.e. the old single-key format).
func sealAs(t *testing.T, aead cipher.AEAD, kid string, plain []byte) []byte {
	t.Helper()
	nonce := make([]byte, aead.NonceSize())
	io.ReadFull(rand.Reader, nonce)
	out, _ := json.Marshal(wrapper{Enc: aead.Seal(nonce, nonce, plain, nil), K: kid})
	return out
}

var samples = []interface{}{
	"hello world",
	map[string]interface{}{"txt": "hi", "fmt": []interface{}{map[string]interface{}{"at": float64(0), "len": float64(2)}}},
}

// Key-ring mode (keys 1,2 + current 2): new content is tagged k="2" and round-trips.
func TestRingRoundtrip(t *testing.T) {
	withRing(t, map[string]cipher.AEAD{"1": mkAEAD(t, 1), "2": mkAEAD(t, 2)}, "2")
	for i, orig := range samples {
		plain, _ := json.Marshal(orig)
		stored := Encode(plain)
		if KeyID(stored) != "2" {
			t.Fatalf("case %d: expected key id 2, got %q (%s)", i, KeyID(stored), stored)
		}
		if bytes.Contains(stored, plain) {
			t.Fatalf("case %d: plaintext leaked", i)
		}
		if got := Decode(stored); !reflect.DeepEqual(got, orig) {
			t.Fatalf("case %d: roundtrip mismatch: want %#v got %#v", i, orig, got)
		}
	}
}

// After rotating current 1 → 2, a message written under id 1 stays readable.
func TestRotation(t *testing.T) {
	k1, k2 := mkAEAD(t, 1), mkAEAD(t, 2)
	withRing(t, map[string]cipher.AEAD{"1": k1}, "1")
	stored := Encode([]byte(`"written-under-1"`))
	withRing(t, map[string]cipher.AEAD{"1": k1, "2": k2}, "2")
	if got := Decode(stored); got != "written-under-1" {
		t.Fatalf("id-1 message unreadable after rotation: %#v", got)
	}
}

// The old single-key format (no "k") is read as key id "1".
func TestOldFormatIsKey1(t *testing.T) {
	k1 := mkAEAD(t, 1)
	old := sealAs(t, k1, "", []byte(`"from-single-key"`))
	if bytes.Contains(old, []byte(`"k"`)) {
		t.Fatal("fixture should have no k field")
	}
	if KeyID(old) != "1" {
		t.Fatalf("no-k content must report key id 1, got %q", KeyID(old))
	}
	withRing(t, map[string]cipher.AEAD{"1": k1, "2": mkAEAD(t, 2)}, "2")
	if got := Decode(old); got != "from-single-key" {
		t.Fatalf("old single-key message not read as id 1: %#v", got)
	}
}

// A missing key id surfaces as ErrNoKey (rekey/status must not treat it as done).
func TestMissingKey(t *testing.T) {
	stored := sealAs(t, mkAEAD(t, 7), "7", []byte(`"x"`))
	withRing(t, map[string]cipher.AEAD{"2": mkAEAD(t, 2)}, "2")
	if _, enc, err := Decrypt(stored); !enc || err != ErrNoKey {
		t.Fatalf("expected ErrNoKey, got enc=%v err=%v", enc, err)
	}
}

// Legacy plaintext passes through; NULL decodes to nil.
func TestPassthrough(t *testing.T) {
	withRing(t, map[string]cipher.AEAD{"1": mkAEAD(t, 1)}, "1")
	for _, orig := range []interface{}{"old plaintext", map[string]interface{}{"txt": "legacy"}} {
		plain, _ := json.Marshal(orig)
		if got := Decode(plain); !reflect.DeepEqual(got, orig) {
			t.Fatalf("passthrough mismatch: want %#v got %#v", orig, got)
		}
	}
	if Decode(nil) != nil {
		t.Fatal("nil content must decode to nil")
	}
}
