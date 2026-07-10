# Multi-stage build: static Go binary into distroless, runs as nonroot
# with a read-only rootfs. Only the data volume is writable.
FROM golang:1.23 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /neabbs ./cmd/neabbs

FROM gcr.io/distroless/static:nonroot
COPY --from=build /neabbs /neabbs
COPY content /content
ENV NEABBS_LISTEN=:2222 \
    NEABBS_DB=/data/neabbs.db \
    NEABBS_HOSTKEY=/data/hostkey \
    NEABBS_CONTENT=/content
EXPOSE 2222
VOLUME /data
USER nonroot
ENTRYPOINT ["/neabbs"]
