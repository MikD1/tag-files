# syntax=docker/dockerfile:1
ARG APP_IMAGE=tag-files-app:local
FROM golang:1.27.4-bookworm@sha256:4fa7c968ba93f1635670522dbb01a2c78150443e1385746924566079f36570ea AS build
WORKDIR /src/app
COPY app/go.mod app/go.sum ./
RUN go mod download
COPY app/ ./
WORKDIR /src/tools/migrate
COPY tools/migrate/go.mod tools/migrate/go.sum ./
RUN go mod download
COPY tools/migrate/ ./
RUN CGO_ENABLED=1 go build -trimpath -ldflags='-s -w' -o /out/tag-files-migrate ./cmd/tag-files-migrate \
    && CGO_ENABLED=1 go build -trimpath -ldflags='-s -w' -o /out/tag-files-migration-host ./cmd/tag-files-migration-host

# The application runtime supplies the matching libc and FFprobe.
FROM ${APP_IMAGE}
COPY --from=build /out/tag-files-migrate /out/tag-files-migration-host /usr/local/bin/
COPY compose.yml /usr/share/tag-files/compose.yml
HEALTHCHECK NONE
USER 0:0
ENTRYPOINT ["tag-files-migrate"]
CMD []
