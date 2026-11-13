# Build context is the repo root: ingest needs the generated proto module.
FROM golang:1.27.4-alpine3.24 AS build
WORKDIR /src
COPY proto/gen/go proto/gen/go
COPY ingest ingest
WORKDIR /src/ingest
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags=-s -o /sentinel-ingest ./cmd/sentinel-ingest

FROM scratch
COPY --from=build /sentinel-ingest /sentinel-ingest
USER 65532:65532
ENTRYPOINT ["/sentinel-ingest"]
