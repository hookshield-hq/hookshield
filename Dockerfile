# HookShield all-in-one image: API, ingest, worker and the dashboard in one
# container behind one port (hookshield-server). Used by the one-click deploys
# (Render, Railway, Coolify) and small installs. Bigger installs run the
# separate images instead: backend/Dockerfile (api, ingest, worker) and
# web/Dockerfile — see deploy/docker-compose.yml.
#
#   docker build -t hookshield .
#   docker run -p 8000:8000 -e HS_DATABASE_URL=… -e HS_REDIS_URL=… -v hookshield-data:/data hookshield
#
# Routes: /v1/… API · /in/v1/{slug} ingest · everything else the dashboard.
# The encryption key is HS_ENCRYPTION_KEY, or generated on first start into
# /data/encryption.key (mount a volume there and back the file up).

# ── backend (cross-compiled: no emulation for multi-arch builds) ──
FROM --platform=$BUILDPLATFORM golang:1.24-alpine AS backend
WORKDIR /src
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ .
ARG TARGETOS TARGETARCH
ARG VERSION=dev
ARG COMMIT=none
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -tags timetzdata \
      -ldflags "-s -w \
        -X github.com/hookshield-hq/hookshield/backend/internal/version.Version=${VERSION} \
        -X github.com/hookshield-hq/hookshield/backend/internal/version.Commit=${COMMIT}" \
      -o /out/ ./cmd/hookshield-server ./cmd/api ./cmd/hookshield

# ── dashboard ──────────────────────────────────────────────
FROM node:22-alpine AS web-deps
WORKDIR /app
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund

FROM node:22-alpine AS web
WORKDIR /app
ENV NEXT_TELEMETRY_DISABLED=1
COPY --from=web-deps /app/node_modules ./node_modules
COPY web/ .
RUN npm run build

# ── runtime ────────────────────────────────────────────────
FROM node:22-alpine
ENV NODE_ENV=production NEXT_TELEMETRY_DISABLED=1 \
    HS_ENV=production PORT=8000 \
    HS_WEB_DIR=/app/web HS_ENCRYPTION_KEY_FILE=/data/encryption.key
COPY --from=web --chown=node:node /app/.next/standalone /app/web
COPY --from=web --chown=node:node /app/.next/static /app/web/.next/static
COPY --from=web --chown=node:node /app/public /app/web/public
COPY --from=backend /out/ /usr/local/bin/
RUN mkdir -p /data && chown node:node /data
USER node
EXPOSE 8000
HEALTHCHECK --interval=10s --timeout=3s --start-period=60s --retries=6 \
  CMD wget -qO- "http://127.0.0.1:${PORT}/readyz" >/dev/null || exit 1
ENTRYPOINT ["hookshield-server"]
