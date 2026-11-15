# Persistent library and storage pairing

`Ensure` establishes one durable pair before runtime admission, worker claims or
external-write reconciliation. The storage marker is committed/synced first;
SQLite then uses a conditional write. A lost reply or failed SQLite transaction
can resume only with the same library/storage/root identity. Neither side has a
runtime rebind/reset endpoint. Schema4 stores the logical storage UUID and the
physical root fingerprint together and makes both immutable once bound.

Application startup must use this sequence:

1. Open the library and hold its process ownership lock.
2. Create the authenticated storage control client from the configured private
   S3 origin/credentials; call `Ensure`.
3. Create the S3 adapter and call `AtLibrary(storageID, libraryID, volumeID)`.
   Pass that adapter to the I/O manager and every runtime storage consumer.
4. Run `ingestion.RecoverInterrupted` before the first job claim. It preflights
   all working payloads and the independent external-write journal without S3.
5. Prove old scratch producers stopped before reclaiming their paths; construct
   one worker and open admission only after the remaining runtime gates pass.

`application.Open` now wires this sequence while holding both library and scratch
ownership, preflights SQL before reclaim, and exposes HTTP/worker execution only
after success. Failed startup releases owners and keeps unsupported work/caches.
`application.Serve` closes admission and joins HTTP/worker before releasing them.
Authenticated capacity and runtime 507 are now wired through `capacity`; upload
HTTP/batch/retry integration and full deployment capacity acceptance remain open.
Direct unbound S3 clients are reserved for isolated fixtures
and standalone offline operations; runtime must not infer pairing from a bucket
name, an empty listing, elapsed lease, or a boot epoch alone.

The control request/response uses HMAC-SHA256, a fresh UUID nonce and bounded
strict JSON. It is on the private storage port, not an application API/CLI/UI.
`StorageEpoch` on a library-bound adapter authenticates the current persistent
identities too. An older gateway that ignores new request headers is refused;
its transient epoch cannot retire a writer from another backend. Each typed I/O
operation then carries the captured epoch and permanent pair headers.

The root fingerprint uses Linux filesystem type/fsid and root inode. A copied
format2 marker on another root is refused before backend startup. Logical UUIDs
are preserved across the internal format1 upgrade; the physical fingerprint is
derived for the owned root. Ext4's fsid derives from its filesystem UUID in the
[Linux implementation](https://github.com/torvalds/linux/blob/v6.18/fs/ext4/super.c).
The actual named-volume container restart passes; physical Mac/VM lifecycle and
other filesystem profiles still need their own evidence.

Cold restore/rehearsal tooling must first validate a frozen generation, then
explicitly rebase the new root fingerprint in both the storage marker and copied
SQLite before ordinary startup. It must preserve logical library/storage UUIDs
and all object identities. That offline workflow is not implemented yet. Pairing
is not a substitute for archive completeness/hash checks, single-app/full-volume
deployment ownership or coherent metadata/storage backup generations.
