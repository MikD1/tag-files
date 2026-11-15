# Media execution profile

This module processes owned local scratch files. It does not publish S3 objects,
remove the durable incoming source, own SQLite transactions, or accept browser
MIME claims. The ingestion worker supplies guarded scratch; the executable app
startup and poster execution are not wired yet.
`domain.MediaOutput` is shared with ingestion's immutable candidate protocol.

The tested Linux profile is Ubuntu 26.04, FFmpeg package `7:8.0.1-3ubuntu2`.
The base index digest and package version are pinned in `deploy/app/Dockerfile`.
The current Docker targets are a runtime foundation and disposable test image;
there is no rewritten server/SPA image yet. Run `scripts/check-media` for host
race tests, or `scripts/check-media-image` for the pinned nonroot image with a
read-only root, bounded tmpfs and no external network. Image tests are ordinary
Go test binaries; host checks supply the race detector.

All ffprobe/FFmpeg execution uses `internal/process`: direct argv, separate Linux
process group, bounded stdout/stderr, TERM/grace/KILL, Wait and owned descendant
reaping. Subreaping is enabled process-wide, but Wait4 only consumes the owned
process group after its leader has been reaped. A leader that leaves descendants
is an error even when it reports success. Cancellation also interrupts local
copy/hash loops between reads.

Every command receives a Linux parent-death SIGKILL. The forking Go thread stays
locked through Wait because this signal follows the creating thread's lifetime.
Guarded scratch contexts also supply inherited root/attempt lock descriptors;
surviving descendants prevent scratch reclamation until their last close. See
[`scratch`](../scratch/README.md). This relies on the pinned tools retaining
their guards, rather than inferring quiescence from job age. The Linux contracts
are documented in [PR_SET_PDEATHSIG](https://man7.org/linux/man-pages/man2/PR_SET_PDEATHSIG.2const.html)
and [flock](https://man7.org/linux/man-pages/man2/flock.2.html).

Input protocols are restricted to local files, with bounded probe bytes/time,
stream count, decoder allocation and frame pixels. Formats/codecs have explicit
allowlists. Audio-only files and attached cover art do not become video uploads.
Selected default/first non-cover video and default/first audio are the working
tracks. Other auxiliary video tracks are not copied by this single-version profile.
A format/codec being enabled is not evidence that every variant is accepted.

HEIF primary selection also considers default tile groups, excluding dependent
tile streams from independent-image selection. The bounded profile admits up to
256 streams/tiles. It validates the whole coded/visible canvas, group-relative
stream references, nonoverlapping complete coverage and crop bounds. HEIC grids
assemble all tiles before cropping and applying primary rotation/mirroring;
AVIF keeps original bytes after all selected tiles decode. Grid probing/decoding
uses one decoder thread per tile. This is not a measured whole-process RSS cap.

FFprobe 8 omits the grid's coded transform side data. A bounded ISO box reader
therefore reads only HEIF metadata (4 MiB and 4096 boxes), resolves the selected
item's property associations, including wide IDs/indices, and applies transforms
in their stored order. HEIC auxiliary alpha, ICC/HDR conversion, clean-aperture/
scaling and overlapping overlay profiles currently return unsupported codec;
they are not silently flattened, recolored or resized. Broader color/alpha and
overlay acceptance remains open.

Usable JPEG/PNG/WebP/GIF/AVIF may pass through only after full decode. Other
supported photos convert to PNG. Video converts to MP4 H.264/AAC, veryfast CRF23,
128k audio, yuv420p and faststart, matching the legacy video settings. No resize
or padding filter is applied. Autorotation is checked against the output shape
and cleared output rotation; output is decoded fully before its hash/descriptor.
Odd video dimensions and HDR transfer profiles currently report unsupported codec
rather than silently resizing or claiming a tone-mapping implementation.
Animated PNG uses `image/png` and remains animated through byte preservation;
GIF likewise retains the original animation. JPEG EXIF is frame metadata in
this FFmpeg profile: normal JPEG remains byte-identical, while non-normal EXIF
orientations convert to PNG with the complete transform baked into pixels.
Eight orientation/mirror variants check displayed dimensions and corner colors.

Defaults are two codec threads, 64 million pixels, 20 GiB output, 30s probe and
6h conversion. These are explicit initial resource limits, not measured Mac
capacity. Output is exclusive, partial output is removed on failure, input stays
intact, and the byte maximum is inclusive. FFmpeg's file-size stop is set one byte
past that maximum; the final size/hash and output decode are still checked.
Scratch free-space reservation, filesystem monitoring/507 and whole-app RSS
measurement remain gates. The process runner bounds captures, not total codec RSS.

Duration uses timestamp/time-base rational arithmetic or fixed decimal seconds,
rounded to nearest 100 ns (ties up). Unknown duration stays NULL; invalid syntax,
negative duration and int64 overflow are rejected. Exponents are rejected before
big-number allocation. Existing legacy ticks never pass through this module.

Actual fixtures currently cover PNG byte identity despite a misleading filename,
BMP conversion, invalid/truncated input, unsupported FFV1, audio-only MP4 and M4A
cover art, MPEG4/PCM to H264/AAC, silent rotated video, faststart, exact ticks,
pixel/output limits, cancellation and a blocked external playlist URL. Broader
fixtures now include JPEG EXIF (all eight orientations), animated PNG/GIF,
single and tiled HEIC/AVIF, primary-vs-thumbnail, group transforms, corruption in
the last tile and whole-canvas limits. Animated WebP, HDR/color, aspect/VFR,
>4K and the real Mac/browser corpus still need their acceptance evidence
before this is a complete production media matrix.

Primary references: [FFmpeg options](https://ffmpeg.org/ffmpeg.html),
[ffprobe sections](https://ffmpeg.org/ffprobe.html),
[protocol allowlists](https://ffmpeg.org/ffmpeg-protocols.html),
[MOV external references](https://ffmpeg.org/ffmpeg-formats.html#mov_002fmp4_002f3gp).
The group mapping follows [AVStreamGroupTileGrid](https://ffmpeg.org/doxygen/8.0/structAVStreamGroupTileGrid.html);
the probe omission and relative index are visible in the
[pinned ffprobe source](https://github.com/FFmpeg/FFmpeg/blob/n8.0.1/fftools/ffprobe.c).
The native gate caught that the old rotate metadata fixture did not create a
display matrix in this build; the fixture now uses `-display_rotation` and ffprobe
checks that the matrix exists before exercising conversion.
