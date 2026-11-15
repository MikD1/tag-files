# syntax=docker/dockerfile:1
FROM golang:1.27.4-alpine@sha256:0a5f7fe796599ff487605987432e7619ae45beeb2fba4c2ce0b907879932aebf AS build
RUN apk add --no-cache build-base
WORKDIR /src/app
COPY app/go.mod app/go.sum ./
RUN go mod download
COPY app/ ./
WORKDIR /src/tools/backup
COPY tools/backup/go.mod tools/backup/go.sum ./
RUN go mod download
COPY tools/backup/*.go ./
RUN --mount=type=cache,target=/root/.cache/go-build CGO_ENABLED=1 go build -trimpath -ldflags='-s -w -linkmode external -extldflags -static' -o /cold-backup .

FROM scratch
COPY --from=build /cold-backup /cold-backup
USER 0:0
ENTRYPOINT ["/cold-backup"]
