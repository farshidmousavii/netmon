# syntax=docker/dockerfile:1

# Multi-stage build: a pinned Go build stage produces the bidar binary; the
# runtime stage contains only that binary. The SAME image runs bidar serve
# or bidar migrate — the command passed at `docker run`/compose time
# decides, there is no second image.
#
# NOTE: the container refuses to start without a real BIDAR_MASTER_KEY —
# the .env.example placeholder is deliberately invalid base64 so a
# copied-as-is deployment fails fast (see internal/crypto). Copy
# .env.example to .env and run `openssl rand -base64 32` first.

# ---- build stage ----
# Pinned to the exact Go version declared in go.mod (go 1.25.7).
FROM golang:1.25.7-alpine AS build
WORKDIR /src

# Resolve dependencies before copying source, so the layer is cached
# across builds.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# CGO_ENABLED=0: the dependency tree is pure Go (gosnmp, pgx, cobra,
# bubbletea) so this produces a fully static binary that runs on any base
# image. -trimpath + -s -w shrink and de-personalize the binary.
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/bidar ./cmd/bidar

# ---- runtime stage ----
# Alpine rather than distroless: gcr.io (distroless' only registry) is
# unreachable from this network (403 on pull), and alpine's shell gives
# operators a debugging console for the daemon. ca-certificates is pulled
# in now because Phase 1's LDAPS/AD provider will need it.
FROM alpine:3.21
RUN apk add --no-cache ca-certificates \
    && adduser -D -u 10001 bidar
COPY --from=build /out/bidar /usr/local/bin/bidar
USER bidar

ENTRYPOINT ["/usr/local/bin/bidar"]
# Default command; compose overrides with `migrate` for the one-shot job.
CMD ["serve"]
