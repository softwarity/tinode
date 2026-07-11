# softwarity/tinode — Tinode server rebuilt from source on top of the official
# runtime image.
#
# Why: the upstream image ships a prebuilt binary, so it cannot carry local
# patches (e.g. message-content encryption in the Postgres adapter). Here the
# `tinode` and `init-db` binaries are compiled from THIS repo and dropped into
# the official 0.25.3 runtime, which keeps the webapp, config template, tools
# and entrypoint unchanged. Only the two Go binaries differ from upstream.
#
# Pin FROM and the golang tag to the same Tinode release (0.25.3 / go.mod).
# The binary is amd64 to match the upstream runtime image (amd64-only); pure-Go
# build (CGO disabled), so cross-compiling from an arm64 host is free.

FROM --platform=$BUILDPLATFORM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ENV CGO_ENABLED=0 GOOS=linux GOARCH=amd64
# -tags postgres: Tinode registers each DB adapter behind a build tag
# (//go:build postgres). Without it the binary has no adapter and dies at
# startup with "postgres adapter is not available in this binary".
RUN go build -trimpath -tags postgres -o /out/tinode  ./server \
 && go build -trimpath -tags postgres -o /out/init-db ./tinode-db

FROM tinode/tinode-postgres:0.25.3
# Swap in the locally-built binaries; everything else stays from upstream.
COPY --from=build /out/tinode  /opt/tinode/tinode
COPY --from=build /out/init-db /opt/tinode/init-db
