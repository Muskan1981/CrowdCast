# CrowdCast API - Distroless static non-root image
FROM gcr.io/distroless/static-debian12:nonroot

WORKDIR /

# Copy statically compiled binary
COPY api-linux /api

EXPOSE 8080

USER nonroot:nonroot

ENTRYPOINT ["/api"]
