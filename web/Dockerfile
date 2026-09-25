# syntax=docker/dockerfile:1
# SPDX-License-Identifier: Apache-2.0
#
# dbr2-web — DBR² web console (Next.js standalone server).
#
#   docker build -f web/Dockerfile --build-arg BUILD=57 -t dbr2-web:0.1.0.57 web
#
# The console version is `$(cat VERSION).$BUILD` (ADR-0015); BUILD defaults to
# 0 for local builds and is the GitHub Actions run_number in CI. The API
# upstream is configured at RUN time with DBR2_API_INTERNAL_URL.

# Pinned by tag and digest; bump both together.
ARG NODE_IMAGE=node:22.23.3-alpine3.24@sha256:0a7108bf6c7bf5de370ffb1a3ed6be93d405b43ff159f681a8d18c0e2bc2e402

# ---------------------------------------------------------------------------
FROM ${NODE_IMAGE} AS deps
WORKDIR /app
COPY package.json package-lock.json ./
RUN npm ci --no-audit --no-fund

# ---------------------------------------------------------------------------
FROM ${NODE_IMAGE} AS builder
WORKDIR /app
ARG BUILD=0
ENV NEXT_TELEMETRY_DISABLED=1
COPY --from=deps /app/node_modules ./node_modules
COPY . .
RUN set -eu; \
    case "$BUILD" in ''|*[!0-9]*) echo "BUILD must be a non-negative integer, got '$BUILD'" >&2; exit 1;; esac; \
    base="$(tr -d '[:space:]' < VERSION)"; \
    echo "$base" | grep -Eq '^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$' \
      || { echo "web/VERSION must be MAJOR.MINOR.BUGFIX, got '$base'" >&2; exit 1; }; \
    version="$base.$((BUILD + 0))"; \
    echo "Building dbr2-web $version"; \
    printf '%s\n' "$version" > FULL_VERSION; \
    NEXT_PUBLIC_DBR2_VERSION="$version" npm run build

# ---------------------------------------------------------------------------
FROM ${NODE_IMAGE} AS runner
ARG BUILD=0
LABEL org.opencontainers.image.title="dbr2-web" \
      org.opencontainers.image.description="DBR² web console" \
      org.opencontainers.image.licenses="Apache-2.0" \
      org.opencontainers.image.source="https://github.com/AxiomOperator/dbr2"
WORKDIR /app
ENV NODE_ENV=production \
    NEXT_TELEMETRY_DISABLED=1 \
    PORT=3000 \
    HOSTNAME=0.0.0.0 \
    DBR2_API_INTERNAL_URL=http://dbr2-server:8080

# Application files are root-owned and read-only for the runtime user; only
# the Next.js cache directory is writable.
COPY --from=builder /app/.next/standalone ./
COPY --from=builder /app/.next/static ./.next/static
COPY --from=builder /app/public ./public
COPY --from=builder /app/FULL_VERSION ./VERSION
RUN mkdir -p .next/cache && chown node:node .next/cache

USER node
EXPOSE 3000
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD wget -q -O /dev/null "http://127.0.0.1:${PORT}/login" || exit 1
CMD ["node", "server.js"]
