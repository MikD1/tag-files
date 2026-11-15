# Durable multipart checksum proof in the owned gateway

Scope: satisfy 02:192 / 07:131 without changing pinned SeaweedFS 4.53 or the
application's immutable bindings and full post-Complete verification. No source
data migration, publication, shared Docker tag, or application feature change.

## Proof and authentication

- Authenticate the pinned header-based AWS SDK SigV4 request by re-signing the
  original signed-header subset with the pinned AWS signer (S3 URI escaping).
  Require signed checksum/length and the signed method/path/query tuple; reject
  ambiguous query/header values, malformed digests and unsupported streaming
  signing profiles. Do not implement another canonical-signature algorithm.
- After authentication and before forwarding UploadPart, durably bind
  storage ID / bucket / key / upload ID / part number / length / canonical SHA.
  Other length/SHA for the tuple is 409 before body forwarding. Reject an existing
  native part without a gateway binding; never derive proof from app SQLite or
  legacy request headers.
- Stream the body through a SHA256 and exact-byte counter. A validated proof
  requires actual length/hash, backend success and matching backend checksum
  plus nonempty ETag. Persist/fsync the proof before acknowledging the client.

## Files and recovery

- Versioned, strict, bounded per-part JSON files under a private root inside
  `/data`; safe SHA-derived paths, no untrusted path components, no symlink or
  nonregular-file traversal, no silent repair of malformed/unknown metadata.
- Atomic file replacement uses file fsync, rename and parent-directory fsync;
  directory creation and format/ownership markers are also synced. Reload
  persisted state after uncertain writes rather than trusting a stale cache.
- Same-part writes serialize until acknowledgement commit. Complete/Abort first
  persists a closed per-MPU admission fence under the same lock used to admit
  new part writers, then waits admitted writers; terminal RPCs serialize too.
  Neither a timeout nor client
  disconnect proves the backend writer stopped: persist uncertainty and retain
  metadata until a backend-epoch barrier if needed.
- A bound but unproven request can retry only the same size/SHA. Crash after
  backend acceptance before proof requires an identical resend. Crash after
  proof before client ACK is resolved by authoritative ListParts.
- A new dispatch durably suppresses any historical proof before network I/O.
  ListParts requires no pending attempt and no unresolved earlier writer. A
  later valid ACK settles only its own request; an earlier uncertain writer
  remains fenced until the old backend is dead. At that epoch barrier retain
  the binding but invalidate historical validation: a fresh actual-byte resend
  must prove which bytes are currently stored. Only the pinned native backend's
  exact HTTP 400 `Error/BadDigest` no-write rejection settles a negative attempt;
  arbitrary HTTP errors and success with a mismatched actual body do not.
- Retry existing directories after any failed mkdir/parent fsync by syncing the
  parent again. Before dispatch, positive ACK, or ListParts SHA, synchronize the
  `/data` entry for the journal root, the root entry for the MPU directory, and
  the MPU directory itself. Visible file/directory entries are not durability
  evidence without this complete resynchronization.

## Listing and terminal cleanup

- Obtain native ListParts through the existing signed request. Preserve native
  pagination, selectors, ordering and opaque ETags; enrich only exact
  number/size/ETag matches with the independently proven canonical SHA.
  No invented parts/checksums. Missing/conflicting proof fails closed and keeps
  source parts. Bound response metadata per page; never buffer a part/file.
- Cleanup is not age-based. Normal terminal cleanup needs all known writers
  drained, no uncertain accepted requests, and authoritative MPU absence.
  Otherwise retain until a new owned backend epoch, before opening the network,
  when the previous child is dead and an authoritative absence check proves no
  future writer can recreate this MPU. Then delete/fsync only the known proof
  directory. Full `/data` cold backup includes all remaining proof state.
- Existing native MPU parts without proof remain intact and explicitly blocked.
  New empty part slots may create fresh bindings. No legacy checksum fabrication
  or automatic cancel/delete is authorized.

## Tests and implementation sequence

1. RED authentication/binding/proof persistence tests, then implement journal and
   signer validation using only `multipart_proof*.go`.
2. RED fake-backend streaming, retry, lost-ACK, missing/corrupt/symlink proof and
   pagination tests; implement the smallest gateway hook plus per-part proof
   handler, preserving existing pairing/epoch gates and proxy behavior.
3. Terminal cleanup and reopen/restore tests, including retained uncertainty and
   the explicit newer-backend absence barrier.
4. Dedicated native integration fixture: unique image tag, owned storage,
   independent source digests, actual body mismatch, changed binding, lost ACK,
   restart, proof paging and full post-Complete SHA. Inspect/clean only its own
   disposable containers, volumes and image tag.
5. Fresh affected vet/race checks and the native fixture. Report evidence and
   remaining physical durability/platform gates. Parent owns strict runtime
   ListParts consumers, shared conformance assertions and specification updates.
