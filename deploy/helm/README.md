# Kubernetes (Helm) deployment

Chart for `softwarity/tinode-postgres-cipher` with encryption at rest and an
on-demand re-key Job. For the full rotation story (states 0→4) see
[../README.md](../README.md).

## Install

```sh
# generate keys
openssl rand -base64 32

helm install chat ./tinode \
  --set cipher.current=1 \
  --set cipher.keys.1=<base64-32-bytes> \
  --set postgres.dsn="postgres://tinode:tinode@postgres:5432/tinode?sslmode=disable"
```

The key ring is rendered into a Secret (`<release>-tinode-keys`) as
`TINODE_MSG_KEY_<id>` + `TINODE_MSG_KEY_CURRENT`, and the Deployment reads it via
`envFrom`. A checksum annotation rolls the pods automatically whenever the ring
changes. In production, manage that Secret out of band (sealed-secrets,
external-secrets, Vault) instead of passing keys on the command line.

## Rotate a key

1. **Add** the new key, make it current, keep the old one — then upgrade:
   ```sh
   helm upgrade chat ./tinode \
     --set cipher.keys.1=<old> --set cipher.keys.2=<new> --set cipher.current=2
   ```
   The checksum annotation triggers a rolling restart. No downtime; old messages stay
   readable via their id.

2. **Re-encrypt** onto the new key with the Job:
   ```sh
   helm upgrade chat ./tinode ... --set rekey.enabled=true      # same key flags as above
   kubectl logs -f job/chat-tinode-rekey-2                      # wait for "0 failed"
   kubectl run rekey-status --rm -it --restart=Never \
     --image=softwarity/tinode-postgres-rekey:latest \
     --overrides='{"spec":{"containers":[{"name":"s","image":"softwarity/tinode-postgres-rekey:latest","args":["-status"],"envFrom":[{"secretRef":{"name":"chat-tinode-keys"}}]}]}}'
   # ...or just check the Job succeeded; -status exits non-zero while work remains.
   helm upgrade chat ./tinode ... --set rekey.enabled=false     # turn the Job off
   ```

3. **Drop** the old key — upgrade with only the new key:
   ```sh
   helm upgrade chat ./tinode --set cipher.keys.2=<new> --set cipher.current=2
   ```

> ⚠️ Never drop the old key before the re-key Job has completed — remaining old-key
> messages would become unreadable. The `-status` mode is the gate: it exits non-zero
> while anything is left to migrate.
