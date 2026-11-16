# Migrate an existing installation

Run these steps on the host of the old C#/Angular installation with SQLite and
MinIO. Docker with Compose v2 is required. Allow space for several copies of the
library plus at least 10 GB of free space.

1. Download [migrate.sh](https://github.com/MikD1/tag-files/releases/latest/download/migrate.sh)
   and [migration.env](https://github.com/MikD1/tag-files/releases/latest/download/migration.env)
   into the same directory.

2. Edit `migration.env`. Set absolute paths to the old installation and a new,
   empty destination outside it:

   ```sh
   SOURCE_DIR=/srv/tag-files-old
   TARGET_DIR=/srv/tag-files-new
   ```

3. Stop the old installation. Keep its containers: the script uses them to find
   the database and storage. **Do not run `docker compose down`.**

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

If interrupted, keep the old installation stopped and rerun `sh migrate.sh`
with the same files and paths. If a source issue blocks migration, follow
[migration recovery](operations.md#migration-recovery).

Keep the old installation and `TARGET_DIR.migration` until you decide to remove
them. To manage the new installation, run `docker compose stop` or
`docker compose up -d` inside `TARGET_DIR`.
