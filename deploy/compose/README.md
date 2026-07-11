# Docker / Swarm deployment

Minimal `softwarity/tinode-postgres-cipher` stack with encryption at rest, plus the
one-shot re-key. For the full rotation story (states 0→4) see [../README.md](../README.md).

## Run

```sh
# generate real keys — do NOT ship the demo keys in the compose files
openssl rand -base64 32

docker compose up -d
# Tinode on http://localhost:6060
```

The key ring lives in `tinode`'s `environment:` in [docker-compose.yml](docker-compose.yml):
`TINODE_MSG_KEY_<id>` + `TINODE_MSG_KEY_CURRENT`. Unset it entirely to store content in
clear (upstream behaviour).

## Rotate a key

1. **Add** the new key and make it current, keep the old one:
   ```yaml
   TINODE_MSG_KEY_1: <old>
   TINODE_MSG_KEY_2: <new>
   TINODE_MSG_KEY_CURRENT: "2"
   ```
   `docker compose up -d` — no downtime, old messages still readable.

2. **Re-encrypt** everything onto the new key (see [rekey.yml](rekey.yml)):
   ```sh
   docker compose -f docker-compose.yml -f rekey.yml run --rm rekey -status   # what's left
   docker compose -f docker-compose.yml -f rekey.yml run --rm rekey           # migrate
   docker compose -f docker-compose.yml -f rekey.yml run --rm rekey -status   # must be 0
   ```

3. **Drop** the old key: remove `TINODE_MSG_KEY_1`, keep only key 2, `docker compose up -d`.

> ⚠️ Never remove the old key before `-status` reports `to re-encrypt: 0` — the
> remaining old-key messages would become unreadable.

On **Swarm** there is no `compose run`; run the re-key as a one-shot service
(`--restart-condition none`) — the exact command is in the header of [rekey.yml](rekey.yml).
