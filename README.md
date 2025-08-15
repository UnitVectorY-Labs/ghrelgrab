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
