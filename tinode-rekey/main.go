// Command tinode-rekey brings all stored message content onto the current key of the
// msgcipher key ring: content encrypted under another key is re-encrypted, and content
// still in clear (from before encryption was enabled) gets encrypted. It is the
// "retire an old key" / "encrypt the backlog" step: after you add a key and point
// TINODE_MSG_KEY_CURRENT at it, run this once (a k8s Job or a Swarm one-shot service)
// so every message ends up sealed under the current key; then you can drop any old key
// from the secret.
//
// It reads the same key ring as the server (TINODE_MSG_KEY_* env). The database is
// given either as a full URL in TINODE_REKEY_DSN, or — when that is empty — via the
// standard libpq variables PGHOST / PGPORT / PGUSER / PGPASSWORD / PGDATABASE /
// PGSSLMODE (handy in Kubernetes, where PGPASSWORD comes from a Secret). It is
// idempotent (rows already on the current key are skipped) and resumable (just run it
// again after a crash). The live server may keep serving throughout: it only ever
// writes current-key content, which this tool skips.
//
//	-status   report how many messages remain to migrate, then exit (no writes)
//	-batch N  rows read per round (default 500)
//	-dry-run  scan and report what would change, without writing
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/tinode/chat/server/msgcipher"
)

func main() {
	statusOnly := flag.Bool("status", false, "report remaining work and exit")
	dryRun := flag.Bool("dry-run", false, "scan and report without writing")
	batch := flag.Int("batch", 500, "rows read per round")
	flag.Parse()

	msgcipher.InitFromEnv()
	if !msgcipher.Enabled() {
		log.Fatalln("rekey: no current key — set TINODE_MSG_KEY_* and TINODE_MSG_KEY_CURRENT")
	}
	// Empty DSN → pgx falls back to the libpq PG* environment variables.
	dsn := os.Getenv("TINODE_REKEY_DSN")

	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		log.Fatalln("rekey: cannot connect (set TINODE_REKEY_DSN or PGHOST/PGUSER/PGPASSWORD/PGDATABASE):", err)
	}
	defer conn.Close(ctx)

	cur := msgcipher.CurrentID()
	if *statusOnly {
		mustStatus(ctx, conn, cur)
		return
	}
	rekey(ctx, conn, cur, *batch, *dryRun)
}

// mustStatus prints the encrypted/on-current/remaining counts and exits non-zero
// when migration is incomplete, so it doubles as a gate in scripts/CI before the old
// key is removed.
func mustStatus(ctx context.Context, conn *pgx.Conn, cur string) {
	var onCurrent, otherKey, plaintext int64
	err := conn.QueryRow(ctx, `
		SELECT
		  count(*) FILTER (WHERE content::jsonb ? '_enc' AND coalesce(content::jsonb->>'k','1') =  $1),
		  count(*) FILTER (WHERE content::jsonb ? '_enc' AND coalesce(content::jsonb->>'k','1') <> $1),
		  count(*) FILTER (WHERE NOT content::jsonb ? '_enc')
		FROM messages WHERE content IS NOT NULL`, cur).Scan(&onCurrent, &otherKey, &plaintext)
	if err != nil {
		log.Fatalln("rekey: status query failed:", err)
	}
	remaining := otherKey + plaintext
	fmt.Printf("current key id     : %s\n", cur)
	fmt.Printf("on current key     : %d\n", onCurrent)
	fmt.Printf("on another key     : %d\n", otherKey)
	fmt.Printf("still in clear     : %d\n", plaintext)
	fmt.Printf("to bring on current: %d\n", remaining)
	if remaining > 0 {
		os.Exit(1) // not done — do not drop the old key yet
	}
}

func rekey(ctx context.Context, conn *pgx.Conn, cur string, batch int, dryRun bool) {
	start := time.Now()
	var lastID int64
	var scanned, migrated, failed int64
	for {
		rows, err := conn.Query(ctx,
			`SELECT id, content FROM messages
			 WHERE content IS NOT NULL AND id > $1 ORDER BY id LIMIT $2`, lastID, batch)
		if err != nil {
			log.Fatalln("rekey: read failed:", err)
		}
		type job struct {
			id      int64
			content []byte
		}
		var todo []job
		n := 0
		for rows.Next() {
			var id int64
			var content []byte
			if err := rows.Scan(&id, &content); err != nil {
				rows.Close()
				log.Fatalln("rekey: scan failed:", err)
			}
			lastID = id
			n++
			scanned++
			if msgcipher.IsEncrypted(content) && msgcipher.KeyID(content) == cur {
				continue // already sealed under the current key
			}
			// Everything else is brought onto the current key: content encrypted under
			// another key is re-encrypted, and content still in clear (from before
			// encryption was enabled) gets encrypted.
			todo = append(todo, job{id, content})
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			log.Fatalln("rekey: read error:", err)
		}

		for _, j := range todo {
			plain, _, err := msgcipher.Decrypt(j.content)
			if err != nil {
				failed++
				log.Printf("rekey: skip id %d: %v", j.id, err)
				continue
			}
			if dryRun {
				migrated++
				continue
			}
			if _, err := conn.Exec(ctx,
				`UPDATE messages SET content = $1::json WHERE id = $2`,
				string(msgcipher.Encode(plain)), j.id); err != nil {
				failed++
				log.Printf("rekey: update id %d failed: %v", j.id, err)
				continue
			}
			migrated++
		}
		if n < batch {
			break // last page
		}
	}
	verb := "re-encrypted"
	if dryRun {
		verb = "would re-encrypt"
	}
	log.Printf("rekey done in %s: scanned %d, %s %d onto key %q, %d failed",
		time.Since(start).Round(time.Millisecond), scanned, verb, migrated, cur, failed)
	if failed > 0 {
		os.Exit(1)
	}
}
