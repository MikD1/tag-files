# Distribution sources

All license, copyright and source files here retain their original contents.
The Alpine BusyBox packaging archive repacks original `main/busybox` files
without changing their bytes.

- SeaweedFS 4.53 is copied without rebuilding from
  `chrislusf/seaweedfs:4.53@sha256:ccad23e18639c8b98cee17e7100b95830a06077b56d8e5b006d6dcf8ec5e94a0`.
  The executable identifies commit `4ab66fb76da83700739f5839e62fcaf15fb7d02a`,
  main module `v0.0.0-20261108181033-4ab66fb76da8`, Go 1.26.10, CGO disabled.
  [Official source](https://github.com/seaweedfs/seaweedfs/tree/4ab66fb76da83700739f5839e62fcaf15fb7d02a).
  Actual dependency LICENSE/NOTICE/COPYING/COPYRIGHT files are collected
  recursively, including `weed/glog/LICENSE` and embedded static assets, after
  matching downloaded module sums to the executable. Root LICENSE, Go LICENSE,
  module sums, build metadata and binary hash are under `../upstream/seaweedfs`.
  This revision has no root NOTICE; nested notices retain their relative paths.
- The nested `github.com/google/flatbuffers/go` module
  `v0.0.0-20230108230133-3b8644d32c50` omits the repository-root license from
  its module archive. Its version-scoped supplemental `LICENSE.txt` is the
  unmodified original from
  [the matching full commit](https://github.com/google/flatbuffers/blob/3b8644d32c50e86872584829af4b8caf180235ab/LICENSE.txt).
  It supplements the verified module archive and cannot cover other versions.
- BusyBox 1.37.0-r31 and musl 1.2.6-r3 come from
  `alpine:3.24.4@sha256:3f4d6b32e26703c6082839234f43769d5046b3ac7c910033911d1e71d8cd630a`.
  Only BusyBox/applets, the musl loader/libc, generated passwd/group and
  directories enter the scratch image. Exact package identities and file hashes
  are recorded one directory above. No package manager, OpenSSL, FUSE, curl or
  Rust executables are shipped.
- `BUSYBOX-LICENSE`, `BUSYBOX-AUTHORS` and `busybox-1.37.0.tar.bz2` come from the
  [official release](https://busybox.net/downloads/busybox-1.37.0.tar.bz2).
  Complete corresponding source includes that archive plus the recipe, config
  and patches in `busybox-alpine-aports.tar.gz`, from
  [Alpine's exact package commit](https://github.com/alpinelinux/aports/tree/c3ef5d10e6ef6528852c51f0564963e2f8c1be19/main/busybox).
- BusyBox's Alpine build recipe enables UTMP support and statically links
  utmps/skalibs. Recipes at that pinned package-source commit select utmps
  0.1.3.2 and skalibs 2.14.5.0. Their original ISC `COPYING` files, complete
  official source archives and Alpine recipes are included separately:
  [utmps source](https://skarnet.org/software/utmps/utmps-0.1.3.2.tar.gz),
  [skalibs source](https://skarnet.org/software/skalibs/skalibs-2.14.5.0.tar.gz).
  Those versions come from source/build provenance, rather than a version
  self-report embedded in BusyBox.
- `MUSL-COPYRIGHT` is the unmodified
  [musl 1.2.6 copyright/license](https://git.musl-libc.org/cgit/musl/plain/COPYRIGHT?h=v1.2.6).
- The unchanged CA bundle comes from the pinned SeaweedFS image, whose
  `ca-certificates-bundle` package is 20261020-r0.
  `ca-certificates-20261020.tar.bz2` is the
  [official Alpine source](https://gitlab.alpinelinux.org/alpine/ca-certificates/-/archive/20261020/ca-certificates-20261020.tar.bz2),
  verified using SHA-512 from the
  [pinned package recipe](https://github.com/alpinelinux/aports/blob/e78f5987166c6ffbe23f6472e2e8f040298fe2da/main/ca-certificates/APKBUILD).
  `CA-certdata.txt`, `CA-c_rehash.c`, `CA-update-ca.c` and `CA-README.md` retain
  original source/copyright text. `CA-MPL-2.0` and `CA-MIT.txt` retain full
  [Mozilla MPL 2.0](https://www.mozilla.org/media/MPL/2.0/index.txt) and
  [SPDX MIT license text](https://github.com/spdx/license-list-data/blob/v3.27.0/text/MIT.txt).

The Tag Files supervisors have separate actual Go-module notices, Go 1.27.4
LICENSE and the project's original MIT LICENSE one directory above.
