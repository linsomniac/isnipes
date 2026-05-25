# PHASE8.md §11 — multi-stage build → tiny static image.
#
# Production deployments SHOULD pin the builder images by digest
# (node:20-alpine@sha256:..., golang:1.26-alpine@sha256:...) for
# reproducibility; tags are used here for readability.

# Stage 1: web build (esbuild bundle).
FROM node:20-alpine AS web
WORKDIR /web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN rm -rf dist && npm run build   # → /web/dist/{app.js,index.html}

# Stage 2: Go build with the client embedded.
FROM golang:1.26-alpine AS build
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
