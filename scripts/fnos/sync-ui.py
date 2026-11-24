#!/usr/bin/env python3
"""Copy the hand written web UI into the directory the Go binary embeds.

The UI is plain HTML, CSS and JavaScript: there is no transpiler, bundler or
package manager involved, and no npm dependency to install before a build.
That is deliberate - the intended deployment target is a NAS appliance that
may have no internet access, and CI has to be able to produce a release from a
Go toolchain alone.

What this step does buy is a single source of truth: web/ui is edited by hand,
internal/server/dist is what go:embed sees. Both live in git so that a plain
`go build` works without running this script, and this script is what keeps
them in sync (`--check` fails when they drift).

Usage:
    scripts/fnos/sync-ui.py            # copy web/ui -> internal/server/dist
    scripts/fnos/sync-ui.py --check    # verify they match, exit 1 if they do not
"""

from __future__ import annotations

import argparse
import filecmp
import shutil
import sys
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parents[2]
SOURCE = REPO_ROOT / "web" / "ui"
TARGET = REPO_ROOT / "internal" / "server" / "dist"

# Everything the browser requests. Listing them explicitly means an editor
# backup file or a stray note never ends up inside the binary.
ASSETS = ("index.html", "app.css", "app.js")


def missing(root: Path) -> list[str]:
    return [name for name in ASSETS if not (root / name).is_file()]


def sync() -> int:
    absent = missing(SOURCE)
    if absent:
        print(f"error: missing UI source files in {SOURCE}: {', '.join(absent)}", file=sys.stderr)
        return 1

    TARGET.mkdir(parents=True, exist_ok=True)
    for name in ASSETS:
        shutil.copyfile(SOURCE / name, TARGET / name)
    print(f"synced {len(ASSETS)} files to {TARGET.relative_to(REPO_ROOT)}")
    return 0


def check() -> int:
    absent = missing(SOURCE) + missing(TARGET)
    if absent:
        print(f"error: missing UI files: {', '.join(absent)}", file=sys.stderr)
        return 1

    drifted = [
        name
        for name in ASSETS
        if not filecmp.cmp(SOURCE / name, TARGET / name, shallow=False)
    ]
    if drifted:
        print(
            "error: the embedded UI is out of date: "
            + ", ".join(drifted)
            + "\n       run scripts/fnos/sync-ui.py and commit the result",
            file=sys.stderr,
        )
        return 1

    print(f"embedded UI is up to date ({len(ASSETS)} files)")
    return 0


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--check",
        action="store_true",
        help="verify internal/server/dist matches web/ui instead of updating it",
    )
    args = parser.parse_args()
    return check() if args.check else sync()


if __name__ == "__main__":
    raise SystemExit(main())
