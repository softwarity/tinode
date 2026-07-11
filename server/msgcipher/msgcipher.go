// Package msgcipher implements Tinode message-content encryption at rest with a
// key ring for zero-downtime rotation (softwarity fork). It is shared verbatim by
// the PostgreSQL adapter (transparent encrypt on write / decrypt on read) and by the
// re-key tool, so both produce byte-identical wrappers.
//
// Keys are read from the environment:
//
//	TINODE_MSG_KEY_<id> = base64 of a 32-byte key   (one per id, e.g. _1, _2)
//	TINODE_MSG_KEY_CURRENT = <id>                    (which key encrypts new messages)
//	TINODE_MSG_KEY = base64 of a 32-byte key         (alias for id "1", zero-config
//	                                                  upgrade from the single-key image)
//
// Stored format: {"_enc":"<base64(nonce|ciphertext|tag)>","k":"<id>"}. Content with
// NO "k" is the old single-key format and is read as key id "1", so a single-key
// deployment adopts the ring with no data migration. Legacy plaintext (no wrapper)
// always passes through untouched.
package msgcipher

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log"
	"os"
	"strings"
)

// DefaultKeyID is the id of content with no "k" field (old single-key format) and of
// a bare TINODE_MSG_KEY.
const DefaultKeyID = "1"

var (
	keys      = map[string]cipher.AEAD{} // id → cipher
	current   cipher.AEAD                // key used to encrypt new content (nil ⇒ off)
	currentID string

	encPrefix = []byte(`{"_enc":`)

	// ErrNoKey is returned by Decrypt when a wrapped value references a key id that
	// is not in the ring.
	ErrNoKey = errors.New("msgcipher: no key for the message's key id")
)

func init() { InitFromEnv() }

// InitFromEnv (re)builds the ring from the environment. Called once at process
// start; exported so tools can rely on it explicitly.
func InitFromEnv() {
	keys = map[string]cipher.AEAD{}
	current, currentID = nil, ""

	for _, env := range os.Environ() {
		name, val, _ := strings.Cut(env, "=")
		if name == "TINODE_MSG_KEY" {
			addKey(DefaultKeyID, val)
		} else if id, ok := strings.CutPrefix(name, "TINODE_MSG_KEY_"); ok && id != "CURRENT" {
			addKey(id, val)
		}
	}
	if len(keys) == 0 {
		return // disabled
	}

	cur := os.Getenv("TINODE_MSG_KEY_CURRENT")
	if cur == "" {
		if len(keys) == 1 {
			for id := range keys {
				cur = id
			}
		} else {
			log.Println("[cipher] several keys but TINODE_MSG_KEY_CURRENT is not set; encryption DISABLED (decryption still works)")
			return
		}
	}
	aead, ok := keys[cur]
	if !ok {
		log.Printf("[cipher] TINODE_MSG_KEY_CURRENT=%q has no matching key; encryption DISABLED (decryption still works)", cur)
		return
	}
	current, currentID = aead, cur
	log.Printf("[cipher] message content encryption ENABLED (AES-256-GCM, current key id %q, %d key(s) in ring)", cur, len(keys))
}

func addKey(id, b64 string) {
	if b64 == "" {
		return
	}
	key, err := base64.StdEncoding.DecodeString(b64)
	if err != nil || len(key) != 32 {
		log.Printf("[cipher] key id %q must be base64 of exactly 32 bytes; ignored", id)
		return
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		log.Printf("[cipher] key id %q: cannot init AES: %v; ignored", id, err)
		return
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		log.Printf("[cipher] key id %q: cannot init GCM: %v; ignored", id, err)
		return
	}
	keys[id] = gcm
}

// Enabled reports whether a current key is configured (i.e. new content is encrypted).
func Enabled() bool { return current != nil }

// CurrentID is the id new content is encrypted under ("" when disabled).
func CurrentID() string { return currentID }

// wrapper is the on-disk shape. A []byte marshals to base64, keeping valid JSON.
type wrapper struct {
	Enc []byte `json:"_enc"`
	K   string `json:"k,omitempty"`
}

// Encode wraps already-serialized JSON content for storage. With a current key: the
// encrypted wrapper tagged with its id. Without a key (or on any failure): the input
// unchanged, so nothing is ever lost.
func Encode(plain []byte) []byte {
	if current == nil || len(plain) == 0 {
		return plain
	}
	nonce := make([]byte, current.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		log.Println("[cipher] nonce generation failed, storing content in clear:", err)
		return plain
	}
	sealed := current.Seal(nonce, nonce, plain, nil)
	out, err := json.Marshal(wrapper{Enc: sealed, K: currentID})
	if err != nil {
		log.Println("[cipher] wrapper marshal failed, storing content in clear:", err)
		return plain
	}
	return out
}

// IsEncrypted reports whether a stored value is one of our wrappers.
func IsEncrypted(raw []byte) bool { return bytes.HasPrefix(raw, encPrefix) }

// KeyID returns the key id a wrapped value is encrypted under (DefaultKeyID when the
// wrapper carries no "k"). Only meaningful when IsEncrypted(raw) is true.
func KeyID(raw []byte) string {
	var w wrapper
	if json.Unmarshal(raw, &w) != nil {
		return ""
	}
	if w.K == "" {
		return DefaultKeyID
	}
	return w.K
}

// Decrypt returns the plaintext JSON bytes for a stored value. For non-wrapped
// content (legacy plaintext or NULL) it returns the input unchanged with
// encrypted=false. Errors only on a wrapper whose key id is missing from the ring
// or whose ciphertext is corrupt.
func Decrypt(raw []byte) (plain []byte, encrypted bool, err error) {
	if len(raw) == 0 || !bytes.HasPrefix(raw, encPrefix) {
		return raw, false, nil
	}
	var w wrapper
	if err := json.Unmarshal(raw, &w); err != nil {
		return nil, true, err
	}
	kid := w.K
	if kid == "" {
		kid = DefaultKeyID
	}
	aead, ok := keys[kid]
	if !ok {
		return nil, true, ErrNoKey
	}
	if len(w.Enc) < aead.NonceSize() {
		return nil, true, errors.New("msgcipher: ciphertext too short")
	}
	ns := aead.NonceSize()
	out, err := aead.Open(nil, w.Enc[:ns], w.Enc[ns:], nil)
	if err != nil {
		return nil, true, err
	}
	return out, true, nil
}

// Decode is the adapter's read path: Decrypt then unmarshal to a Drafty value.
// Returns nil on any failure (matching the previous behaviour).
func Decode(raw []byte) interface{} {
	plain, _, err := Decrypt(raw)
	if err != nil {
		log.Println("[cipher] decode failed:", err)
		return nil
	}
	if len(plain) == 0 {
		return nil
	}
	var content interface{}
	if err := json.Unmarshal(plain, &content); err != nil {
		log.Println("[cipher] content unmarshal failed:", err)
		return nil
	}
	return content
}
