[![GitHub release](https://img.shields.io/github/release/UnitVectorY-Labs/ghrelgrab.svg)](https://github.com/UnitVectorY-Labs/ghrelgrab/releases/latest) [![License](https://img.shields.io/badge/license-MIT-blue)](https://opensource.org/licenses/MIT) [![Active](https://img.shields.io/badge/Status-Active-green)](https://guide.unitvectorylabs.com/bestpractices/status/#active) [![Go Report Card](https://goreportcard.com/badge/github.com/UnitVectorY-Labs/ghrelgrab)](https://goreportcard.com/report/github.com/UnitVectorY-Labs/ghrelgrab)

# ghrelgrab

Fetches and extracts a specified version of a GitHub release asset from a chosen repository for the current or mapped system architecture.

## Usage

```bash
ghrelgrab \
  --repo owner/repo \
  --version v1.2.3 \
  --file "asset-{version}-{os}-{arch}.tar.gz"
```

Supported flags:

- `--repo` (required): GitHub `owner/repo` (e.g. `owner/repo`)
- `--version` (required): Release tag (e.g. `v1.2.3`)
- `--file` (required): Asset filename, supports `{version}`, `{os}`, and `{arch}` tokens
- `--out`: Output directory (default: current directory)
- `--os`: Override detected OS for `{os}` substitution (default: current OS)
- `--os-map`: Remap detected OS before substitution (e.g. `linux=ubuntu,windows=win32`)
- `--arch`: Override detected architecture for `{arch}` substitution (default: current arch)
- `--arch-map`: Remap detected arch before substitution (e.g. `amd64=x86_64,arm64=aarch64`)
- `--token`: GitHub token (defaults to `GH_TOKEN` env) for private assets
- `--debug`: Enable debug output

The tool automatically extracts `.tar.gz`, `.tgz`, and `.zip` archives. Other files are saved as-is. Output file paths are printed to stdout.


While this application can be used as a command line application, one of its uses is as a docker layer for downloading application dependencies.

```
# Stage 1: fetch the release asset with ghrelgrab 
FROM ghcr.io/unitvectory-labs/ghrelgrab:latest AS fetcher
WORKDIR /work

RUN ["/ghrelgrab","--repo","owner/repo","--version","v1.2.3","--file","asset-{version}-{os}-{arch}.tar.gz","--out","/work","--arch-map","x86_64=amd64,aarch64=arm64","--debug"]

# Stage 2: your runtime
FROM gcr.io/distroless/base-debian12

# If the tar extracts a file named `asset` at /work,
# copy it from there. Adjust the path if the archive has a subdir.
COPY --from=fetcher /work/asset /usr/local/bin/asset

WORKDIR /app

# the rest goes here...
```
