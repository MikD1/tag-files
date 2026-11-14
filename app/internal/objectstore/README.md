# S3 wire profile

The adapter uses AWS SDK for Go v2 `service/s3` 1.114.10 (root SDK 1.48.0,
Smithy 1.28.9), explicit credentials/region, SigV4 and path style. The integration
fixture pins SeaweedFS 4.53 by the index digest in the storage ADR. It uses a
disposable named volume and random credentials, publishes a temporary loopback
test port, disables WebDAV/admin UI/telemetry, applies persisted global fsync,
and removes its containers and volume on completion.

From the repository root:

```sh
docker pull chrislusf/seaweedfs:4.53@sha256:ccad23e18639c8b98cee17e7100b95830a06077b56d8e5b006d6dcf8ec5e94a0
scripts/check-storage
```

The ordinary `scripts/check` suite skips Docker integration explicitly. To
inspect adapter allocations separately from the network/storage process:

```sh
cd app
go test ./internal/objectstore -run '^$' -bench BenchmarkNonseekablePart -benchtime=3x -benchmem
```

Uploads use direct PutObject/UploadPart with explicit ContentLength and SHA256.
The SigV4 payload middleware uses UNSIGNED-PAYLOAD; the explicit checksum still
protects the bytes and the real gateway rejects a wrong digest. No request body
is buffered or rewound by a transfer manager. Each mutation with a body, MPU
creation and Complete makes one SDK attempt: ambiguity is resolved through
durable ownership and reconciliation by the caller. Read/list/delete operations
can retry. Global stream admission belongs to ingestion.

Complete requires a contiguous ordered manifest containing the prior part
bindings. After any successful Complete response it performs HEAD and a full
streamed GET, verifies the size and each part SHA256, and computes the full
object hash. VerifyParts supports the same check after a lost Complete response;
Verify checks an immutable candidate against its expected size/full hash. An
ETag or user metadata hash never satisfies either check.

Observed SeaweedFS 4.53 behavior is part of the tested profile:

- ListParts omits SHA256, including after restart. Its missing value stays
  absent in the adapter. The caller must use immutable durable part bindings;
  Complete refuses a manifest without those digests.
- Identical part resends can leave duplicate records of the same part number.
  ListParts merges records only when all returned size/ETag/checksum fields
  match. Conflicting records fail integrity validation. This normalization does
  not prove byte identity; final verification reads every part from the object.
- Docker may reassign an ephemeral published test port on restart. The fixture
  discovers it again; production uses the stable private `storage:8333` origin.

Primary sources: [AWS nonseekable input](https://docs.aws.amazon.com/sdk-for-go/v2/developer-guide/sdk-utilities-s3.html#unseekable-streaming-input),
[AWS checksums](https://docs.aws.amazon.com/sdk-for-go/v2/developer-guide/s3-checksums.html),
[SeaweedFS 4.53 ListParts implementation](https://github.com/seaweedfs/seaweedfs/blob/4.53/weed/s3api/filer_multipart.go).

The owned gateway additionally exposes a transient boot epoch. `StorageEpoch`
reads it through signed HeadBucket and rejects missing/malformed/ambiguous
headers. `AtEpoch` creates an immutable SDK client with the expected epoch in
its requests. A gateway after restart rejects the stale epoch before forwarding;
the SDK does not automatically resend a consumed body. A durable write intent
must record this same epoch before its request. An epoch change proves no later
acceptance of that old request, not its earlier success/failure; reconciliation
and full object hashes remain necessary. Generic S3 endpoints still support the
ordinary adapter methods, but do not supply this owned-process proof.

This module owns network IO only. It does not enforce session/candidate
ownership or persist bindings, write intents, jobs and cleanup. The deployment image,
bootstrap ordering (including bucket creation), negative readiness preflight,
application streaming/RAM gate and physical Mac durability remain separate
requirements. Container restart is not a power-loss test.
