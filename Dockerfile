# Multi-stage build: static Go binary into distroless, runs as nonroot
# with a read-only rootfs. Only the data volume is writable.
FROM golang:1.25 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /neabbs ./cmd/neabbs
# Stage an empty /data so the final image can own it as the nonroot uid;
# a fresh named volume inherits this ownership and is therefore writable.
RUN mkdir -p /data

FROM gcr.io/distroless/static:nonroot
COPY --from=build /neabbs /neabbs
COPY content /content
# distroless "nonroot" is uid/gid 65532; the builder has no such user by
# name, so chown numerically. The DB and hostkey are the only writes.
COPY --from=build --chown=65532:65532 /data /data
ENV NEABBS_LISTEN=:2222 \
    NEABBS_DB=/data/neabbs.db \
    NEABBS_HOSTKEY=/data/hostkey \
    NEABBS_CONTENT=/content
EXPOSE 2222
VOLUME /data
USER nonroot
ENTRYPOINT ["/neabbs"]
