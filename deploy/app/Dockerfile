# syntax=docker/dockerfile:1
# This foundation currently exposes media-runtime and media-check targets.
# The final server/SPA stage is added with application runtime wiring.
FROM --platform=$BUILDPLATFORM golang:1.27.4-alpine@sha256:0a5f7fe796599ff487605987432e7619ae45beeb2fba4c2ce0b907879932aebf AS media-tests
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src
COPY app/go.mod app/go.sum ./
RUN go mod download
COPY app/ ./
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go test -c -o /out/media-test ./internal/media \
    && CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go test -c -o /out/process-test ./internal/process

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
COPY --from=media-tests /out/media-test /out/process-test /usr/local/bin/
CMD ["/usr/local/bin/media-test", "-test.v", "-test.timeout=2m"]
