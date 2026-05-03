# syntax=docker/dockerfile:1.7
# ds-service container image for Fly.io.
#
# Layout:
#   /repo                       — read-only baked-in app code (token JSONs,
#                                 icon manifest, migrations, etc.)
#   /repo/services/ds-service/data
#                               — MOUNT POINT for the persistent Fly volume
#                                 (sqlite db + screen PNG cache).
#
# Why bake the whole repo: cmd/server/main.go reads
#   - services/ds-service/migrations/*.sql
#   - public/icons/glyph/manifest.json
#   - lib/tokens/indmoney/*.tokens.json
# all relative to REPO_DIR. Copying the whole tree (minus .dockerignore'd
# heavy bits) is simpler than wiring per-file COPYs that break every time
# someone adds a new asset path.

# ─── Stage 1: build the Go binary ────────────────────────────────────────────
FROM golang:1.25-alpine AS build
WORKDIR /src

# Cache the module download layer.
COPY services/ds-service/go.mod services/ds-service/go.sum ./services/ds-service/
WORKDIR /src/services/ds-service
RUN go mod download

# Pure-Go SQLite (modernc.org/sqlite) — CGO disabled so the binary is
# fully static and the runtime image stays slim.
COPY services/ds-service/ ./
ENV CGO_ENABLED=0 GOOS=linux
RUN go build -trimpath -ldflags="-s -w" -o /out/ds-service          ./cmd/server
RUN go build -trimpath -ldflags="-s -w" -o /out/revoke-token        ./cmd/revoke-token
RUN go build -trimpath -ldflags="-s -w" -o /out/mint-tokens         ./cmd/mint-tokens
RUN go build -trimpath -ldflags="-s -w" -o /out/backfill-lod        ./cmd/backfill-lod
RUN go build -trimpath -ldflags="-s -w" -o /out/set-version-status  ./cmd/set-version-status

# ─── Stage 2: runtime ────────────────────────────────────────────────────────
FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata

WORKDIR /repo
COPY --from=build /out/ds-service          /usr/local/bin/ds-service
COPY --from=build /out/revoke-token        /usr/local/bin/revoke-token
COPY --from=build /out/mint-tokens         /usr/local/bin/mint-tokens
COPY --from=build /out/backfill-lod        /usr/local/bin/backfill-lod
COPY --from=build /out/set-version-status  /usr/local/bin/set-version-status

# Ship the parts of the repo cmd/server reads at runtime. Everything not
# listed here is excluded by .dockerignore — keeps the image small.
COPY services/ds-service/migrations ./services/ds-service/migrations
COPY public/icons/glyph/manifest.json ./public/icons/glyph/manifest.json
COPY lib/tokens/indmoney ./lib/tokens/indmoney

# Volume mount target. Empty in the image; Fly attaches the persistent
# volume here at boot. cmd/server creates ds.db + screens/ on first run.
RUN mkdir -p /repo/services/ds-service/data

ENV REPO_DIR=/repo \
    PORT=8080
EXPOSE 8080

CMD ["ds-service"]
