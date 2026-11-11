# OCIS Storage Dump Utility (Go)

Inspired by the Python tool [dylarm/ocis-storage-dumper](https://github.com/dylarm/ocis-storage-dumper), reimplemented in Go. Extracts files from an OCIS instance (personal or project spaces) into a standard POSIX directory structure. Ships as a single static binary.

## Build

```bash
go build -o bin/ocis-dump ./cmd/ocis-dump
go test ./...
# embed a version (shown by `ocis-dump --version`)
go build -ldflags "-X main.version=1.0.0" -o bin/ocis-dump ./cmd/ocis-dump
```

## Usage

One binary with three sub-commands:

```
ocis-dump dump [topdir] [outdir]   Dump OCIS storage into a POSIX file tree
ocis-dump mpk <file-or-dir>        Show decoded .mpk metadata files
ocis-dump symlinks <path>          Verify (and repair) the node symlink tree
```

Global flags: `-v, --verbose` and `-q, --quiet` (mutually exclusive; `--quiet` affects `dump`). Use `--help` on any command.

### dump

| Flag | Description |
|------|-------------|
| `topdir` | OCIS storage root. Default: `$HOME/.ocis` |
| `outdir` | Output directory. Default: `.` |
| `-l, --list` | List files without copying |
| `-n, --dry-run` | Show what would be copied |
| `-u, --user NAME` | Filter by user's display name |
| `--username USER` | Filter by actual username |
| `-i, --info` | Only show space info |
| `-f, --force` | Overwrite even if unchanged |
| `-j, --jobs N` | Parallel copy workers (default: 4) |
| `--storage-prefix PATH` | Default: `storage/users/spaces` |

Copies are incremental (size + mtime comparison).

```bash
ocis-dump dump /srv/ocis ./backup
ocis-dump dump -l -u "John Doe"
ocis-dump dump /srv/ocis ./backup --dry-run
```

### mpk

```bash
ocis-dump mpk path/to/node.mpk
ocis-dump mpk /srv/ocis/storage -s -o output.txt
```

### symlinks

```bash
ocis-dump symlinks /srv/ocis --data
ocis-dump symlinks /srv/ocis --metadata --fix
```

Exits with code 2 if symlinks remain incorrect. Flags: `-d/--data` or `-m/--metadata` (one required), `-f/--fix`, `--data-subdir`, `--metadata-subdir`.

## Docker

```bash
docker build -t ocis-dump .
docker run --rm -v /path/to/ocis:/data:ro -v /path/to/output:/output ocis-dump dump /data /output
```

## Development

```bash
make test        # tests with the race detector
make cover       # coverage report
make hooks       # install git hooks (needs prek and a git repo)
make lint        # run all checks (golangci-lint, actionlint, hygiene)
```

Commit messages must follow [Conventional Commits](https://www.conventionalcommits.org/) (`feat: ...`, `fix: ...`, `docs: ...`); the `commit-msg` hook installed by `make hooks` enforces it.

### CI and releases

GitHub Actions runs lint, actionlint and tests on every push and pull request, then builds the multi-arch (`linux/amd64`, `linux/arm64`) Docker image:

| Event | Result |
|-------|--------|
| Pull request | Image is built, not pushed |
| Push to `main` | Pushes `frostyslav/ocis-dump:edge` and `:latest` |
| Tag `v1.2.3` | Pushes `:1.2.3`, `:1.2` and `:1` (the version is embedded in the binary) |

Set these repository secrets for pushing to Docker Hub: `DOCKERHUB_USERNAME` and `DOCKERHUB_TOKEN` (an access token with write permission).
