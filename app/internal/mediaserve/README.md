# Same-origin media delivery

`Service` admits only verified/published assets owned by a visible item and a
canonical library key. It excludes candidates, foreign keys and deleting items.
Admission registers the exact key in `objectio` before rechecking SQL ownership;
the registration lasts through body close and downstream writes. Deletion hides
new requests and waits already admitted streams. No S3 call holds a SQL writer.

The handler implements GET/HEAD, strong/weak conditional comparisons, exact
If-Range, single and bounded multipart byte ranges. HEAD ignores Range. Unknown
range units are ignored, invalid/unsatisfiable byte ranges return 416, and range
count/header/aggregate limits prevent amplification. Each upstream GET pins the
HEAD's opaque ETag; it is never interpreted as a content hash. See
[RFC 9110](https://www.rfc-editor.org/rfc/rfc9110.html).

Sixteen concurrent streams by default use 32 KiB body buffers and backpressure;
slots and upstream reads honor request cancellation. Actual full size/hash
verification belongs to immutable asset publication, rather than buffering a
file before responding. Upstream size, range and ETag must match. Errors before
response bytes become Problem Details; a later failure aborts the stream, so a
truncated binary body is never completed with JSON.

Explicit raster/video MIME types can render inline. Legacy Unknown/Text/Audio
and active or unsupported MIME types download as octet-stream with a generated
filename, no-sniff and a sandbox CSP. Existing object bytes and metadata remain
unchanged. Serving media never records a view. An actual primary 404 marks its
availability missing; a poster 404 leaves the primary ready and queues a durable
poster rebuild in the same fenced transaction.

Tests run the production SQLite, HTTP adapter and AWS SDK against a controlled
HTTP peer for conditional/range semantics, safe downloads, cancellation,
truncation and concurrent deletion. The owned-image worker test additionally
exercises real SeaweedFS media requests, missing-poster regeneration and the
subsequent deletion/restart protocol. `BenchmarkMediaHTTPStreaming` compares
8/64 MiB streamed allocation behavior; its loopback speed is not a production
throughput or RSS measurement.
