# fnOS packaging

Tooling that turns this repository into an installable fnOS `.fpk` package.

Everything here is driven by the official [`fnpack`](https://developer.fnnas.com/docs/cli/fnpack/)
utility. No alternative archiver is used: `fnpack` both verifies the package
layout and produces the final archive.

## Layout

| Path | Purpose |
| --- | --- |
| `build.sh` | Orchestrates cross-compile, staging, and `fnpack build` |
| `project/` | Static package skeleton: `manifest`, `config/`, `cmd/`, `ui/`, icons |

`project/` is copied verbatim into a staging directory on every build; the Go
binary is injected as `app/bitsrun` afterwards. Nothing in `project/` is
generated, so it can be edited like any other source file.

## Usage

```bash
# build the x86 package
./scripts/fnos/build.sh

# build the arm package
./scripts/fnos/build.sh --arch arm

# build both
./scripts/fnos/build.sh --arch x86
./scripts/fnos/build.sh --arch arm

# only stage the tree, skip fnpack (useful for inspecting the layout)
./scripts/fnos/build.sh --stage-only
```

Output goes to `build/fnos/`:

```
build/fnos/
├── bin/x86/bitsrun                     # cross-compiled binary
├── bitsrunlogin-go-1.6.8-x86/          # staged tree handed to fnpack
└── bitsrunlogin-go_1.6.8_x86.fpk       # final package
```

## Packages are unsigned

`build.sh` always passes `--nosign`. These packages are distributed through
GitHub Releases and installed with **App Center → Manual Install**; they are not
submitted to the fnOS App Store. Cleaning up the verification that signing
provides is not planned — see `docs/fnos.md` for the manual-install steps.

## Building on Windows

`fnpack` 1.3.x fails on Windows while writing its temporary archive:

```
Packing failed. Compress ... file error : sync ...\.appname.fpk-NNN: Access is denied.
```

The failure happens after `Verifying files...` succeeds, so the package tree is
valid; only the archive write fails. It reproduces on every drive and path we
tried, and `os.Rename` / directory fsync behave normally in the same location,
so this is a limitation of the Windows build of `fnpack`, not of this
repository.

Build on Linux, in WSL, or via the `fnos-release` GitHub workflow instead.
`build.sh` detects the failure and prints the same guidance.

## Adding a new architecture

1. Add the `platform` value to `project/manifest` if the new platform needs it
   (fnOS accepts `x86`, `arm`, or `all`).
2. Extend the `case "${ARCH}"` block in `build.sh` with the matching `GOARCH`.
