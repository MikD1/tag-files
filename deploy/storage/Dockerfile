# syntax=docker/dockerfile:1
FROM chrislusf/seaweedfs:4.53@sha256:ccad23e18639c8b98cee17e7100b95830a06077b56d8e5b006d6dcf8ec5e94a0 AS upstream

FROM --platform=$BUILDPLATFORM golang:1.27.4-alpine@sha256:0a5f7fe796599ff487605987432e7619ae45beeb2fba4c2ce0b907879932aebf AS build
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src
COPY app/go.mod app/go.sum ./
RUN go mod download
COPY app/ ./
COPY scripts/collect-go-notices scripts/collect-seaweed-notices /usr/local/bin/
COPY deploy/storage/notices/go-modules /source-notices/
COPY LICENSE /out/notices/LICENSE
COPY --from=upstream /usr/bin/weed /upstream/weed
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags=-s -o /out/storage-run ./cmd/storage-run \
    && CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags=-s -o /out/storage-check ./cmd/storage-check \
    && CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH collect-go-notices /out/notices/go ./cmd/storage-run ./cmd/storage-check \
    && cp /usr/local/go/LICENSE /out/notices/GO-LICENSE \
    && go version -m /out/storage-run > /out/notices/storage-run.buildinfo.txt \
    && go version -m /out/storage-check > /out/notices/storage-check.buildinfo.txt
RUN --mount=type=cache,id=tag-files-upstream-notices-v1,target=/go/pkg/mod COLLECT_GO_NOTICES_EXTRA_DIR=/source-notices collect-seaweed-notices /upstream/weed /out/notices/upstream/seaweedfs

FROM alpine:3.24.4@sha256:3f4d6b32e26703c6082839234f43769d5046b3ac7c910033911d1e71d8cd630a AS runtime-files
# Keep the same Alpine/musl/BusyBox runtime family without distributing unused
# Rust, FUSE, curl, OpenSSL or package-manager executables in ancestor layers.
RUN addgroup -g 1000 seaweed && adduser -D -u 1000 -G seaweed seaweed \
    && mkdir -p /runtime/bin /runtime/lib /runtime/etc/ssl/certs /runtime/tmp \
    && chmod 1777 /runtime/tmp \
    && cp /bin/busybox /runtime/bin/ \
    && for applet in $(/bin/busybox --list); do ln -s busybox /runtime/bin/$applet; done \
    && cp -L /lib/ld-musl-*.so.1 /runtime/lib/ \
    && cp /etc/passwd /etc/group /runtime/etc/ \
    && mkdir -p /runtime/usr/share/tag-files/notices \
    && awk 'BEGIN {RS=""} /\nP:(busybox|musl|ca-certificates-bundle)\n/ {print}' /lib/apk/db/installed > /runtime/usr/share/tag-files/notices/runtime-packages.txt
COPY --from=upstream /etc/ssl/certs/ca-certificates.crt /runtime/etc/ssl/certs/ca-certificates.crt
RUN sha256sum /runtime/bin/busybox /runtime/lib/ld-musl-*.so.1 /runtime/etc/ssl/certs/ca-certificates.crt > /runtime/usr/share/tag-files/notices/runtime-files.sha256

FROM scratch
COPY --from=runtime-files /runtime/ /
COPY --from=upstream /usr/bin/weed /usr/bin/weed
COPY deploy/storage/filer.toml /etc/seaweedfs/filer.toml
COPY compose.yml /usr/share/tag-files/compose.yml
COPY --from=build /out/storage-run /out/storage-check /usr/local/bin/
COPY --from=build /out/notices /usr/share/tag-files/notices
COPY deploy/storage/notices /usr/share/tag-files/notices/runtime
ENV PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
WORKDIR /run/secrets
WORKDIR /data
USER root
VOLUME /data
EXPOSE 18080 18888 19333 7333 8080 8333 8888 9333
STOPSIGNAL SIGTERM
HEALTHCHECK --interval=10s --timeout=10s --start-period=90s --retries=3 CMD ["storage-run", "check"]
ENTRYPOINT ["storage-run"]
CMD []
