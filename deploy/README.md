# Deploying tinode-postgres-cipher

Deployment recipes for this fork — the encrypted Tinode image and its re-key tool.
They go together: the server ([`softwarity/tinode-postgres-cipher`](https://hub.docker.com/r/softwarity/tinode-postgres-cipher))
encrypts message content at rest, and the one-shot re-key
([`softwarity/tinode-postgres-rekey`](https://hub.docker.com/r/softwarity/tinode-postgres-rekey))
migrates messages between keys during a rotation.

- **Docker / Swarm** → [compose/](compose/)
- **Kubernetes (Helm)** → [helm/](helm/)

## What is encrypted

Only `messages.content` (the message body). It is stored AES-256-GCM encrypted and
transparently decrypted when served. `head` metadata, uploaded files and server-side
search are **not** covered — encrypting the body disables Tinode's message search.

This protects the body **at rest**: someone reading the database directly (a DBA, a
backup, `dbgate`) sees ciphertext, not text. It is *not* end-to-end encryption — the
server holds the keys, so it is not designed to resist an attacker who can read the
key material out of the running container.

## Generating a key

A key is 32 random bytes, base64-encoded:

```sh
openssl rand -base64 32
```

Use a **different key per deployment**.

## Two modes (plus off)

Configuration is just environment variables, and there are three states:

| Mode | Environment | Use it when |
|------|-------------|-------------|
| **Off** | *(no key set)* | you don't need encryption — content is stored in clear (stock Tinode) |
| **Single key** | `TINODE_MSG_KEY_1=<key>` | you just want content encrypted at rest, one key, no rotation |
| **Key ring** | `TINODE_MSG_KEY_1`, `TINODE_MSG_KEY_2` + `TINODE_MSG_KEY_CURRENT=<id>` | you want to be able to **rotate** keys without downtime |

One consistent style: keys are always numbered. **Single key** is just a ring of one —
set `TINODE_MSG_KEY_1` and you're done (`CURRENT` defaults to the only key). The same
variables configure the server and the re-key tool.

### The key ring (rotation mode)

```
TINODE_MSG_KEY_1=<base64 key>      # a key, by id
TINODE_MSG_KEY_2=<base64 key>
TINODE_MSG_KEY_CURRENT=2           # the id that encrypts NEW messages
```

Each encrypted message records the id that sealed it: `{"_enc":"…","k":"2"}`. On read,
that id selects the key from the ring, so keys coexist: new messages use the current
key, older ones stay readable via theirs. In practice you hold **two** keys during a
rotation (the old one and the new one) and one the rest of the time.

Content written before key ids existed has **no `k`** and is read as **id `1`**, so
messages already stored are readable as long as key 1 is set — no data migration.

> ⚠️ **`TINODE_MSG_KEY_CURRENT` must name a key that is actually set** (non-empty). If
> it points to a missing or empty key, the server logs
> `encryption DISABLED (decryption still works)` and stores **new content in clear** —
> it does *not* fail to start. Check that log line after changing keys. Likewise, a key
> whose value is empty is ignored (not added to the ring).

### Turning encryption on for an existing (plaintext) database

Enabling a key only encrypts **new** messages; the existing plaintext stays in clear
and readable. To encrypt the backlog too, run the re-key once — it brings **every**
message onto the current key, encrypting whatever is still in clear:

```sh
tinode-rekey -status   # "still in clear: N"
tinode-rekey           # encrypts them; then -status shows 0
```

## Rotating a key — the workflow

The reason the ring and the re-key tool exist: change the key without downtime, and —
when a key is compromised — get every message off it.

### State 0 — nominal, one key
`TINODE_MSG_KEY_1=<k1>`, `CURRENT=1`. Every message is on key 1. The secret holds `k1`.

### State 1 — a key is compromised
`k1` may have leaked. Goal: re-encrypt everything under a fresh key, then remove `k1`.

### State 2 — add the new key (zero downtime)
Generate `k2`. In the secret: add `TINODE_MSG_KEY_2=<k2>`, set `CURRENT=2`, **keep
`k1`**. Roll the pods.
→ New messages are sealed with `k2`; old ones remain `k:"1"`, still readable because
`k1` is still in the ring. Nothing has been rewritten yet, no outage.

### State 3 — run the re-key (migrate old → new)
Run the one-shot re-key with the **same ring** (`k1` + `k2`, `CURRENT=2`):

- **Kubernetes**: enable the Job — `helm upgrade … --set rekey.enabled=true`.
- **Swarm**: a one-shot service (`--restart-condition none`).
- **Compose**: `docker compose … run --rm rekey`.

It re-encrypts every message not already on id 2 (the `k:"1"` and the no-`k` ones)
onto `k2`, in batches. It is **idempotent** (rows already on the current key are
skipped) and **resumable** (just run it again after a crash). The live server keeps
serving throughout — it only writes current-key content, which the tool skips.

Verify completion — this is the gate:

```sh
tinode-rekey -status      # prints "to re-encrypt: N"; exits non-zero while N > 0
```

### State 4 — remove the old key
Once `-status` reports `to re-encrypt: 0`, drop `k1`: in the secret keep only
`TINODE_MSG_KEY_2` + `CURRENT=2`, roll the pods. `k1` is gone from the running system;
the compromise is contained.

> ⚠️ **Never reach state 4 before state 3 is complete.** Removing `k1` while any
> `k:"1"` message remains makes those messages permanently unreadable. `-status` is
> there precisely so you never guess.

Per-platform commands: [compose/README.md](compose/README.md) · [helm/README.md](helm/README.md).
