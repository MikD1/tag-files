# Item deletion

`Request` atomically hides a ready/missing item as `deleting`, checks its version,
strictly decodes and removes poster jobs, snapshots owned resources, advances
search generation, and creates an independent deletion receipt/job. A repeated
DELETE reuses pending work, including after item metadata is gone; after verified
completion it returns absent. Unknown work or a stale editor rolls back the
whole command. The HTTP route requires If-Match and returns 202/jobId or 204.
There is no new UI action, trash, Undo, expiry, or application tools command.

Schema 5's `deletion_assets` stores a normalized inventory in bounded pages. It
captures every attached primary/poster key with expected bytes, MIME, role and
publication attempt. For a new uploaded item it also captures the exact known
`incoming/<library>/<upload>/source` key and upload UUID, even if the earlier
ready-cleanup receipt has finished. Incoming byte facts are NULL, rather than
invented from a converted primary. Names never supply storage paths. Ownership
is immutable; keys are processed in unique key order, so upload and asset UUIDs
need not belong to one identity namespace.

Independent ready/orphan cleanup receipts finish first. The deletion worker then
preflights every inventory/journal page before I/O, drains known readers/writers,
and removes only owned primary/poster/incoming resources. Incoming cleanup lists
and aborts only MPUs for that exact key, then repeats HEAD/Delete; prefix-neighbor
uploads remain untouched. Unknown writes retain the tombstone and inventory
until the authenticated same-volume storage epoch barrier and owned-result
reconciliation. Generation/lease age supplies no removal permission.

Before DELETE or Abort, a per-resource epoch checkpoint commits to SQLite.
The actual job lease and a compare-and-swap prevent a delayed request from
replacing a newer checkpoint with an older lifetime. I/O never runs inside a
SQL transaction. Once writers/resources/dependent receipts are settled, metadata
removal cascades item assets/tags/views; the recovery inventory remains separate.
The computed collection cover already follows the next visible oldest item.

Minimal keys/receipt/job remain until they are rechecked absent, including MPUs,
at a later authenticated storage lifetime than their last DELETE/Abort/first
absence observation. An object reappearing after restart is deleted again and
its checkpoint advances. Only final verification removes inventory/receipt/job.
`worker.Deletion` keeps this working state retryable with bounded delay instead
of exhausting an attempt counter; diagnostics expose
`deletion_awaiting_storage_restart`. An old Complete cannot recreate an item.

Startup preflights delete job/receipt/inventory codecs and fences interrupted
leases without removing keys. Tests use the production SQLite driver and real
SDK, including future codecs/pages, immutable ownership, writer independence,
lost DELETE responses, resurrection, stale checkpoints, dependent poster work,
HTTP replay, schema upgrade and actual owned-storage restart. Whole-VM/Mac
power-loss behavior remains part of final durability acceptance, separate from
process-restart tests. The application runtime, broader media/HTTP and final
release remain in progress.
