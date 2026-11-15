# Owned media scratch

Each attempt uses `<job UUID>/<lease generation>-<attempt UUID>/` with separate
source/output files. A stale writer cannot open or truncate a newer attempt's
path. The manager holds an OS lock and refuses release while attempts are live.
Close removes only its own attempt after producers/readers have returned.
The ingestion pipeline obtains `Attempt.Context` before I/O and propagates it
through every media command. Commands inherit both the root lock and an
attempt lock. Retirement serializes with process Start; Close refuses to remove
files while a producer retains its inherited attempt lock, including when a
caller has returned too early. Existing contexts cannot start new commands after
retirement. Neither lock is released with an explicit flock unlock.

Before I/O, reserve the expected source bytes plus the processor's complete
configured output cap. Admission counts retained files, live written bytes and
unwritten reservations against the configured quota and Linux statfs available
bytes/inodes, including the configured physical free-space reserve. Neither
quiet files nor lease age authorize removal. Symlinks/nonregular entries are
refused and job IDs never become arbitrary paths.

Ingestion and poster pipelines use `Attempt.Run` to own the inherited context,
join the periodic monitor and close only after the callback returns. They check
capacity after download/conversion and once per second during work. Exhaustion
cancels and joins the source read/media execution
before scratch removal; the durable S3 source remains authoritative. The typed
insufficient-storage error is available for runtime HTTP507 mapping, which is
not wired yet. This module measures scratch only; it does not infer physical
storage-volume capacity through S3 or replace metadata/storage preflight.

After a crash, Linux kills direct media processes with the application. A
surviving descendant retains the inherited root lock and refuses a new owner.
Only after all such descriptors close can a new manager call `Reclaim` before
admission. It preflights the whole bounded tree and every attempt lock before
removing attempts with the v1 guard marker and exact UUID/generation layout.
Unknown entries, symlinks, future/unmarked caches and legacy caches do not gain
cleanup permission. Unproven ordinary caches remain retained and budgeted.

This protocol assumes the pinned media tools preserve inherited descriptors;
actual FFmpeg and adversarial descendant tests exercise this boundary. Arbitrary
commands that close their inherited guards are outside this profile. App startup
must still wire reclaim before worker/HTTP admission; full disk admission, process
RSS and Mac capacity remain open.
