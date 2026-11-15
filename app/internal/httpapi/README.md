# Same-origin API

Generated strict handlers implement JSON routes from `api/openapi.yaml`.
Owned media and raw upload parts bypass the JSON body limit and binder. Binary
responses are checked against the full source contract in transport tests.

Upload creation commits at most 500 SQL sessions and an operation receipt;
first part transfer initializes/reconciles the MPU. Local and authenticated
remote capacity admission occurs outside SQL. Read-only operation inspection
precedes admission, with an atomic recheck in Execute and another replay check
when admission fails after a concurrent winner committed.

Part PUT validates UUID/number, one generation and canonical SHA-256 header,
known nonempty Content-Length up to 5 GiB and octet-stream Content-Type before
reading a body. Domain checks reject stale sessions and immutable-binding
conflicts before MPU creation, then repeat before S3 dispatch. No handler reads
an entire binary input. Invalid headers and 507 leave the body unread.

Complete closes part admission and enqueues a durable versioned job, returning
202 without S3 work. The queue owns verification/publication after disconnect
or restart. Final ready/failure receipts commit with the domain changes.
An expired known key still checks its request scope before ready lookup;
ready lookup also survives pruning. Sessions/batches contain pending/error
only, with no TTL or history. GET is a passive SQL projection; uncertain part
acknowledgements remain bound and can be resent only with their original bytes.

Retry/cancel job routes operate on upload Complete/ingestion work. Independent
cleanup/deletion jobs continue their resource protocol; they cannot be abandoned
through upload controls. Cancel returns the ready winner, an independent cleanup
receipt, or absence. Polling exposes public state/IDs without object keys or MPU
credentials. Local React/browser, real-image uploads/restart and 20 GiB server
RSS checks now have evidence in `docs/rewrite/ACCEPTANCE.md`; that index also
lists the remaining native-platform and full-library acceptance gates.
