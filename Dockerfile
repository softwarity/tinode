# softwarity/tinode-postgres-cipher — Tinode 0.25.3, PostgreSQL-only, with message
# content encryption at rest (see server/db/postgres/cipher.go and the README).
#
# Multi-arch (amd64 + arm64). The upstream image is amd64-only, so we do NOT inherit
# from it; instead:
#   * the two Go binaries (tinode, init-db) are cross-compiled per target arch
#     (pure Go, CGO disabled);
#   * every arch-independent runtime file (the TinodeWeb static assets, entrypoint,
#     config template, sample data, credentials.sh, /botdata) is copied out of the
#     upstream amd64 image — those are JS/shell/JSON, identical on every arch;
#   * the runtime is a plain multi-arch alpine, matching the upstream image's
#     packages, ENV, entrypoint, healthcheck and exposed ports.
# Net effect: same image as upstream 0.25.3, plus our patched binaries, on both arches.

# --- 1. Cross-compile our binaries for the requested target arch -------------------
FROM --platform=$BUILDPLATFORM golang:1.26 AS build
ARG TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# -tags postgres: Tinode registers each DB adapter behind a build tag; without it the
# binary has no adapter and dies at startup ("postgres adapter is not available").
RUN CGO_ENABLED=0 GOOS=linux GOARCH=$TARGETARCH go build -trimpath -tags postgres -o /out/tinode  ./server \
 && CGO_ENABLED=0 GOOS=linux GOARCH=$TARGETARCH go build -trimpath -tags postgres -o /out/init-db ./tinode-db

# --- 2. Arch-independent runtime files, taken from the upstream (amd64) image -------
FROM --platform=linux/amd64 tinode/tinode-postgres:0.25.3 AS upstream

# --- 3. Multi-arch runtime ----------------------------------------------------------
FROM alpine:3.22
LABEL org.opencontainers.image.title="tinode-postgres-cipher"
LABEL org.opencontainers.image.source="https://github.com/softwarity/tinode"

# Same runtime env defaults as the upstream image (its entrypoint renders the config
# template from these; kept complete so the image renders a valid default config even
# though our downstream NEO image supplies its own via EXT_CONFIG).
ENV VERSION=0.25 TARGET_DB=postgres STORE_USE_ADAPTER=postgres \
    WAIT_FOR= RESET_DB=false UPGRADE_DB=false NO_DB_INIT=false \
    SAMPLE_DATA=data.json DEFAULT_COUNTRY_CODE=US \
    MYSQL_DSN='root@tcp(mysql)/tinode?parseTime=true&collation=utf8mb4_0900_ai_ci' \
    POSTGRES_DSN='postgresql://postgres:postgres@localhost:5432/tinode?sslmode=disable&connect_timeout=10' \
    PLUGIN_PYTHON_CHAT_BOT_ENABLED=false \
    MEDIA_HANDLER=fs FS_CORS_ORIGINS='["*"]' AWS_CORS_ORIGINS='["*"]' \
    AWS_ACCESS_KEY_ID= AWS_SECRET_ACCESS_KEY= AWS_REGION= AWS_S3_BUCKET= AWS_S3_ENDPOINT= \
    SMTP_HOST_URL='http://localhost:6060' SMTP_SERVER= SMTP_PORT= SMTP_SENDER= \
    SMTP_LOGIN= SMTP_PASSWORD= SMTP_AUTH_MECHANISM= SMTP_HELO_HOST= \
    EMAIL_VERIFICATION_REQUIRED= DEBUG_EMAIL_VERIFICATION_CODE= SMTP_DOMAINS='' \
    API_KEY_SALT=T713/rYYgW7g4m3vG6zGRh7+FM1t0T8j13koXScOAj4= \
    AUTH_TOKEN_KEY=wfaY2RgF2S1OQI/ZlK+LSrp1KB2jwAdGAIHQ7JZn+Kc= \
    UID_ENCRYPTION_KEY=la6YsO+bNX/+XIkOqc5Svw== \
    TLS_ENABLED=false TLS_DOMAIN_NAME= TLS_CONTACT_ADDRESS= \
    FCM_PUSH_ENABLED=false FCM_API_KEY= FCM_APP_ID= FCM_SENDER_ID= FCM_PROJECT_ID= \
    FCM_VAPID_KEY= FCM_MEASUREMENT_ID= FCM_INCLUDE_ANDROID_NOTIFICATION=true \
    TNPG_PUSH_ENABLED=false TNPG_AUTH_TOKEN= TNPG_ORG= \
    WEBRTC_ENABLED=false ICE_SERVERS_FILE= \
    SERVER_STATUS_PATH='' ACC_GC_ENABLED=false

RUN apk add --no-cache ca-certificates bash grep

WORKDIR /opt/tinode
# All runtime files from upstream (static webapp, entrypoint.sh, config template,
# tinode.conf, data.json, credentials.sh, ...) — arch-independent.
COPY --from=upstream /opt/tinode/ /opt/tinode/
COPY --from=upstream /botdata/ /botdata/
# Overwrite the amd64 binaries with our per-arch, patched ones.
COPY --from=build /out/tinode /out/init-db /opt/tinode/
RUN chmod +x entrypoint.sh credentials.sh tinode init-db

HEALTHCHECK --interval=1m --timeout=3s --start-period=30s \
  CMD nc -z localhost 6060 || exit 1
ENTRYPOINT ["./entrypoint.sh"]
EXPOSE 6060 16060 12000-12003
