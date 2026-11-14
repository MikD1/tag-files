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

Unknown RPCs remain in the independent versioned journal. The same epoch cannot
retire them. After a different epoch, the manager reloads authoritative work and
calls domain-owned reconciliation before deletion. Unknown payload versions are
preserved without invoking cleanup. Read/reconciliation interfaces expose no data
write or epoch-rebinding method; new writes require another journalled operation.
The runtime must verify persistent library/storage pairing and the single owned
volume topology before relying on a boot epoch as proof of quiescence.

The Complete intent binds a version-1 canonical manifest identity:
`{"version":1,"parts":[{"partNumber":N,"size":S,"etag":E,"sha256":H},...]}`
with contiguous ordered part numbers, opaque ETags and canonical base64 digests.
This identity is not the completed file's SHA-256. The manager copies the manifest
before preparation and checks its size/identity against the intent before S3.

Tests use real SQLite and SDK requests against a controlled HTTP peer to exercise
ordering, failure boundaries and verification. Native SDK/storage conformance is
separate; the ingestion coordinator and its full native fault matrix remain open.
