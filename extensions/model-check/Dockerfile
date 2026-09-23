# Uses the BuildKit Dockerfile frontend bundled with Docker 25 or later.
# The browser revision must match package-lock.json. Pin the upstream image
# digest as well as its tag so Node, Chromium, fonts and native libraries stay
# identical between verification and publication.
ARG PLAYWRIGHT_IMAGE=mcr.microsoft.com/playwright:v1.62.1-noble@sha256:dcc5531e97840b9b5e794f2814476b21571c5124a3fca2267d73041f56e7580e
FROM ${PLAYWRIGHT_IMAGE} AS app

ENV NODE_ENV=production \
    PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD=1 \
    PLAYWRIGHT_BROWSERS_PATH=/ms-playwright \
    XDG_CACHE_HOME=/tmp/.cache \
    MODEL_CHECK_HOST=127.0.0.1 \
    MODEL_CHECK_PORT=8096 \
    MODEL_CHECK_DATA_DIR=/app/data \
    MODEL_CHECK_DEMO=0 \
    SUB2API_BACKEND=http://127.0.0.1:8080 \
    MODEL_CHECK_SITE_API_BASE=http://127.0.0.1:8080/v1

WORKDIR /app
COPY extensions/model-check/package.json extensions/model-check/package-lock.json ./
RUN --mount=type=cache,id=lumivia-model-check-npm,target=/root/.npm \
    npm ci --omit=dev --engine-strict --no-audit --no-fund

# Use a fixed application UID instead of the base image's distribution UID.
# Keep installed application code root-owned; Compose makes the complete
# filesystem (including the upstream browser installation) read-only.
RUN groupadd --gid 10001 modelcheck \
    && useradd --uid 10001 --gid modelcheck --create-home --shell /usr/sbin/nologin modelcheck \
    && install -d -o modelcheck -g modelcheck -m 0700 /app/data
COPY extensions/model-check/src/ ./src/
USER 10001:10001

# CI runs the complete suite in this target with --network none. Its real
# renderer exercises the exact Chromium, SQLite and sharp used in production.
FROM app AS test
ENV CI=true \
    NPM_CONFIG_CACHE=/tmp/npm-cache \
    NPM_CONFIG_UPDATE_NOTIFIER=false
COPY extensions/model-check/test/ ./test/
CMD ["npm", "test"]

FROM app AS runtime
ARG VERSION=dev
ARG COMMIT=unknown
LABEL org.opencontainers.image.title="Lumivia Model Check" \
      org.opencontainers.image.source="https://github.com/goldenfishs/sub2api" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${COMMIT}"

HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 \
    CMD ["node", "-e", "fetch('http://127.0.0.1:' + (process.env.MODEL_CHECK_PORT || '8096') + '/api/v1/model-check/health', { signal: AbortSignal.timeout(3000) }).then(async r => { const b = await r.json(); if (!r.ok || b.data?.status !== 'ok' || b.data.demo !== false) process.exit(1); }).catch(() => process.exit(1))"]

CMD ["node", "src/server.mjs"]
