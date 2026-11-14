# Owned media scratch

Each attempt uses `<job UUID>/<lease generation>-<attempt UUID>/` with separate
source/output files. A stale writer cannot open or truncate a newer attempt's
path. The manager holds an OS lock and refuses release while attempts are live.
Close removes only its own attempt after producers/readers have returned.

Before I/O, reserve the expected source bytes plus the processor's complete
configured output cap. Admission counts retained files, live written bytes and
unwritten reservations against the configured quota and Linux statfs available
bytes/inodes, including the configured physical free-space reserve. Neither
quiet files nor lease age authorize removal. Symlinks/nonregular entries are
refused and job IDs never become arbitrary paths.

The ingestion pipeline checks capacity after download/conversion and once per
second during work. Exhaustion cancels and joins the source read/media execution
before scratch removal; the durable S3 source remains authoritative. The typed
insufficient-storage error is available for runtime HTTP507 mapping, which is
not wired yet. This module measures scratch only; it does not infer physical
storage-volume capacity through S3 or replace metadata/storage preflight.

After a crash, existing caches are counted and retained. Automatic reclamation
still requires startup proof that their producers stopped and is not implemented
here. Retention avoids unsafe deletion but is not the finished crash-recovery
or disk-budget workflow. Process-wide RSS and Mac capacity remain unmeasured.
