# HookShield backend image: one image, every binary (api, ingest, worker, hookshield-server, hookshield CLI).
# Pick the process with the container command/entrypoint, e.g. `api`, `ingest`, `worker`,
# `hookshield sink`.
#
#   docker build -t hookshield/backend ./backend

# ── build ─────────────────────────────────────────────────
# Cross-compiled on the build machine: multi-arch images need no emulation.
FROM --platform=$BUILDPLATFORM golang:1.24-alpine AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
ARG TARGETOS TARGETARCH
ARG VERSION=dev
ARG COMMIT=none
# timetzdata embeds the time zone database, so the runtime image needs no tzdata package.
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -tags timetzdata \
      -ldflags "-s -w \
        -X github.com/hookshield-hq/hookshield/backend/internal/version.Version=${VERSION} \
        -X github.com/hookshield-hq/hookshield/backend/internal/version.Commit=${COMMIT}" \
      -o /out/ ./cmd/...

# ── runtime ───────────────────────────────────────────────
# alpine already ships the CA bundle (ca-certificates-bundle) and busybox wget for
# healthchecks, so nothing is installed: the build needs no package repository.
FROM alpine:3.20
COPY --from=build /out/ /usr/local/bin/
# Where a fresh install keeps its generated encryption key when HS_ENCRYPTION_KEY
# is not set: mount one volume here, shared by api, ingest and worker.
RUN mkdir -p /var/lib/hookshield && chown 10001:10001 /var/lib/hookshield
ENV HS_ENCRYPTION_KEY_FILE=/var/lib/hookshield/encryption.key
# Safe defaults (SSRF protection on, Secure cookies). The dev compose sets development.
ENV HS_ENV=production
USER 10001:10001

# ingest 8080 · api 8081 · worker health 8082 · demo sink 4000
EXPOSE 8080 8081 8082 4000
ENTRYPOINT ["api"]
