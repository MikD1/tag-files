# Independent poster work

`Service` owns `poster-v1` jobs after primary publication, including legacy items
without an upload ID. It reads the verified ready primary through bounded
`objectio.CopyVerified`, checks full source bytes, runs `media.PreparePoster` in
guarded scratch, then allocates a fresh immutable candidate for this lease.
Every source/candidate/publication check uses the stored job payload and actual
elapsed writer wait. The item stays ready through poster failure; no upload
session is recreated. `worker.Poster` shares the media failure/retry policy.

Before PUT, one SQLite transaction persists expected bytes/shape/profile and
an independent cleanup receipt/job. Keys are
`previews/<library UUID>/<asset UUID>/poster-v1/poster.jpg`. Every allocation and
lease receives a different asset/key. Existing matching bytes are fully read
and verified; mismatches are never overwritten. PUT uses the journaled I/O
manager and needs full GET/hash before marking verified. A lost response may
be read-recovered while its uncertain write intent remains independently stored.

Leased publication attaches the verified asset, increments the item version
because `posterUrl`/ETag change, removes the completed poster job and its exact
cleanup receipt/job atomically. It leaves primary, user metadata and search
ordering intact. Replay requires the exact already-published attempt; an
arbitrary winner cannot mask an error. Repeating work for an existing profile
finishes its job without replacing bytes. A tombstone/source change or stale
generation refuses publication.

`poster_candidate` receipts contain immutable library/item/source/asset/job/
generation/key/size/SHA identity and survive parent work. Cleanup proves a
permanent publication fence from one short SQL read snapshot: the job is gone,
has a newer generation, or retired into failed/retry_wait. Neither a quiet file
nor an elapsed lease is this proof. A same-generation running attempt refuses
removal. Startup may validate a receipt while its producer runs; this validates
its codec/ownership, without granting cleanup permission.

After the fence, cleanup drains known I/O, preflights every journal page before
removing anything, and removes only this exact candidate key. Uncertain writes
keep the asset/receipt until authenticated same-volume epoch change and another
owned-result reconciliation. SQL removes working records only after no writes
remain. Published lost-response writes use `ReconcileWrites` and full GET/hash,
preserving the published object. Versioned unknown work is retained and refused.

`scripts/check-media` includes real poster preparation/pipeline tests on the
host under race detection. `scripts/check-storage-image` with
`TAG_FILES_MEDIA_INTEGRATION=1` exercises actual owned S3, pairing, SDK, SQLite,
FFmpeg, lost responses, restart, missing scratch and independent cleanup.
The disposable media image tests the byte profile and process/scratch layer;
its CGO SQLite worker is not packaged there. The executable application,
startup factory/background reconciliation, item deletion and media HTTP wiring
remain separate integration work; these modules are not a release-ready server.
