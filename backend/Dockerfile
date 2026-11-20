# CrowdCast backend - multi-stage build with three targets:
#   api      (default) distroless, non-root, runs the API
#   loadgen  distroless, runs the virtual-viewer load generator
#   dev      Go toolchain + air, for hot reload in docker-compose.override.yml
#
# Stage "build" compiles both binaries, so no prebuilt files are needed on the host.

ARG GO_VERSION=1.26

FROM golang:${GO_VERSION}-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/api ./cmd/api \
 && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/loadgen ./cmd/loadgen

# ---- api ---------------------------------------------------------------------
FROM gcr.io/distroless/static-debian12:nonroot AS api
WORKDIR /
COPY --from=build /out/api /api
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/api"]

# ---- loadgen -----------------------------------------------------------------
FROM gcr.io/distroless/static-debian12:nonroot AS loadgen
WORKDIR /
COPY --from=build /out/loadgen /loadgen
USER nonroot:nonroot
ENTRYPOINT ["/loadgen"]

# ---- dev (hot reload) --------------------------------------------------------
FROM golang:${GO_VERSION}-alpine AS dev
RUN go install github.com/air-verse/air@v1.61.7
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
EXPOSE 8080 2345
CMD ["air", "-c", ".air.toml"]
