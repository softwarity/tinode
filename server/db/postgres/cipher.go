//go:build postgres
// +build postgres

package postgres

// Message-content encryption at rest (softwarity fork).
//
// When TINODE_MSG_KEY is set to the base64 of a 32-byte key, messages.content is
// stored AES-256-GCM encrypted and transparently decrypted on read. When the
// variable is unset, content is stored and read verbatim (no encryption).
//
// Legacy plaintext messages stay readable in both modes: decodeContent only
// decrypts values that carry our {"_enc":...} wrapper and passes everything else
// through unchanged, so no migration is needed.
//
// Cost: the GCM cipher is built once at startup and reused for every message; with
// AES-NI the per-message cost is sub-microsecond, negligible next to the DB round
// trip. Scope is messages.content only — head, attachments and search are not
// covered (encrypting content disables server-side search).

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"log"
	"os"
)

// msgAEAD is nil when encryption is disabled (TINODE_MSG_KEY unset or invalid).
var msgAEAD cipher.AEAD

// encPrefix is the exact head of a wrapped value, used as an allocation-free probe
// on read. Drafty content is either a JSON string or an object keyed by txt/fmt/ent,
// so it never starts with this — the probe cannot misfire on real content.
var encPrefix = []byte(`{"_enc":`)

func init() {
	b64 := os.Getenv("TINODE_MSG_KEY")
	if b64 == "" {
		return // encryption disabled
	}
	key, err := base64.StdEncoding.DecodeString(b64)
	if err != nil || len(key) != 32 {
		log.Println("[cipher] TINODE_MSG_KEY must be base64 of exactly 32 bytes; message encryption DISABLED")
		return
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		log.Println("[cipher] cannot initialize AES:", err, "- message encryption DISABLED")
		return
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		log.Println("[cipher] cannot initialize GCM:", err, "- message encryption DISABLED")
		return
	}
	msgAEAD = gcm
	log.Println("[cipher] message content encryption ENABLED (AES-256-GCM)")
}

// encWrapper is the on-disk shape of an encrypted content. A []byte field marshals
// to base64 in JSON, so the stored value stays valid JSON: {"_enc":"<base64>"}.
type encWrapper struct {
	Enc []byte `json:"_enc"`
}

// encodeContent takes already-serialized JSON content and returns the value to
// store. With a key: {"_enc":"..."} holding nonce||ciphertext||tag. Without a key
// (or on any failure): the input unchanged, so nothing is ever lost.
func encodeContent(plain []byte) []byte {
	if msgAEAD == nil || len(plain) == 0 {
		return plain
	}
	nonce := make([]byte, msgAEAD.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		log.Println("[cipher] nonce generation failed, storing content in clear:", err)
		return plain
	}
	sealed := msgAEAD.Seal(nonce, nonce, plain, nil) // nonce||ciphertext||tag
	out, err := json.Marshal(encWrapper{Enc: sealed})
	if err != nil {
		log.Println("[cipher] wrapper marshal failed, storing content in clear:", err)
		return plain
	}
	return out
}

// decodeContent turns a stored content value back into a Drafty structure. It
// handles our encrypted wrapper, legacy plaintext, and NULL (returns nil).
func decodeContent(raw []byte) interface{} {
	if len(raw) == 0 {
		return nil
	}
	if bytes.HasPrefix(raw, encPrefix) {
		if msgAEAD == nil {
			log.Println("[cipher] encrypted message found but TINODE_MSG_KEY is not set")
			return nil
		}
		var w encWrapper
		if err := json.Unmarshal(raw, &w); err != nil || len(w.Enc) < msgAEAD.NonceSize() {
			log.Println("[cipher] malformed encrypted content")
			return nil
		}
		ns := msgAEAD.NonceSize()
		plain, err := msgAEAD.Open(nil, w.Enc[:ns], w.Enc[ns:], nil)
		if err != nil {
			log.Println("[cipher] decryption failed:", err)
			return nil
		}
		raw = plain
	}
	var content interface{}
	if err := json.Unmarshal(raw, &content); err != nil {
		log.Println("[cipher] content unmarshal failed:", err)
		return nil
	}
	return content
}
