# PHASE8.md §11 — multi-stage build → tiny static image.
#
# Builder images are pinned by digest (tag kept for readability) so rebuilds
# are reproducible and a retagged/compromised base cannot slip in. Refresh
# the digests intentionally with `docker pull <tag>` + `docker inspect
# --format '{{index .RepoDigests 0}}' <tag>`.

# Stage 1: web build (esbuild bundle).
FROM node:20-alpine@sha256:fb4cd12c85ee03686f6af5362a0b0d56d50c58a04632e6c0fb8363f609372293 AS web
WORKDIR /web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN rm -rf dist && npm run build   # → /web/dist/{app.js,index.html}

# Stage 2: Go build with the client embedded.
FROM golang:1.26-alpine@sha256:91eda9776261207ea25fd06b5b7fed8d397dd2c0a283e77f2ab6e91bfa71079d AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# Take ONLY the freshly built web output (no stale/renamed chunk survives
# into the embed); then build a static, embedded binary.
RUN rm -rf cmd/isnipes/dist && mkdir -p cmd/isnipes/dist
COPY --from=web /web/dist/ ./cmd/isnipes/dist/
RUN CGO_ENABLED=0 GOOS=linux go build -tags embed -trimpath \
      -ldflags "-s -w" -o /isnipes ./cmd/isnipes

# Stage 3: minimal final image.
FROM scratch
COPY --from=build /isnipes /isnipes
# Public listener only; the admin (/metrics + pprof) listener stays on
# loopback and is not published.
EXPOSE 8080
# Run unprivileged (scratch has no /etc/passwd, so a numeric UID/GID).
USER 65534:65534
ENTRYPOINT ["/isnipes"]
CMD ["--addr=:8080", "--admin-addr=127.0.0.1:6060"]
