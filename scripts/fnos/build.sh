#!/usr/bin/env bash
#
# Build a fnOS .fpk package with the official fnpack tool.
#
# This script orchestrates the whole pipeline:
#   1. cross-compile the Go binary for the requested architecture
#   2. stage the package tree (manifest/config/cmd/ui from scripts/fnos/project)
#   3. let fnpack build (and verify) the .fpk
#
# fnpack is the only packer used. There is no fallback archiver: an .fpk that
# was not produced by fnpack is not a package we ship.
#
# Usage:
#   scripts/fnos/build.sh [options]
#
#   -a, --arch ARCH    target architecture: x86 (amd64) or arm (arm64).
#                      Default: x86.
#   -d, --dir DIR      output directory. Default: build/fnos.
#   -f, --fnpack PATH  fnpack executable. Default: fnpack on PATH.
#   -s, --stage-only   stop after staging the tree, do not invoke fnpack.
#   -h, --help         show this help.
#
# Environment:
#   FNPACK   same as --fnpack
#   GO       go executable. Default: go on PATH.
#
# Exit codes:
#   0  success
#   1  usage / configuration error
#   2  build failure
#   3  fnpack unavailable or packaging failed
#
set -euo pipefail

readonly SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
readonly REPO_ROOT="$(cd -- "${SCRIPT_DIR}/../.." && pwd)"
readonly PROJECT_DIR="${SCRIPT_DIR}/project"

ARCH="x86"
OUT_DIR="${REPO_ROOT}/build/fnos"
FNPACK_BIN="${FNPACK:-fnpack}"
STAGE_ONLY=0

# fnOS package platform -> Go GOARCH
readonly PACKAGE_NAME="bitsrunlogin-go"

usage() {
	sed -n '3,32p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'
	exit "${1:-0}"
}

log() { printf '==> %s\n' "$*"; }
warn() { printf 'warning: %s\n' "$*" >&2; }
die() { printf 'error: %s\n' "$*" >&2; exit "${2:-2}"; }

while (($# > 0)); do
	case "$1" in
	-a | --arch)
		ARCH="${2:-}"
		shift 2
		;;
	-d | --dir)
		OUT_DIR="${2:-}"
		shift 2
		;;
	-f | --fnpack)
		FNPACK_BIN="${2:-}"
		shift 2
		;;
	-s | --stage-only)
		STAGE_ONLY=1
		shift
		;;
	-h | --help)
		usage 0
		;;
	*)
		die "unknown option: $1 (try --help)" 1
		;;
	esac
done

case "${ARCH}" in
x86) GOARCH_TARGET="amd64" ;;
arm) GOARCH_TARGET="arm64" ;;
*) die "unsupported arch '${ARCH}' (expected x86 or arm)" 1 ;;
esac

GO_BIN="${GO:-go}"
command -v "${GO_BIN}" >/dev/null 2>&1 || die "go not found (set GO=/path/to/go)" 1

if [[ ! -d "${PROJECT_DIR}" ]]; then
	die "package skeleton missing at ${PROJECT_DIR}" 1
fi

read_package_version() {
	local manifest="${PROJECT_DIR}/manifest" version
	[[ -f "${manifest}" ]] || die "manifest missing at ${manifest}" 1
	version="$(sed -nE 's/^[[:space:]]*version[[:space:]]*=[[:space:]]*(.*)$/\1/p' "${manifest}" | head -n1 | tr -d '[:space:]')"
	[[ -n "${version}" ]] || die "could not read version from ${manifest}" 1
	printf '%s' "${version}"
}

PKG_VERSION="$(read_package_version)"
STAGE_DIR="${OUT_DIR}/${PACKAGE_NAME}-${PKG_VERSION}-${ARCH}"

mkdir -p "${OUT_DIR}"

# ---------------------------------------------------------------------------
# 1. cross-compile
# ---------------------------------------------------------------------------
log "compiling ${PACKAGE_NAME} for linux/${GOARCH_TARGET}"
BIN_DIR="${OUT_DIR}/bin/${ARCH}"
mkdir -p "${BIN_DIR}"
(
	cd "${REPO_ROOT}"
	CGO_ENABLED=0 GOOS=linux GOARCH="${GOARCH_TARGET}" \
		"${GO_BIN}" build -trimpath \
		-ldflags "-s -w -extldflags '-static -fpic'" \
		-o "${BIN_DIR}/bitsrun" ./cmd/bitsrun
) || die "go build failed for ${ARCH}" 2

# ---------------------------------------------------------------------------
# 2. stage the package tree
# ---------------------------------------------------------------------------
log "staging package tree at ${STAGE_DIR}"
rm -rf "${STAGE_DIR}"
mkdir -p "${STAGE_DIR}"
cp -R "${PROJECT_DIR}/." "${STAGE_DIR}/"

# Drop editor / OS noise that must never end up inside the package.
find "${STAGE_DIR}" -name '.DS_Store' -delete
find "${STAGE_DIR}" -name 'Thumbs.db' -delete

# The Go binary lives in app/ and is copied to target/ on install.
mkdir -p "${STAGE_DIR}/app"
cp "${BIN_DIR}/bitsrun" "${STAGE_DIR}/app/bitsrun"
chmod 0755 "${STAGE_DIR}/app/bitsrun"

# Lifecycle scripts must be executable inside the archive.
if [[ -d "${STAGE_DIR}/cmd" ]]; then
	chmod 0755 "${STAGE_DIR}/cmd/"* 2>/dev/null || true
fi

log "staged $(find "${STAGE_DIR}" -type f | wc -l | tr -d ' ') files"

if ((STAGE_ONLY)); then
	log "--stage-only given, skipping fnpack"
	exit 0
fi

# ---------------------------------------------------------------------------
# 3. package with fnpack
# ---------------------------------------------------------------------------
if ! command -v "${FNPACK_BIN}" >/dev/null 2>&1 && [[ ! -x "${FNPACK_BIN}" ]]; then
	cat >&2 <<-EOF
		error: fnpack not found ('${FNPACK_BIN}')

		Download the official build for your platform from:
		  https://developer.fnnas.com/docs/cli/fnpack/

		then either put it on PATH or pass --fnpack /path/to/fnpack.
		The staged tree at '${STAGE_DIR}' is still usable for inspection.
	EOF
	exit 3
fi

# The package is unsigned on purpose: it is distributed through GitHub
# Releases and installed manually. Removing --nosign would make fnpack look
# for a developer certificate and fail.
log "packaging with fnpack (unsigned)"

# fnpack writes the archive into the current working directory, not into the
# directory named by --directory. Running it from OUT_DIR (with an absolute
# --directory) puts the result somewhere predictable; without this the
# archive lands wherever the caller happened to be and gets lost.
STAGE_DIR_ABS="$(cd -- "${STAGE_DIR}" && pwd)"
set +e
(cd -- "${OUT_DIR}" && "${FNPACK_BIN}" build --nosign --directory "${STAGE_DIR_ABS}" 2>&1)
fnpack_status=$?
set -e

if ((fnpack_status != 0)); then
	cat >&2 <<-EOF

		error: fnpack failed (exit ${fnpack_status})

		Note: fnpack 1.3.x is known to fail on Windows with
		  "sync <path>: Access is denied"
		while writing the temporary archive. The package tree itself is valid
		(fnpack gets past 'Verifying files...'). Build the .fpk on Linux, in
		WSL, or let the 'fnos-release' GitHub workflow do it.

		Staged tree: ${STAGE_DIR}
	EOF
	exit 3
fi

# fnpack names the archive after the working directory it ran in, so look in
# OUT_DIR. The staged tree is checked as well: older fnpack builds wrote next
# to the manifest and a silent relocation should not break packaging.
built="$(find "${OUT_DIR}" -maxdepth 1 -name '*.fpk' -print -quit)"
if [[ -z "${built}" ]]; then
	built="$(find "${STAGE_DIR}" -maxdepth 1 -name '*.fpk' -print -quit)"
fi
if [[ -z "${built}" ]]; then
	die "fnpack reported success but produced no .fpk in ${OUT_DIR} or ${STAGE_DIR}" 3
fi

final="${OUT_DIR}/${PACKAGE_NAME}_${PKG_VERSION}_${ARCH}.fpk"
mv -f "${built}" "${final}"
log "package ready: ${final}"
