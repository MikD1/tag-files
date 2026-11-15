#!/bin/sh
set -eu

SETUP_VERSION='@TAG_FILES_VERSION@'
SETUP_REPOSITORY='@TAG_FILES_REPOSITORY@'

fail() { printf 'Tag Files setup: %s\n' "$*" >&2; exit 1; }
usage() {
    printf '%s\n' 'Usage: sh setup.sh [options]' \
        '  --directory PATH     New deployment directory (default: $HOME/tag-files)' \
        '  --version VERSION    Stable release version (embedded in release downloads)' \
        '  --app-image IMAGE    Application image override' \
        '  --storage-image IMAGE  Storage image override' \
        '  --skip-pull          Use images already loaded in Docker' \
        '  --project NAME       New Compose project (default: tag-files)' \
        '  --port PORT          Loopback HTTP port (default: 8080)' '  --help'
}
version=$SETUP_VERSION directory=${HOME:?HOME must be set}/tag-files
app_image= storage_image= project=tag-files port=8080 pull=yes
while [ "$#" -gt 0 ]; do
    case "$1" in
        --help|-h) usage; exit 0 ;;
        --skip-pull) pull=no; shift; continue ;;
        --directory|--version|--app-image|--storage-image|--project|--port)
            [ "$#" -ge 2 ] && [ -n "$2" ] || fail "$1 requires a value" ;;
        *) fail "unknown option: $1 (see --help)" ;;
    esac
    case "$1" in
        --directory) directory=$2 ;; --version) version=$2 ;;
        --app-image) app_image=$2 ;; --storage-image) storage_image=$2 ;;
        --project) project=$2 ;; --port) port=$2 ;;
    esac
    shift 2
done
case "$version" in @*) fail 'download setup.sh from a release, or supply --version and both image overrides for development' ;; esac
release=${version#v}
case "$release" in ''|*[!0-9.]*|.*|*.|*..*) fail 'version must be stable major.minor.patch' ;; esac
remaining=$release
for position in 1 2 3; do
    part=${remaining%%.*}
    case "$part" in ''|0[0-9]*) fail 'version must be stable major.minor.patch' ;; esac
    if [ "$position" -lt 3 ]; then
        [ "$remaining" != "$part" ] || fail 'version must be stable major.minor.patch'
        remaining=${remaining#*.}
    else
        [ "$remaining" = "$part" ] || fail 'version must be stable major.minor.patch'
    fi
done
version=v$release
case "$project" in ''|[!a-z0-9]*|*[!a-z0-9_-]*) fail 'project must use lowercase letters, digits, underscores or hyphens' ;; esac
case "$port" in ''|*[!0-9]*) fail 'port must be an integer from 1 to 65535' ;; esac
[ "$port" -ge 1 ] && [ "$port" -le 65535 ] || fail 'port must be from 1 to 65535'
if [ -z "$app_image" ] || [ -z "$storage_image" ]; then
    case "$SETUP_REPOSITORY" in @*) fail 'use a release download, or supply both --app-image and --storage-image' ;; esac
    repository=$(printf '%s' "$SETUP_REPOSITORY" | tr '[:upper:]' '[:lower:]')
    app_image=${app_image:-ghcr.io/$repository-app:$release}
    storage_image=${storage_image:-ghcr.io/$repository-storage:$release}
fi
case "$directory" in /*) ;; *) directory=$(pwd -P)/$directory ;; esac
case "$directory" in *','*|*'
'*) fail 'directory cannot contain commas or newlines' ;; esac
[ ! -L "$directory" ] || fail 'deployment directory must not be a symlink'
if [ -e "$directory" ]; then
    [ -d "$directory" ] || fail 'deployment directory must be an empty directory'
    for entry in "$directory"/* "$directory"/.[!.]* "$directory"/..?*; do
        if [ -e "$entry" ] || [ -L "$entry" ]; then fail 'deployment directory must be empty'; fi
    done
fi
command -v docker >/dev/null 2>&1 || fail 'install Docker with Compose v2 before running setup'
docker compose version >/dev/null 2>&1 || fail 'Docker Compose v2 is required'
# Explicit deployment files must control Compose even in a customized shell.
unset COMPOSE_FILE COMPOSE_PROJECT_NAME COMPOSE_PROFILES COMPOSE_ENV_FILES COMPOSE_PATH_SEPARATOR
unset TAG_FILES_APP_IMAGE TAG_FILES_STORAGE_IMAGE TAG_FILES_PORT TAG_FILES_MIN_FREE_BYTES TAG_FILES_UID TAG_FILES_GID
platform() {
    case "$1" in
        'linux amd64'|'linux x86_64') printf '%s' 'linux amd64' ;;
        'linux arm64'|'linux aarch64') printf '%s' 'linux arm64' ;;
        *) fail "unsupported native Docker platform: $1" ;;
    esac
}
host=$(docker info --format '{{.OSType}} {{.Architecture}}') || fail 'cannot contact Docker'
native=$(platform "$host") || exit 1
unused_project() {
    containers=$(docker ps -aq --filter "label=com.docker.compose.project=$project") || fail 'cannot inspect Compose project containers'
    [ -z "$containers" ] || fail 'Compose project already has containers; choose a fresh project'
    volumes=$(docker volume ls -q --filter "label=com.docker.compose.project=$project") || fail 'cannot inspect Compose project volumes'
    [ -z "$volumes" ] || fail 'Compose project already has volumes; choose a fresh project'
    volumes=$(docker volume ls -q) || fail 'cannot inspect named volumes'
    for suffix in app_data storage_data scratch; do
        case "
$volumes
" in *"
${project}_${suffix}
"*) fail "named volume ${project}_${suffix} already exists; choose a fresh project" ;; esac
    done
}
unused_project
if [ "$pull" = yes ]; then
    docker pull "$app_image" || fail 'application image pull failed'
    docker pull "$storage_image" || fail 'storage image pull failed'
fi
image() {
    id=$(docker image inspect --format '{{.Id}}' "$1") || fail "cannot inspect image: $1"
    hash=${id#sha256:}
    case "$id" in sha256:*) ;; *) fail "expected exact image ID for $1" ;; esac
    case "$hash" in ''|*[!0-9a-f]*) fail "invalid exact image ID for $1" ;; esac
    [ "${#hash}" -eq 64 ] || fail "invalid exact image ID for $1"
    metadata=$(docker image inspect --format '{{.Os}} {{.Architecture}}' "$id") || fail "cannot inspect image platform: $1"
    candidate=$(platform "$metadata") || exit 1
    [ "$candidate" = "$native" ] || fail "image $1 does not match native Docker platform $native"
    printf '%s' "$id"
}
app_id=$(image "$app_image") || exit 1
storage_id=$(image "$storage_image") || exit 1
unused_project
umask 077
mkdir -p "$directory" || fail 'cannot create deployment directory'
directory=$(CDPATH= cd -P "$directory" && pwd -P) || fail 'cannot resolve deployment directory'
docker run --rm --network none --user 0:0 --entrypoint storage-run \
    --mount "type=bind,src=$directory,dst=/install" "$storage_id" setup \
    --directory /install --version "$version" --app-image "$app_id" \
    --storage-image "$storage_id" --project "$project" --port "$port" || fail 'configuration setup failed; inspect the deployment directory before retrying'
unused_project
docker compose --project-directory "$directory" --env-file "$directory/.env" \
    -f "$directory/compose.yml" up -d --wait --wait-timeout 180 || fail 'startup failed; configuration is retained for inspection and restart'
printf '\nTag Files is ready at http://localhost:%s\n' "$port"
printf 'Deployment directory: %s\nLibrary data: %s/data\nTemporary workspace: %s/scratch\n' "$directory" "$directory" "$directory"
printf 'To start again, open the deployment directory and run: docker compose up -d\n'
