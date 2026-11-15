"""Versioned host installation/update. No library or native volume is opened here."""
import argparse
from contextlib import contextmanager
import fcntl
import datetime
import hashlib
import json
import os
from pathlib import Path
import re
import secrets
import shutil
import stat
import subprocess
import tempfile
import uuid

REPO = Path(__file__).resolve().parent.parent


def docker(*args):
    env = {k: v for k, v in os.environ.items() if not k.startswith("TAG_FILES_") and not k.startswith("COMPOSE_")}
    r = subprocess.run(["docker", *args], env=env, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
    if r.returncode:
        raise ValueError("docker " + args[0] + " failed: " + r.stderr.strip())
    return r.stdout.strip()


def sync_dir(root):
    fd = os.open(root, os.O_RDONLY)
    try: os.fsync(fd)
    finally: os.close(fd)


def atomic_write(path, data, mode=0o600):
    fd, name = tempfile.mkstemp(prefix=".install-tmp-", dir=path.parent)
    try:
        with os.fdopen(fd, "wb") as f:
            os.fchmod(f.fileno(), mode)
            f.write(data)
            f.flush()
            os.fsync(f.fileno())
        os.replace(name, path)
        sync_dir(path.parent)
    finally:
        if os.path.exists(name): os.unlink(name)


def save_json(path, obj):
    atomic_write(path, (json.dumps(obj, indent=2) + "\n").encode())


def read_regular(path):
    fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK)
    with os.fdopen(fd, "rb") as f:
        info = os.fstat(f.fileno())
        if not stat.S_ISREG(info.st_mode) or info.st_size > 4 * 1024 * 1024:
            raise ValueError("expected bounded regular deployment file")
        return f.read()


def version(value):
    m = re.fullmatch(r"v?(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)", value)
    if not m: raise ValueError("an explicit stable major.minor.patch version is required")
    return tuple(map(int, m.groups()))


def image(ref, no_pull):
    if not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9._:/@-]*", ref):
        raise ValueError("invalid image reference")
    if not no_pull:
        if not (re.search(r":v?\d+\.\d+\.\d+$", ref) or re.search(r"@sha256:[0-9a-f]{64}$", ref)):
            raise ValueError("remote images must use a stable version tag or immutable digest")
        docker("pull", ref)
    row = json.loads(docker("image", "inspect", ref))[0]
    if row.get("Os") != "linux" or not re.fullmatch(r"sha256:[0-9a-f]{64}", row.get("Id", "")):
        raise ValueError("a local exact Linux image is required")
    return {"requested": ref, **{k: row.get(k) for k in ("Id", "RepoDigests", "Architecture", "Os")}}


def native_images(images):
    # Query the daemon, including a remote daemon or Docker Desktop VM, rather
    # than the host Python process. Preloaded foreign images may be inspectable
    # while binfmt/QEMU is absent; never stop a working pair to discover that.
    host = json.loads(docker("info", "--format", "{{json .}}"))
    aliases = {"aarch64": "arm64", "arm64": "arm64", "x86_64": "amd64", "amd64": "amd64"}
    arch = aliases.get(host.get("Architecture"))
    if host.get("OSType") != "linux" or arch is None:
        raise ValueError("cannot establish a supported native Docker platform")
    for role, candidate in images.items():
        if candidate["Os"] != "linux" or candidate["Architecture"] != arch:
            raise ValueError(role + " image does not match native Docker platform linux/" + arch)


def roots(directory):
    candidate = Path(directory).absolute()
    if candidate.is_symlink(): raise ValueError("deployment root must not be a symlink")
    root = candidate.resolve()
    if root == REPO or REPO in root.parents:
        raise ValueError("use an external deployment directory outside the source checkout")
    docker_root = Path(docker("info", "--format", "{{.DockerRootDir}}")).resolve()
    if root == docker_root or docker_root in root.parents or root in docker_root.parents:
        raise ValueError("deployment must be outside Docker's native data root")
    return root



@contextmanager
def deployment_lock(root, create_parent=False):
    """One host owner across validation, install publication and update writes.

    Keep this sibling inode: replacing/unlinking a locked file would let another
    process lock a different inode while the first operation is still running.
    Kernel ownership disappears on close/process death; durable operation state
    independently prevents a crashed update from being silently retried.
    """
    if create_parent:
        root.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
    parent_fd = os.open(root.parent, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW | os.O_CLOEXEC)
    lock_fd = None
    try:
        name = "." + root.name + ".install-update.lock"
        lock_fd = os.open(name, os.O_CREAT | os.O_RDWR | os.O_NOFOLLOW | os.O_NONBLOCK | os.O_CLOEXEC,
                          0o600, dir_fd=parent_fd)
        info = os.fstat(lock_fd)
        if (not stat.S_ISREG(info.st_mode) or info.st_nlink != 1 or info.st_uid != os.geteuid()
                or info.st_mode & 0o077 or info.st_size != 0):
            raise ValueError("deployment lock must be an empty private regular file owned by this host user")
        try:
            fcntl.flock(lock_fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError as error:
            raise ValueError("another install/update owns this deployment; retry after it finishes") from error
        current = os.stat(name, dir_fd=parent_fd, follow_symlinks=False)
        named_parent = os.stat(root.parent, follow_symlinks=False)
        if (not os.path.samestat(os.fstat(parent_fd), named_parent)
                or not os.path.samestat(info, current) or root.is_symlink()):
            raise ValueError("deployment or lock path changed while acquiring ownership")
        yield
    finally:
        if lock_fd is not None:
            os.close(lock_fd)
        os.close(parent_fd)


def compose(root, *args, env_file=None, compose_file=None):
    return docker("compose", "--project-directory", str(root), "--env-file", str(env_file or root / ".env"),
                  "-f", str(compose_file or root / "compose.yml"), "-p", load_env(env_file or root / ".env")["COMPOSE_PROJECT_NAME"], *args)


def env_bytes(project, port, images):
    return (f"COMPOSE_PROJECT_NAME={project}\nTAG_FILES_APP_IMAGE={images['app']['Id']}\n"
            f"TAG_FILES_STORAGE_IMAGE={images['storage']['Id']}\nTAG_FILES_PORT={port}\n"
            "TAG_FILES_MIN_FREE_BYTES=1073741824\n").encode()


def load_env(path):
    out = {}
    for line in read_regular(path).decode().splitlines():
        key, sep, value = line.partition("=")
        if not sep or key in out: raise ValueError("invalid deployment env")
        out[key] = value
    if set(out) != {"COMPOSE_PROJECT_NAME", "TAG_FILES_APP_IMAGE", "TAG_FILES_STORAGE_IMAGE", "TAG_FILES_PORT", "TAG_FILES_MIN_FREE_BYTES"}:
        raise ValueError("unknown or missing deployment env keys")
    return out


def hashes(root):
    return {name: hashlib.sha256(read_regular(root / name)).hexdigest() for name in (".env", "compose.yml")}


def unused_project(project):
    if docker("ps", "-aq", "--filter", "label=com.docker.compose.project=" + project):
        raise ValueError("project already has containers; install requires a new empty deployment")
    volumes = set(docker("volume", "ls", "-q").splitlines())
    if volumes.intersection({project + "_app_data", project + "_storage_data", project + "_scratch"}):
        raise ValueError("project already has named data volumes; do not use install for existing libraries")


def start_services(root):
    compose(root, "up", "-d", "--pull", "never", "--wait", "--wait-timeout", "150", "storage")
    compose(root, "exec", "-T", "storage", "storage-run", "check")
    compose(root, "up", "-d", "--pull", "never", "--wait", "--wait-timeout", "150", "app")


def stop_services(root):
    compose(root, "stop", "-t", "120", "app")
    compose(root, "stop", "-t", "120", "storage")


def install(args):
    root = roots(args.directory)
    with deployment_lock(root, create_parent=True):
        return _install_locked(args, root)


def _install_locked(args, root):
    version(args.version)
    if not re.fullmatch(r"[a-z0-9][a-z0-9_-]*", args.project) or not 1 <= args.port <= 65535:
        raise ValueError("invalid project name or loopback port")
    if root.exists() and (not root.is_dir() or list(root.iterdir())):
        raise ValueError("install requires a new empty directory")
    unused_project(args.project)
    images = {"app": image(args.app_image, args.no_pull), "storage": image(args.storage_image, args.no_pull)}
    native_images(images)
    root.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
    stage = Path(tempfile.mkdtemp(prefix="." + root.name + ".install-", dir=root.parent))
    try:
        stage.chmod(0o700)
        (stage / "secrets").mkdir(mode=0o700)
        atomic_write(stage / "secrets/s3_access_key", (secrets.token_hex(16) + "\n").encode(), 0o444)
        atomic_write(stage / "secrets/s3_secret_key", (secrets.token_hex(32) + "\n").encode(), 0o444)
        atomic_write(stage / "compose.yml", (REPO / "compose.yml").read_bytes())
        atomic_write(stage / ".env", env_bytes(args.project, args.port, images))
        record = {"format": 1, "version": args.version, "project": args.project, "port": args.port,
                  "created": datetime.datetime.now(datetime.timezone.utc).isoformat(), "images": images,
                  "files": hashes(stage), "state": "prepared"}
        save_json(stage / "installation.json", record)
        compose(stage, "config", "-q")
        unused_project(args.project)
        if root.exists(): root.rmdir()  # Empty checked above; no recursive deletion.
        os.rename(stage, root)
        sync_dir(root.parent)
    except Exception:
        # Preserve an incomplete stage for diagnosis; it contains no old data.
        raise
    if args.start:
        try:
            record["state"] = "starting"
            save_json(root / "installation.json", record)
            unused_project(args.project)
            start_services(root)
            record["state"] = "running"
            save_json(root / "installation.json", record)
        except Exception as error:
            record["state"] = "failed"
            save_json(root / "installation.json", record)
            save_json(root / "install-failure.json", {"state": "failed", "error": str(error)})
            raise
    return {"directory": str(root), "version": args.version, "state": record["state"], "project": args.project,
            "origin": "http://127.0.0.1:" + str(args.port), "images": images}


def cold_backup(root, args, old):
    app = compose(root, "ps", "-aq", "app")
    storage = compose(root, "ps", "-aq", "storage")
    if not app or not storage or "\n" in app + storage:
        raise ValueError("update requires one existing stopped app and storage container")
    rows = json.loads(docker("inspect", app, storage))
    volumes = {}
    for role, container in (("metadata", app), ("storage", storage)):
        row = next((r for r in rows if r.get("Id") == container), None)
        mounts = [] if row is None else [m for m in row.get("Mounts", []) if m.get("Destination") == "/data" and m.get("Type") == "volume"]
        if len(mounts) != 1: raise ValueError("cannot establish native volume pair")
        volumes[role] = mounts[0]["Name"]
    generation = args.backup_generation or datetime.datetime.now(datetime.timezone.utc).strftime("%Y%m%dT%H%M%S") + "-before-" + args.version
    cmd = [str(REPO / "tools/backup/cold-backup"), "backup", "--image", args.backup_tool_image,
           "--app-container", app, "--storage-container", storage, "--metadata-volume", volumes["metadata"],
           "--storage-volume", volumes["storage"], "--deployment", str(root), "--destination", args.backup_destination,
           "--generation", generation]
    result = subprocess.run(cmd, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
    if result.returncode: raise ValueError("cold backup failed; services remain stopped: " + result.stderr.strip())
    return json.loads(result.stdout)


def update(args):
    root = roots(args.directory)
    with deployment_lock(root):
        return _update_locked(args, root)


def _update_locked(args, root):
    target_version = version(args.version)
    old = json.loads(read_regular(root / "installation.json"))
    if old.get("format") != 1 or old.get("state") not in ("prepared", "running"):
        raise ValueError("unsupported or failed installation; inspect evidence before retry")
    if target_version <= version(old["version"]):
        raise ValueError("update requires a strictly newer stable version; schema rollback is never automatic")
    if hashes(root) != old.get("files"):
        raise ValueError("deployment configuration changed; review it before updating")
    if (root / "updates").exists():
        for p in (root / "updates").glob("*/operation.json"):
            if json.loads(read_regular(p)).get("state") != "complete":
                raise ValueError("an incomplete earlier update requires diagnosis before another update")
    # Pull/inspect every new immutable image and the external tool before stopping
    # current services. A network/tag failure leaves current services untouched.
    images = {"app": image(args.app_image, args.no_pull), "storage": image(args.storage_image, args.no_pull)}
    backup_image = image(args.backup_tool_image, True)
    native_images({**images, "backup tool": backup_image})
    env = load_env(root / ".env")
    op = root / "updates" / (datetime.datetime.now(datetime.timezone.utc).strftime("%Y%m%dT%H%M%S") + "-" + str(uuid.uuid4()))
    (root / "updates").mkdir(mode=0o700, exist_ok=True)
    op.mkdir(mode=0o700)
    (op / "before").mkdir(mode=0o700)
    for name in (".env", "compose.yml", "installation.json"):
        atomic_write(op / "before" / name, read_regular(root / name))
    next_env = op / "next.env"
    atomic_write(next_env, env_bytes(old["project"], int(env["TAG_FILES_PORT"]), images))
    next_compose = op / "next-compose.yml"
    atomic_write(next_compose, (REPO / "compose.yml").read_bytes())
    record = {"state": "prepared", "phase": "images-resolved", "fromVersion": old["version"], "toVersion": args.version,
              "images": images, "created": datetime.datetime.now(datetime.timezone.utc).isoformat()}
    save_json(op / "operation.json", record)
    config_changed = False
    try:
        compose(root, "config", "-q", env_file=next_env, compose_file=next_compose)
        record["phase"] = "stopping-services"
        save_json(op / "operation.json", record)
        stop_services(root)
        record["phase"] = "cold-backup"
        save_json(op / "operation.json", record)
        record["backup"] = cold_backup(root, args, old)
        save_json(op / "operation.json", record)
        atomic_write(root / ".env", read_regular(next_env))
        config_changed = True
        atomic_write(root / "compose.yml", read_regular(next_compose))
        record["phase"] = "starting-new-images"
        save_json(op / "operation.json", record)
        start_services(root)
        current = {**old, "version": args.version, "images": images, "state": "running", "files": hashes(root),
                   "updated": datetime.datetime.now(datetime.timezone.utc).isoformat()}
        save_json(root / "installation.json", current)
        record["phase"] = "complete"
        record["state"] = "complete"
        save_json(op / "operation.json", record)
    except Exception as error:
        record["state"] = "failed"
        record["error"] = str(error)
        if config_changed:
            try: stop_services(root)
            except Exception as stop_error: record["stopError"] = str(stop_error)
        save_json(op / "operation.json", record)
        # An app may already have migrated or written. Never restart old images
        # or rewrite .env as an implicit schema downgrade after a failed startup.
        raise
    return {"directory": str(root), "version": args.version, "state": "running", "receipt": str(op / "operation.json"),
            "backup": record["backup"], "images": images}


def main(command):
    p = argparse.ArgumentParser(description="External versioned Tag Files " + command)
    p.add_argument("--directory", required=True, help="external deployment directory")
    p.add_argument("--version", required=True, help="stable major.minor.patch version")
    p.add_argument("--app-image", required=True)
    p.add_argument("--storage-image", required=True)
    p.add_argument("--no-pull", action="store_true", help="use preloaded local images and pin their exact IDs")
    if command == "install":
        p.add_argument("--project", default="tag-files")
        p.add_argument("--port", type=int, default=4200)
        p.add_argument("--start", action="store_true", help="start the new empty installation after preparation")
    else:
        p.add_argument("--backup-destination", required=True, help="external cold archive destination")
        p.add_argument("--backup-tool-image", required=True, help="preloaded separate cold-backup tool image")
        p.add_argument("--backup-generation")
    args = p.parse_args()
    try:
        result = install(args) if command == "install" else update(args)
        print(json.dumps(result))
        return 0
    except (ValueError, OSError, json.JSONDecodeError) as error:
        print(command + ": " + str(error), file=__import__("sys").stderr)
        return 1
