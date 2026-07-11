# softwarity/tinode-postgres-rekey — one-shot re-encryption of stored message
# content onto the current msgcipher key (see tinode-rekey/main.go and
# deploy/README.md). Tiny multi-arch image: a single static Go binary.
#
# Run it as a Kubernetes Job or a Swarm one-shot service with the same key ring as
# the server (TINODE_MSG_KEY_* / _CURRENT) plus TINODE_REKEY_DSN. Idempotent and
# resumable. Build context is the repository root.

FROM --platform=$BUILDPLATFORM golang:1.26 AS build
ARG TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=$TARGETARCH go build -trimpath -o /out/tinode-rekey ./tinode-rekey

FROM alpine:3.22
RUN apk add --no-cache ca-certificates
COPY --from=build /out/tinode-rekey /usr/local/bin/tinode-rekey
# No args → run the migration. Pass -status to only report remaining work (exit 1
# while incomplete — the gate before dropping an old key), or -dry-run to scan
# without writing.
ENTRYPOINT ["tinode-rekey"]
