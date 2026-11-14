# syntax=docker/dockerfile:1
FROM --platform=$BUILDPLATFORM golang:1.27.4-alpine@sha256:0a5f7fe796599ff487605987432e7619ae45beeb2fba4c2ce0b907879932aebf AS build
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src
COPY app/go.mod app/go.sum ./
RUN go mod download
COPY app/ ./
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags=-s -o /out/storage-run ./cmd/storage-run \
    && CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags=-s -o /out/storage-check ./cmd/storage-check

FROM chrislusf/seaweedfs:4.53@sha256:ccad23e18639c8b98cee17e7100b95830a06077b56d8e5b006d6dcf8ec5e94a0
COPY deploy/storage/filer.toml /etc/seaweedfs/filer.toml
COPY --from=build /out/storage-run /out/storage-check /usr/local/bin/
WORKDIR /run/secrets
WORKDIR /data
USER root
STOPSIGNAL SIGTERM
HEALTHCHECK --interval=10s --timeout=10s --start-period=90s --retries=3 CMD ["storage-run", "check"]
ENTRYPOINT ["storage-run"]
CMD []
