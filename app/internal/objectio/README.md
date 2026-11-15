# External-write lifecycle

The application uses `CreateMultipart`, `UploadPart`, `Complete` and `Put` for
data writes. These own the SDK dispatch and propagate its actual error; callers
cannot supply a replacement write callback or silently turn an unknown RPC into
a completed intent by reading an older identical object. `Put` requires a full
GET/size/SHA-256 after its successful response. `Complete` delegates the mandatory
HEAD/full GET/per-part/full-file verification to the conformance-tested adapter.

Each operation first obtains an owned storage epoch, then commits domain
preparation and the immutable intent in one SQLite transaction. Preparation must
validate the current parent/generation and exact bytes/key. Network IO begins
after that transaction. Metadata publication uses its own fenced transaction.
The intent remains live through metadata acknowledgement; known successful RPCs
can settle even if that metadata transaction fails or its caller is cancelled.
The settlement deadline starts after the metadata callback returns.

Admission is bounded and returns `ErrBusy` without forwarding when full.
`Drain` waits for known calls through acknowledgement; admission must first be
closed in the domain transaction. It does not settle uncertain writes. Domain
cleanup must permanently fence the session/job generation, rather than infer
permission from lease age, a quiet interval or an empty listing.
`Read` registers its callback through full body consumption/close; an
all-operation drain conservatively waits all read callbacks because one callback
may access multiple keys. A part-only drain does not wait unrelated reads.
`ReadKey` restricts its reader to one immutable key and registers that key until
the callback has consumed/closed the body and finished downstream writes. Media
serving and verified source copies use it, so deleting one item waits its admitted
streams without waiting unrelated items. A domain recheck after registration
prevents reads admitted after the item's deletion fence from reaching its object.
`RemoveOwned` exposes only reads and idempotent Abort/Delete after the domain's
permanent fence check. Its independent cleanup receipt owns ambiguous removal
results. `ListKey` pages only intents for that exact resource and preserves
unknown payloads rather than skipping them.

Unknown RPCs remain in the independent versioned journal. The same epoch cannot
retire them. After a different epoch, the manager reloads authoritative work and
calls domain-owned reconciliation before deletion. Unknown payload versions are
preserved without invoking cleanup. Read/reconciliation interfaces expose no data
write or epoch-rebinding method; new writes require another journalled operation.
If a known request is still running in this process, epoch recovery first waits
through its metadata acknowledgement and reloads the authoritative journal row;
it does not race that owner or reconcile work it has already settled.
The runtime must verify persistent library/storage/root pairing and the single
owned volume topology before relying on a boot epoch as proof of quiescence.
`librarybinding.Ensure` and `AtLibrary` supply this bootstrap path. A bound
adapter authenticates identity/epoch with the private control nonce protocol,
then adds permanent identity and epoch guards to SDK requests. Foreign volumes
and older gateways cannot supply a false barrier. Startup codec preflight never
settles an external intent just because the application process restarted.

The Complete intent binds a version-1 canonical manifest identity:
`{"version":1,"parts":[{"partNumber":N,"size":S,"etag":E,"sha256":H},...]}`
with contiguous ordered part numbers, opaque ETags and canonical base64 digests.
This identity is not the completed file's SHA-256. The manager copies the manifest
before preparation and checks its size/identity against the intent before S3.

Tests use real SQLite and SDK requests against a controlled HTTP peer to exercise
ordering, failure boundaries and verification. Native ingestion tests also use the
actual owned image for lost responses, storage SIGKILL/restart, verified queuing
and independent cancellation cleanup. Active-session recovery also validates old
requests against their immutable MPU/part/frozen manifest and records extra owned
MPUs for separate cleanup. The primary candidate/ready path also uses typed Put,
full hash verification and independent intent reconciliation after session removal.
Media/process/scratch execution, independent posters, guarded crash-scratch
reclamation, deletion/restart reconciliation and media HTTP streams are tested
too. The application factory now supplies the bound adapter after pairing and
startup recovery and joins HTTP/worker activity before releasing owners. Upload
HTTP/full disk admission and final server/SPA container packaging remain open.
