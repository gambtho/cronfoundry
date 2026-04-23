FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags='-s -w' \
    -o /out/cronfoundry ./cmd/cronfoundry

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/cronfoundry /cronfoundry
USER nonroot
ENTRYPOINT ["/cronfoundry"]
