# syntax=docker/dockerfile:1.7

# Build stage. BuildKit cache mounts on /go/pkg/mod and /root/.cache/go-build
# let module downloads and compiled object files persist across builds — and
# across CI runs when paired with `cache-from: type=gha` in the workflow.
FROM golang:1.25-alpine AS build
WORKDIR /src

# Copy module manifests first so the download layer is cached on go.mod/go.sum
# changes only — not on every source edit.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go mod download

COPY . .

# Reuse the same cache mounts on compile so incremental builds don't re-link
# every package from scratch when only one file changed.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags='-s -w' \
    -o /out/cronfoundry ./cmd/cronfoundry

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/cronfoundry /cronfoundry
USER nonroot
ENTRYPOINT ["/cronfoundry"]
