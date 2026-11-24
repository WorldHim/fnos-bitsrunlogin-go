#!/usr/bin/env python3
"""Generate the package icons from code.

Icons are checked in, so this script is only needed when the design changes -
but keeping the generator in the repository means the 64px and 256px variants
cannot drift apart, and nobody has to guess which rasteriser produced them.

The mark is a rounded square in the accent colour with a white "signal" glyph:
two arcs over a solid dot, the conventional Wi-Fi shape, on the diagonal. The
subject of the app is a network login, so the glyph reads at a glance even at
64px in the fnOS launcher.

Usage:
    scripts/fnos/make-icons.py

Writes (all referenced by the package):
    scripts/fnos/project/ICON.PNG            64x64
    scripts/fnos/project/ICON_256.PNG        256x256
    scripts/fnos/project/app/ui/images/icon_64.png
    scripts/fnos/project/app/ui/images/icon_256.png
"""

from __future__ import annotations

import struct
import zlib
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parents[2]
PROJECT = REPO_ROOT / "scripts" / "fnos" / "project"

BACKGROUND = (43, 125, 233)  # #2b7de9, the accent colour of the web UI
FOREGROUND = (255, 255, 255)

# Supersampling factor: draw at 4x and box-filter down, which is enough
# antialiasing for a flat glyph and needs no dependencies.
SS = 4


def write_png(path: Path, pixels: list[list[tuple[int, int, int, int]]]) -> None:
    """Write RGBA pixel rows as a PNG without any third party module."""
    height = len(pixels)
    width = len(pixels[0])

    raw = bytearray()
    for row in pixels:
        raw.append(0)  # filter type 0 (None)
        for r, g, b, a in row:
            raw += bytes((r, g, b, a))

    def chunk(tag: bytes, data: bytes) -> bytes:
        out = struct.pack(">I", len(data)) + tag + data
        return out + struct.pack(">I", zlib.crc32(tag + data) & 0xFFFFFFFF)

    png = b"\x89PNG\r\n\x1a\n"
    png += chunk(b"IHDR", struct.pack(">IIBBBBB", width, height, 8, 6, 0, 0, 0))
    png += chunk(b"IDAT", zlib.compress(bytes(raw), 9))
    png += chunk(b"IEND", b"")
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_bytes(png)


def inside_rounded_rect(x: float, y: float, size: float, radius: float) -> bool:
    """True when (x, y) is inside a rounded square of the given size."""
    if x < 0 or y < 0 or x > size or y > size:
        return False
    cx = min(max(x, radius), size - radius)
    cy = min(max(y, radius), size - radius)
    dx = x - cx
    dy = y - cy
    return dx * dx + dy * dy <= radius * radius


def inside_disc(x: float, y: float, cx: float, cy: float, r: float) -> bool:
    dx = x - cx
    dy = y - cy
    return dx * dx + dy * dy <= r * r


def inside_ring(
    x: float, y: float, cx: float, cy: float, r_outer: float, r_inner: float
) -> bool:
    """True inside the annulus between r_inner and r_outer, on the upper half."""
    if y > cy:
        return False
    dx = x - cx
    dy = y - cy
    d = (dx * dx + dy * dy) ** 0.5
    return r_inner <= d <= r_outer


def render(size: int) -> list[list[tuple[int, int, int, int]]]:
    big = size * SS
    # Leave a small margin so the rounded square does not touch the edge.
    radius = big * 0.22
    # Wi-Fi glyph geometry, in units of the big canvas.
    cx = big * 0.5
    cy = big * 0.66
    dot_r = big * 0.072
    # (inner, outer) radii of the two arcs above the dot.
    ring1 = (big * 0.13, big * 0.205)
    ring2 = (big * 0.255, big * 0.330)

    rows: list[list[tuple[int, int, int, int]]] = []
    for py in range(big):
        row: list[tuple[int, int, int, int]] = []
        for px in range(big):
            x = px + 0.5
            y = py + 0.5
            if not inside_rounded_rect(x, y, big, radius):
                row.append((0, 0, 0, 0))
                continue
            glyph = (
                inside_disc(x, y, cx, cy, dot_r)
                or inside_ring(x, y, cx, cy, ring1[1], ring1[0])
                or inside_ring(x, y, cx, cy, ring2[1], ring2[0])
            )
            r, g, b = FOREGROUND if glyph else BACKGROUND
            row.append((r, g, b, 255))
        rows.append(row)

    # Box filter down to the requested size.
    out: list[list[tuple[int, int, int, int]]] = []
    for py in range(size):
        row = []
        for px in range(size):
            r = g = b = a = 0
            for sy in range(SS):
                for sx in range(SS):
                    pr, pg, pb, pa = rows[py * SS + sy][px * SS + sx]
                    r += pr * pa
                    g += pg * pa
                    b += pb * pa
                    a += pa
            n = SS * SS
            if a == 0:
                row.append((0, 0, 0, 0))
            else:
                row.append((r // a, g // a, b // a, a // n))
        out.append(row)
    return out


def main() -> int:
    targets = {
        PROJECT / "ICON.PNG": 64,
        PROJECT / "ICON_256.PNG": 256,
        PROJECT / "app" / "ui" / "images" / "icon_64.png": 64,
        PROJECT / "app" / "ui" / "images" / "icon_256.png": 256,
    }

    cache: dict[int, list[list[tuple[int, int, int, int]]]] = {}
    for path, size in targets.items():
        if size not in cache:
            cache[size] = render(size)
        write_png(path, cache[size])
        print(f"wrote {path.relative_to(REPO_ROOT)} ({size}x{size}, {path.stat().st_size} bytes)")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
