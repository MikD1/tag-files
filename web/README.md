# React client

Node26.13.0, exact dependency versions and `package-lock.json` are required.

```sh
npm ci
npm run dev
npm run build
npm test
npm run test:browser
```

Vite proxies `/api` and `/media` to127.0.0.1:8080 (override
`TAG_FILES_API_ORIGIN`). The production app image serves the build at the same
origin as Go HTTP/media; runtime assets never depend on a CDN.

The generated API contract is in `src/api/schema.d.ts`. Query owns shared server
state; URLs own filters/navigation origin and localStorage stores versioned
preferences/upload command receipts. Browser File contents are not persisted.
Uploads continue across routes and stop polling when no executable work remains.

The browser fixture uses the real HTTP API and SQLite with generated PNG media.
`scripts/check-app-image` separately checks actual built containers, native S3,
worker hashing, Blob XHR, processing, media Range and full pair restart.
`scripts/check-large-upload` runs the actual20GiB Go proxy/RSS acceptance.
WebKit automation does not establish shipping Safari, codecs or VoiceOver.

Accepted v27 sources in `docs/rewrite/prototype` remain immutable. The native
inline viewer was selected after the exact YARL3.33.1 component gate showed
rotation discarding current zoom and interception of browser zoom shortcuts.
