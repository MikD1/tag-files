# syntax=docker/dockerfile:1
ARG APP_IMAGE=tag-files-app:local
FROM golang:1.27.4-bookworm@sha256:4fa7c968ba93f1635670522dbb01a2c78150443e1385746924566079f36570ea AS build
WORKDIR /src/app
COPY app/go.mod app/go.sum ./
RUN go mod download
COPY app/ ./
COPY LICENSE /src/app/LICENSE
COPY scripts/collect-go-notices /usr/local/bin/collect-go-notices
WORKDIR /src/tools/migrate
COPY tools/migrate/go.mod tools/migrate/go.sum ./
RUN go mod download
COPY tools/migrate/ ./
RUN CGO_ENABLED=1 go build -trimpath -ldflags='-s -w' -o /out/tag-files-migrate ./cmd/tag-files-migrate \
    && collect-go-notices /out/notices ./cmd/tag-files-migrate \
    && go version -m /out/tag-files-migrate > /out/notices/go-buildinfo.txt

# The application runtime supplies the matching libc, FFprobe and license notices.
FROM ${APP_IMAGE}
COPY --from=build /out/tag-files-migrate /usr/local/bin/tag-files-migrate
COPY --from=build /out/notices /usr/share/tag-files/notices/migration
USER 0:0
ENTRYPOINT ["tag-files-migrate"]
CMD []
