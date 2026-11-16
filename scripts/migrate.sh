#!/bin/sh
set -eu

MIGRATE_VERSION='@TAG_FILES_VERSION@'
MIGRATE_REPOSITORY='@TAG_FILES_REPOSITORY@'

fail() { printf 'Tag Files migration: %s\n' "$*" >&2; exit 1; }
usage() {
    printf '%s\n' 'Usage: sh migrate.sh [options]' \
        '  --env-file PATH       Migration paths (default: migration.env next to this script)' \
        '  --version VERSION     Stable release version (embedded in release downloads)' \
        '  --app-image IMAGE     Application image override' \
        '  --storage-image IMAGE Storage image override' \
        '  --migration-image IMAGE Migration image override' \
        '  --skip-pull           Use images already loaded in Docker' \
        '  --project NAME        New Compose project (default: derived from target path)' \
        '  --port PORT           Loopback HTTP port (default: 8080)' '  --help'
}
case "$0" in */*) script_parent=${0%/*} ;; *) script_parent=. ;; esac
script_parent=$(CDPATH= cd -P "$script_parent" && pwd -P)
env_file=$script_parent/migration.env version=$MIGRATE_VERSION
app_image= storage_image= migration_image= project= port=8080 pull=yes
while [ "$#" -gt 0 ]; do
    case "$1" in
        --help|-h) usage; exit 0 ;;
        --skip-pull) pull=no; shift; continue ;;
        --env-file|--version|--app-image|--storage-image|--migration-image|--project|--port)
            [ "$#" -ge 2 ] && [ -n "$2" ] || fail "$1 requires a value" ;;
        *) fail "unknown option: $1 (see --help)" ;;
    esac
    case "$1" in
        --env-file) env_file=$2 ;; --version) version=$2 ;; --app-image) app_image=$2 ;;
        --storage-image) storage_image=$2 ;; --migration-image) migration_image=$2 ;;
        --project) project=$2 ;; --port) port=$2 ;;
    esac
    shift 2
done
case "$version" in @*) fail 'download migrate.sh from a stable release, or supply --version and all three image overrides' ;; esac
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
case "$project" in [!a-z0-9]*|*[!a-z0-9_-]*) fail 'project must use lowercase letters, digits, underscores or hyphens' ;; esac
case "$port" in ''|*[!0-9]*) fail 'port must be an integer from 1 to 65535' ;; esac
[ "$port" -ge 1 ] && [ "$port" -le 65535 ] || fail 'port must be from 1 to 65535'
if [ -z "$app_image" ] || [ -z "$storage_image" ] || [ -z "$migration_image" ]; then
    case "$MIGRATE_REPOSITORY" in @*) fail 'use a release download, or supply all three image overrides' ;; esac
    repository=$(printf '%s' "$MIGRATE_REPOSITORY" | tr '[:upper:]' '[:lower:]')
    app_image=${app_image:-ghcr.io/$repository-app:$release}
    storage_image=${storage_image:-ghcr.io/$repository-storage:$release}
    migration_image=${migration_image:-ghcr.io/$repository-migrate:$release}
fi
[ -f "$env_file" ] && [ ! -L "$env_file" ] || fail 'migration.env must be a regular file; edit SOURCE_DIR and TARGET_DIR'
SOURCE_DIR= TARGET_DIR= source_seen=no target_seen=no
# Configuration is data. Never execute it as shell code.
while IFS= read -r line || [ -n "$line" ]; do
    case "$line" in ''|'#'*) continue ;;
        SOURCE_DIR=*) [ "$source_seen" = no ] || fail 'duplicate SOURCE_DIR'; SOURCE_DIR=${line#*=}; source_seen=yes ;;
        TARGET_DIR=*) [ "$target_seen" = no ] || fail 'duplicate TARGET_DIR'; TARGET_DIR=${line#*=}; target_seen=yes ;;
        *) fail 'migration.env accepts only SOURCE_DIR and TARGET_DIR as unquoted absolute paths' ;;
    esac
done < "$env_file"
for path in "$SOURCE_DIR" "$TARGET_DIR"; do
    case "$path" in /*) ;; *) fail 'SOURCE_DIR and TARGET_DIR must be absolute paths' ;; esac
    case "$path" in /|*','*|*'
'*) fail 'directories cannot be / or contain commas or newlines' ;; esac
done
[ -d "$SOURCE_DIR" ] && [ ! -L "$SOURCE_DIR" ] || fail 'SOURCE_DIR must be an existing real deployment directory'
source=$(CDPATH= cd -P "$SOURCE_DIR" && pwd -P) || fail 'cannot resolve SOURCE_DIR'
[ ! -L "$TARGET_DIR" ] || fail 'TARGET_DIR must not be a symlink'
target_leaf=${TARGET_DIR##*/} target_parent=${TARGET_DIR%/*}
[ -n "$target_leaf" ] && [ "$target_leaf" != . ] && [ "$target_leaf" != .. ] || fail 'TARGET_DIR must name a new deployment directory'
[ -n "$target_parent" ] || target_parent=/
[ -d "$target_parent" ] || fail 'the parent of TARGET_DIR must already exist'
target_parent=$(CDPATH= cd -P "$target_parent" && pwd -P) || fail 'cannot resolve target parent'
case "$target_parent" in /) target=/$target_leaf ;; *) target=$target_parent/$target_leaf ;; esac
case "$target/" in "$source/"*) fail 'target must be outside the source deployment' ;; esac
case "$source/" in "$target/"*) fail 'source must be outside the target deployment' ;; esac
work=$target.migration
[ ! -L "$work" ] || fail 'migration state must not be a symlink'
if [ -e "$target" ]; then
    [ -d "$target" ] || fail 'TARGET_DIR must be a new directory'
    if [ ! -f "$work/state.json" ]; then
        for entry in "$target"/* "$target"/.[!.]* "$target"/..?*; do
            if [ -e "$entry" ] || [ -L "$entry" ]; then fail 'TARGET_DIR contains unrelated data; choose a new empty target'; fi
        done
    fi
fi
if [ -d "$work" ] && [ ! -f "$work/state.json" ]; then
    if [ -f "$work/bootstrap.env" ] && [ ! -L "$work/bootstrap.env" ]; then
        bootstrap_source= bootstrap_target= bootstrap_format=
        while IFS= read -r line || [ -n "$line" ]; do
            case "$line" in
                TAG_FILES_MIGRATION_BOOTSTRAP=1) bootstrap_format=1 ;;
                SOURCE_DIR=*) bootstrap_source=${line#*=} ;;
                TARGET_DIR=*) bootstrap_target=${line#*=} ;;
                *) fail 'unrecognized migration bootstrap receipt' ;;
            esac
        done < "$work/bootstrap.env"
        [ "$bootstrap_format" = 1 ] && [ "$bootstrap_source" = "$source" ] && [ "$bootstrap_target" = "$target" ] || fail 'migration bootstrap paths changed'
    else
        for entry in "$work"/* "$work"/.[!.]* "$work"/..?*; do
            if [ -e "$entry" ] || [ -L "$entry" ]; then fail 'existing migration directory has no recognized operation state'; fi
        done
    fi
fi
command -v docker >/dev/null 2>&1 || fail 'install Docker with Compose v2 before migration'
docker compose version >/dev/null 2>&1 || fail 'Docker Compose v2 is required'
unset COMPOSE_FILE COMPOSE_PROJECT_NAME COMPOSE_PROFILES COMPOSE_ENV_FILES COMPOSE_PATH_SEPARATOR
unset TAG_FILES_APP_IMAGE TAG_FILES_STORAGE_IMAGE TAG_FILES_PORT TAG_FILES_MIN_FREE_BYTES TAG_FILES_UID TAG_FILES_GID
platform() {
    case "$1" in 'linux amd64'|'linux x86_64') printf '%s' 'linux amd64' ;;
        'linux arm64'|'linux aarch64') printf '%s' 'linux arm64' ;;
        *) fail "unsupported native Docker platform: $1" ;;
    esac
}
native=$(platform "$(docker info --format '{{.OSType}} {{.Architecture}}')") || exit 1
if [ "$pull" = yes ]; then
    docker pull "$app_image" || fail 'application image pull failed'
    docker pull "$storage_image" || fail 'storage image pull failed'
    docker pull "$migration_image" || fail 'migration image pull failed'
fi
image() {
    id=$(docker image inspect --format '{{.Id}}' "$1") || fail "cannot inspect image: $1"
    case "$id" in sha256:*) ;; *) fail 'an exact Linux image ID is required' ;; esac
    hash=${id#sha256:}
    case "$hash" in ''|*[!0-9a-f]*) fail 'invalid exact image ID' ;; esac
    [ "${#hash}" -eq 64 ] || fail 'invalid exact image ID'
    candidate=$(platform "$(docker image inspect --format '{{.Os}} {{.Architecture}}' "$id")") || exit 1
    [ "$candidate" = "$native" ] || fail 'image does not match native Docker platform'
    printf '%s' "$id"
}
app_id=$(image "$app_image") || exit 1
storage_id=$(image "$storage_image") || exit 1
migration_id=$(image "$migration_image") || exit 1
umask 077
mkdir -p "$work" || fail 'cannot create private migration state'
chmod 700 "$work" || fail 'cannot make migration state private'
if [ ! -e "$work/bootstrap.env" ]; then
    (set -C; printf 'TAG_FILES_MIGRATION_BOOTSTRAP=1\nSOURCE_DIR=%s\nTARGET_DIR=%s\n' "$source" "$target" > "$work/bootstrap.env") || fail 'cannot claim migration bootstrap'
fi
lock=$work/host.lock
if ! mkdir "$lock" 2>/dev/null; then
    [ -f "$lock/pid" ] && [ ! -L "$lock/pid" ] || fail 'another migration is acquiring this operation lock'
    IFS= read -r previous_pid < "$lock/pid" || fail 'invalid operation lock; inspect retained state'
    case "$previous_pid" in ''|*[!0-9]*) fail 'invalid operation lock; inspect retained state' ;; esac
    if kill -0 "$previous_pid" 2>/dev/null; then fail 'another migration process owns this target'; fi
    rm "$lock/pid" && rmdir "$lock" && mkdir "$lock" || fail 'cannot recover stale migration lock'
fi
printf '%s\n' "$$" > "$lock/pid"
OPERATION_ID=
storage_started=no
compose() { docker compose --project-directory "$target" --env-file "$target/.env" -f "$target/compose.yml" -p "$project" "$@"; }
stop_owned() {
    [ -n "$OPERATION_ID" ] || return 0
    own=$(docker ps -aq --filter "name=^/$1$" --filter "label=tag-files.migration.operation=$OPERATION_ID") || return 1
    if [ -n "$own" ]; then docker stop --time 30 "$own" >/dev/null; fi
}
cleanup() {
    status=$?
    trap - 0 HUP INT TERM
    stop_owned "$project-migrate-run" 2>/dev/null || :
    stop_owned "$project-source-minio" 2>/dev/null || :
    if [ "$storage_started" = yes ]; then compose stop -t 120 storage >/dev/null 2>&1 || :; fi
    rm "$lock/pid" 2>/dev/null || :
    rmdir "$lock" 2>/dev/null || :
    if [ "$status" -ne 0 ]; then printf 'Migration state and rollback material retained at: %s\nMigration did not start the target application.\n' "$work" >&2; fi
    exit "$status"
}
trap cleanup 0
trap 'exit 130' INT
trap 'exit 143' HUP TERM
inspect_all() {
    ids=$(docker ps -aq) || fail 'cannot inspect source containers'
    if [ -n "$ids" ]; then
        # Docker emits container IDs; these words contain no shell syntax.
        docker inspect $ids > "$work/docker-inspect.json" || fail 'container inspection failed'
    else printf '[]\n' > "$work/docker-inspect.json"; fi
    helper mount-sources --inspect /work/docker-inspect.json --source-directory "$source" --target-directory "$target" || fail 'cannot plan daemon mount inspection'
    set -- --rm --network none --read-only --user 65534:65534 --cap-drop ALL --security-opt no-new-privileges \
        --entrypoint tag-files-migration-host \
        --mount "type=bind,src=$work/inspection-plan.json,dst=/plan.json,readonly" \
        --mount "type=bind,src=$target_parent,dst=/inspection/target-parent,readonly"
    inspection_kind= inspection_source= inspection_expected=kind
    while IFS= read -r inspection_line || [ -n "$inspection_line" ]; do
        case "$inspection_expected:$inspection_line" in
            kind:MOUNT_KIND=bind|kind:MOUNT_KIND=volume) inspection_kind=${inspection_line#*=}; inspection_expected=source ;;
            source:MOUNT_SOURCE=*) inspection_source=${inspection_line#*=}; inspection_expected=index ;;
            index:MOUNT_INDEX=*)
                inspection_index=${inspection_line#*=}
                case "$inspection_index" in ''|*[!0-9]*) fail 'invalid native inspection index' ;; esac
                set -- "$@" --mount "type=$inspection_kind,src=$inspection_source,dst=/inspection/mount-$inspection_index,readonly"
                inspection_expected=kind ;;
            *) fail 'invalid native inspection protocol' ;;
        esac
    done < "$work/inspection-mounts.env"
    [ "$inspection_expected" = kind ] || fail 'incomplete native inspection protocol'
    # The unprivileged helper only reads mountinfo, never the candidate paths.
    docker run "$@" "$migration_id" canonical-mounts --plan /plan.json > "$work/canonical-mounts.json" \
        || fail 'cannot establish physical native mount identities'
}
helper() {
    docker run --rm --network none --user 0:0 --entrypoint tag-files-migration-host \
        --mount "type=bind,src=$work,dst=/work" \
        --mount "type=bind,src=$source,dst=/deployment,readonly" \
        "$migration_id" "$@" --state /work
}
inspect_all
helper discover --source-directory "$source" --target-directory "$target" \
    --version "$version" --app-image "$app_id" --storage-image "$storage_id" \
    --migration-image "$migration_id" --project "$project" --port "$port" || fail 'source discovery failed'
DB_MOUNT_KIND= DB_MOUNT_SOURCE= STORAGE_MOUNT_KIND= STORAGE_MOUNT_SOURCE= SOURCE_DB_RELATIVE=
SOURCE_STORAGE_IMAGE= SOURCE_APP_ID= SOURCE_STORAGE_ID= OPERATION_ID= protocol_keys=
while IFS= read -r line || [ -n "$line" ]; do
    key=${line%%=*} value=${line#*=}
    [ "$key" != "$line" ] || fail 'invalid migration helper protocol'
    case " $protocol_keys " in *" $key "*) fail 'duplicate migration helper protocol key' ;; esac
    protocol_keys="$protocol_keys $key"
    case "$key" in
        DB_MOUNT_KIND) DB_MOUNT_KIND=$value ;; DB_MOUNT_SOURCE) DB_MOUNT_SOURCE=$value ;;
        STORAGE_MOUNT_KIND) STORAGE_MOUNT_KIND=$value ;; STORAGE_MOUNT_SOURCE) STORAGE_MOUNT_SOURCE=$value ;;
        SOURCE_DB_RELATIVE) SOURCE_DB_RELATIVE=$value ;; SOURCE_STORAGE_IMAGE) SOURCE_STORAGE_IMAGE=$value ;;
        SOURCE_APP_ID) SOURCE_APP_ID=$value ;; SOURCE_STORAGE_ID) SOURCE_STORAGE_ID=$value ;;
        PROJECT) project=$value ;; OPERATION_ID) OPERATION_ID=$value ;;
        *) fail 'unknown migration helper protocol key' ;;
    esac
done < "$work/host.env"
case "$DB_MOUNT_KIND:$STORAGE_MOUNT_KIND" in bind:bind|bind:volume|volume:bind|volume:volume) ;; *) fail 'invalid source native mount kinds' ;; esac
[ -n "$DB_MOUNT_SOURCE" ] && [ -n "$STORAGE_MOUNT_SOURCE" ] && [ -n "$project" ] && [ -n "$OPERATION_ID" ] && [ -n "$SOURCE_DB_RELATIVE" ] && [ -n "$SOURCE_APP_ID" ] && [ -n "$SOURCE_STORAGE_ID" ] || fail 'incomplete migration helper protocol'
case "$OPERATION_ID" in *[!0-9a-f]*) fail 'invalid operation ID' ;; esac
[ "${#OPERATION_ID}" -eq 32 ] || fail 'invalid operation ID'
source_storage_id=$(image "$SOURCE_STORAGE_IMAGE") || exit 1
[ "$source_storage_id" = "$SOURCE_STORAGE_IMAGE" ] || fail 'installed source image changed'
for kind_source in "$DB_MOUNT_KIND:$DB_MOUNT_SOURCE" "$STORAGE_MOUNT_KIND:$STORAGE_MOUNT_SOURCE"; do
    case "$kind_source" in volume:*)
        volume=${kind_source#*:}
        profile=$(docker volume inspect --format '{{.Driver}} {{json .Options}}' "$volume") || fail 'cannot inspect source volume'
        case "$profile" in 'local null'|'local {}') ;; *) fail 'source named volumes must use the plain local driver' ;; esac ;;
    esac
done
native_helper() {
    docker run --rm --network none --user 0:0 --entrypoint tag-files-migration-host \
        --mount "type=bind,src=$work,dst=/work" \
        --mount "type=bind,src=$source,dst=/deployment,readonly" \
        --mount "type=$DB_MOUNT_KIND,src=$DB_MOUNT_SOURCE,dst=/source,readonly" \
        --mount "type=$STORAGE_MOUNT_KIND,src=$STORAGE_MOUNT_SOURCE,dst=/source-storage,readonly" \
        "$migration_id" "$@" --state /work
}
target_helper() {
    docker run --rm --network none --user 0:0 --entrypoint tag-files-migration-host \
        --mount "type=bind,src=$work,dst=/work" --mount "type=bind,src=$target,dst=/install" \
        "$migration_id" "$@" --state /work --target /install
}
ready_message() {
    printf '\nTag Files is ready at http://localhost:%s\n' "$port"
    printf 'Deployment directory: %s\nLibrary data: %s/data\nMigration state, rollback backup, archives and reports: %s\n' "$target" "$target" "$work"
    printf 'Keep the stopped source and retained rollback material until you explicitly remove them.\n'
    printf 'To start again, open the deployment directory and run: docker compose up -d\n'
}
if [ -f "$work/verified.json" ]; then
    target_helper completed || fail 'completed migration receipt or target configuration changed'
    compose up -d --pull never --no-recreate --wait --wait-timeout 180 || fail 'verified deployment startup failed'
    ready_message
    exit 0
fi
# Stop only containers bearing this operation's label. An unrelated name collision
# is preserved and Docker refuses to replace it.
for temporary in "$project-migrate-run" "$project-source-minio"; do
    all=$(docker ps -aq --filter "name=^/$temporary$") || fail 'cannot inspect temporary container names'
    own=$(docker ps -aq --filter "name=^/$temporary$" --filter "label=tag-files.migration.operation=$OPERATION_ID") || fail 'cannot inspect temporary services'
    [ "$all" = "$own" ] || fail 'an unrelated container uses a migration service name; choose another project'
    if [ -n "$own" ]; then docker stop --time 30 "$own" >/dev/null || fail 'cannot stop retained temporary service'; fi
done
native_helper prepare || fail 'verified cold rollback preparation failed'
inspect_all
native_helper verify-source || fail 'source is no longer frozen'
if [ ! -f "$target/installation.json" ]; then
    occupied=$(docker ps -aq --filter "label=com.docker.compose.project=$project") || fail 'cannot inspect target project'
    volumes=$(docker volume ls -q --filter "label=com.docker.compose.project=$project") || fail 'cannot inspect target project volumes'
    [ -z "$occupied" ] && [ -z "$volumes" ] || fail 'target Compose project already owns resources; choose a new project'
    helper setup-stage || fail 'cannot prepare a new target initialization attempt'
    IFS= read -r setup_line < "$work/setup-stage.env" || fail 'missing setup stage receipt'
    case "$setup_line" in SETUP_STAGE=setup-attempt-*) setup_stage=${setup_line#*=} ;; *) fail 'invalid setup stage receipt' ;; esac
    stage_suffix=${setup_stage#setup-attempt-}
    case "$stage_suffix" in ''|*[!0-9a-f]*) fail 'invalid setup stage name' ;; esac
    [ "${#stage_suffix}" -eq 32 ] || fail 'invalid setup stage name'
    docker run --rm --network none --user 0:0 --entrypoint storage-run \
        --mount "type=bind,src=$work/$setup_stage,dst=/install" "$storage_id" setup \
        --directory /install --version "$version" --app-image "$app_id" \
        --storage-image "$storage_id" --project "$project" --port "$port" || fail 'target initialization attempt retained; rerun to use a fresh attempt'
    docker run --rm --network none --user 0:0 --entrypoint tag-files-migration-host \
        --mount "type=bind,src=$work,dst=/work" --mount "type=bind,src=$target_parent,dst=/parent" \
        "$migration_id" publish-target --state /work --parent /parent --stage "$setup_stage" \
        || fail 'target publication refused; initialized attempt is retained'

fi
target_helper configure-target || fail 'target deployment configuration changed'
inspect_all
native_helper verify-source || fail 'source or stopped target changed before storage startup'
storage_started=yes
compose up -d --pull never --wait --wait-timeout 180 storage || fail 'target storage startup failed'
# Only the disposable serving clone is writable. Originals and rollback stay cold.
docker run --detach --rm --name "$project-source-minio" \
    --label "tag-files.migration.operation=$OPERATION_ID" \
    --network "${project}_library" --network-alias frozen-minio \
    --read-only --user 0:0 --tmpfs /tmp:size=64m,mode=1777 \
    --tmpfs /root/.minio:size=16m --tmpfs /.minio:size=16m \
    --env-file "$work/source-minio.env" \
    --mount "type=bind,src=$work/source-minio,dst=/data" \
    --entrypoint minio "$source_storage_id" server --address :9000 --console-address :9001 /data \
    >/dev/null || fail 'frozen source MinIO startup failed'
docker run --rm --network "${project}_library" --user 0:0 --entrypoint tag-files-migration-host \
    --mount "type=bind,src=$work,dst=/work" "$migration_id" wait-source --state /work \
    || fail 'temporary source MinIO readiness check failed'
inspect_all
native_helper verify-source || fail 'source changed before copying'
docker run --rm --name "$project-migrate-run" --sig-proxy=true \
    --label "tag-files.migration.operation=$OPERATION_ID" \
    --network "${project}_library" --user 0:0 \
    --mount "type=$DB_MOUNT_KIND,src=$DB_MOUNT_SOURCE,dst=/source,readonly" \
    --mount "type=bind,src=$work,dst=/work" \
    --mount "type=bind,src=$target/data/metadata,dst=/target" \
    --mount "type=bind,src=$work/source-secrets,dst=/source-secrets,readonly" \
    --mount "type=bind,src=$work/target-secrets,dst=/target-secrets,readonly" \
    "$migration_id" auto -config /work/config.json || fail 'migration stopped; inspect retained reports, then rerun the same command to continue'
inspect_all
native_helper verify-source || fail 'source changed during migration'
stop_owned "$project-source-minio" || fail 'cannot stop temporary source MinIO'
compose stop -t 120 storage || fail 'cannot stop storage before final ownership checks'
storage_started=no
inspect_all
native_helper verify-source || fail 'source or stopped target changed before finalization'
target_helper finalize || fail 'complete verification and final ownership checks failed'
compose up -d --pull never --wait --wait-timeout 180 storage || fail 'verified storage startup failed'
compose up -d --pull never --wait --wait-timeout 180 app || fail 'verified application startup failed; retry the same command'
ready_message
