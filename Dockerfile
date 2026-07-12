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

# Root base (no :nonroot). The daemon starts as root ONLY to fix ownership
# of a root-owned mounted volume, then permanently drops to uid/gid 65532
# before opening the DB (see cmd/neabbs/privileges_linux.go). It never
# serves as root. Run with --user 65532 to skip the root phase entirely
# when the volume is already writable by that uid.
FROM gcr.io/distroless/static
COPY --from=build /neabbs /neabbs
COPY content /content
# Pre-owned by the target uid so a fresh named volume is writable without
# the self-heal step; the builder has no such user by name, so chown
# numerically. The DB and hostkey are the only writes.
COPY --from=build --chown=65532:65532 /data /data
ENV NEABBS_LISTEN=:2222 \
    NEABBS_DB=/data/neabbs.db \
    NEABBS_HOSTKEY=/data/hostkey \
    NEABBS_CONTENT=/content \
    NEABBS_CERTS=/data/certs
EXPOSE 2222 80 443
VOLUME /data
ENTRYPOINT ["/neabbs"]
