# Physical disk admission

One manager owns admission for the exclusively owned library. It reads a bounded
SQLite working-state snapshot, commits that read transaction, then observes
metadata/scratch filesystems and authenticated storage capacity. Its gate
serializes observations with the caller's SQL-only creation/retry commit. Reading
demand first keeps concurrent acknowledgements conservative; no S3 observation
holds a SQL transaction. Reopening derives pending demand from durable rows.

The forecast includes:

- Unverified inputs: MPU plus configured concurrent Complete extents (three
  source sizes at the default concurrency of two), minus confirmed part bytes.
  Concurrent resend allowance uses the largest persisted/new part and write count.
- One serial primary output cap plus poster cap and source/output scratch peak.
  Every unfinished input stays counted; 500 queued photos share one media working
  set. Each processing attempt measures again before executing.
- Running independent posters, current writes and full uncertain/orphan payloads,
  including writes whose parent metadata disappeared. Every intent keeps control
  metadata headroom. The authoritative journal decoder refuses unknown codecs.
- Retained scratch, actual allocated active bytes, unwritten reservations,
  configured free bytes/inodes and scratch quota. Sparse length and unrelated
  files cannot reduce a producer's physical reservation.

Ext4 roots sharing a filesystem identity use one byte pool: demands are added
and the lowest observed free value is used. Distinct ext4 filesystems are checked
separately. Unknown/stacked backing conservatively combines the pools. Unknown
capacity/inodes refuse new work. Arithmetic is checked before addition and public
counters stay within the exact JSON integer bound.

`Admit` with `Files`/`Retry` owns the creation/retry transaction boundary. The
runtime coordinator checks before MPU creation, part bodies and completion;
the runner checks before ingest/poster execution. Ready/source replay and
cleanup/deletion stay available. Metadata-only commands check local headroom;
operation replay is resolved first. Forecast refusal and recognized SQLite FULL,
OS ENOSPC/EDQUOT and S3 insufficient-storage errors map to 507. Observation
failure maps to 503. Inputs, bindings and uncertain work remain retained.

`TAG_FILES_MIN_FREE_BYTES` sets the development command's reserve (default
1 GiB, also applied to scratch); runtime inode reserve defaults to 1024.
`/api/v1/system/status` reports measured or explicitly unknown capacity for all
three roots. Measurements describe guest filesystems and configured headroom;
Docker disk-image limits and Mac host free space still need the external
operational checks required by the storage acceptance plan.

Tests cover shared/separate/unknown backing, exact thresholds, overflow, sparse
and unrelated scratch, concurrent 500-file admission/reopening, operation replay,
deletion, real SQLite page-limit FULL and actual SDK errors. The owned-image
ENOSPC fixture fills a disposable 256 MiB RAM-backed storage volume, refuses a
part before reading its body, keeps the committed source outcome, frees the
fixture and verifies the original full hash. Separate disk-volume restart suites
supply persistence evidence. Upload HTTP and full deployment/browser/20 GiB/RSS/
Mac acceptance remain required.

Kernel interfaces: [statfs](https://man7.org/linux/man-pages/man2/statfs.2.html),
[allocated blocks and holes](https://man7.org/linux/man-pages/man7/inode.7.html),
[ext4 identity](https://github.com/torvalds/linux/blob/v6.18/fs/ext4/super.c).
