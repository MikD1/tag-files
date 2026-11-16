# syntax=docker/dockerfile:1
FROM --platform=$BUILDPLATFORM node:26.13.0-alpine@sha256:8996bb5821d62a8fd9bb27c1cb07462f3fdce6e47d640c6a881ead0b064ce216 AS web-build
WORKDIR /web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

# This builder runs for the target platform so CGO uses that platform's libc.
# Debian's glibc ABI is also available in the pinned Ubuntu media runtime.
FROM golang:1.27.4-bookworm@sha256:4fa7c968ba93f1635670522dbb01a2c78150443e1385746924566079f36570ea AS app-build
WORKDIR /src
COPY app/go.mod app/go.sum ./
RUN go mod download
COPY app/ ./
ARG VERSION=development
RUN CGO_ENABLED=1 go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/tag-files ./cmd/tag-files

FROM --platform=$BUILDPLATFORM golang:1.27.4-alpine@sha256:0a5f7fe796599ff487605987432e7619ae45beeb2fba4c2ce0b907879932aebf AS media-tests
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src
COPY app/go.mod app/go.sum ./
RUN go mod download
COPY app/ ./
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go test -c -o /out/media-test ./internal/media \
    && CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go test -c -o /out/process-test ./internal/process \
    && CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go test -c -o /out/scratch-test ./internal/scratch

FROM ubuntu:26.04@sha256:065aa8626841f16057b78b027b464cd59c4fac36c204c72dcbf48d66f5fa8a96 AS media-runtime
RUN apt-get update \
    && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends ffmpeg=7:8.0.1-3ubuntu2 \
    && rm -rf /var/lib/apt/lists/* \
    && useradd --system --uid 10001 --home-dir /nonexistent tag-files \
    && mkdir -p /data /scratch \
    && chown 10001:10001 /data /scratch
ENV TMPDIR=/scratch
WORKDIR /data
USER 10001:10001

FROM media-runtime AS media-check
COPY --from=media-tests /out/media-test /out/process-test /out/scratch-test /usr/local/bin/
CMD ["/usr/local/bin/media-test", "-test.v", "-test.timeout=2m"]

FROM media-runtime AS app
ENV TAG_FILES_WEB_DIR=/usr/share/tag-files/web
COPY --from=app-build /out/tag-files /usr/local/bin/tag-files
COPY --from=web-build /web/dist /usr/share/tag-files/web
WORKDIR /data
STOPSIGNAL SIGTERM
HEALTHCHECK --interval=10s --timeout=10s --start-period=90s --retries=3 CMD ["tag-files", "check"]
EXPOSE 8080
ENTRYPOINT ["tag-files"]
CMD []
