# Migrate an existing installation

Run these steps on the host of the old C#/Angular installation with SQLite and
MinIO. Docker with Compose v2 is required. The disk holding
`TARGET_DIR.migration` needs at least five times the old library's size (database,
MinIO data and deployment files) plus about 11 GB free.

1. Download [migrate.sh](https://github.com/MikD1/tag-files/releases/latest/download/migrate.sh)
   and [migration.env](https://github.com/MikD1/tag-files/releases/latest/download/migration.env)
   into the same directory.

2. Edit `migration.env`. It accepts only these two keys, as unquoted absolute
   paths without commas or newlines:

   ```sh
   SOURCE_DIR=/srv/tag-files-old
   TARGET_DIR=/srv/tag-files-new
   ```

   `SOURCE_DIR` is the old Compose project's working directory. `TARGET_DIR` must
   be new or empty, its parent must exist, and neither path may be inside the
   other.

3. Stop the old installation. Keep its containers: the script uses them to find
   the database and storage (exactly one legacy app container and one MinIO
   container). **Do not run `docker compose down`.**

   ```sh
   cd /srv/tag-files-old
   docker compose stop
   ```

4. Return to the directory containing the downloaded files and run:

   ```sh
   sh migrate.sh
   ```

5. Wait for the success message and open the printed application URL
   (default: `http://localhost:8080`). The script verifies the backup and migrated
   data before starting the new installation.

Keep the old installation and `TARGET_DIR.migration` until you decide to remove
them. To manage the new installation, run `docker compose stop` or
`docker compose up -d` inside `TARGET_DIR`. Its Compose project is
`tag-files-<first 12 hex digits of SHA-256 of TARGET_DIR>` unless `--project` was
given, so container names differ from a fresh installation.

`sh migrate.sh --help` lists the options: environment file, `--port`,
`--project`, `--version`, `--skip-pull`, image overrides and `--migration-image`.

## Interruption and rerun

Migration state is kept beside the target in `TARGET_DIR.migration`: `rollback/`,
a working MinIO copy (`source-minio/`), source archives (`archive/`),
`config.json`, reports and the resumable `journal/`, plus copies of source and
target credentials. Nothing there or in the original installation is removed
automatically.

After an interruption, keep the source stopped and rerun `sh migrate.sh` with the
same release, files, paths, `--port` and `--project`. Completed archives and copy
checkpoints are reused; an interrupted archive is kept and a new
`.attempt-NNN` starts. Changed source bytes, operation settings, recreated source
containers or unrelated target data cause a refusal. After a successful run, a
rerun only re-verifies and starts the target.

## Source issues

Issues in the source block migration before copying begins. They are listed in
`TARGET_DIR.migration/journal/source-issues.json`; the manifest is
`TARGET_DIR.migration/archive/run-001[.attempt-NNN]/manifest.json`. To accept a
supported loss explicitly, add an entry per issue to the `Exceptions` array in
`TARGET_DIR.migration/config.json`, using the issue's `id`, then rerun:

```json
{"issueId":"<issue id>","decision":"missing_media","reason":"<why this loss is accepted>","owner":"<person accepting it>"}
```

| Issue code | Required decision |
|---|---|
| `missing_primary` | `missing_media` |
| `dangling_collection` | `clear_reference` |
| `invalid_row`, `invalid_duration`, `unknown_thumbnail_status`, `invalid_category`, `invalid_processing`, `dangling_join`, `source_multipart` | `archive_only` |
| `unknown_schema` | always refused |

Entries with an unknown or duplicate `issueId`, or an empty `reason` or `owner`,
are rejected. Decisions are frozen when copying begins. Probe failures for
existing media are accepted only as `missing_media` on a missing primary or for
invalid rows marked `archive_only`; anything else needs investigation.

## Rollback

To return to the original installation before accepting new writes, stop the
new services and start the old Compose deployment. After new writes, preserve the
complete new metadata/storage pair first: returning to the old snapshot loses
those changes. See [backups and restore](README.md#backups-and-restore).
