# simplecloud

A tiny Go package for reading and writing objects in different “bucket” backends with a unified interface.
Supports local files, HTTP(s), Backblaze B2, and Google Cloud Storage (GCS).

---

## Features

- **Unified API** for cloud and local storage
- **Backends supported:**
  - Local filesystem (`file://` or bare paths)
  - HTTP(s) (read-only)
  - Backblaze B2 (`b2://bucket`)
  - Google Cloud Storage (`gs://bucket`)
- **Transparent compression**: `.gz`, `.bz2`, `.xz` auto-detected for reads/writes
- **Simple resource lifecycle**: readers/writers are `io.ReadCloser` / `io.WriteCloser`
- **Tiny core**: no extra abstractions beyond what you need

---

## Install

```sh
go get github.com/mtgban/simplecloud
```

## Status

This is a lightweight helper, not a full-featured SDK.
Good for:
- simple pipelines
- moving files between backends
- quick prototypes

Not a replacement for full cloud SDKs (retry logic, ACLs, advanced features).

---

## License

MIT
