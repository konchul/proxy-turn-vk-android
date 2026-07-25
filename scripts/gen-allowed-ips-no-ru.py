#!/usr/bin/env python3
"""Regenerate app/src/main/res/raw/allowed_ips_no_ru_gz from ipdeny RU zone.

Android Binder rejects huge WireGuard AllowedIPs (TransactionTooLargeException).
We cover each RU network with a containing /12, then emit the IPv4 complement.
That yields a few hundred routes (safe) at the cost of sending some non-RU
addresses in those /12s outside the tunnel.
"""
from __future__ import annotations

import gzip
import ipaddress
import pathlib
import urllib.request

ROOT = pathlib.Path(__file__).resolve().parents[1]
OUT = ROOT / "app/src/main/res/raw/allowed_ips_no_ru_gz"
URL = "https://www.ipdeny.com/ipblocks/data/countries/ru.zone"
# Larger = fewer AllowedIPs, more non-RU IPs also go direct. /12 ≈ 300–400 routes.
COVER_PREFIX = 12


def cover_with_prefix(nets: list[ipaddress.IPv4Network], max_prefix_len: int) -> list[ipaddress.IPv4Network]:
    covered: list[ipaddress.IPv4Network] = []
    for net in nets:
        if net.prefixlen > max_prefix_len:
            covered.append(net.supernet(new_prefix=max_prefix_len))
        else:
            covered.append(net)
    return list(ipaddress.collapse_addresses(covered))


def to_merged_ranges(nets: list[ipaddress.IPv4Network]) -> list[tuple[int, int]]:
    merged: list[list[int]] = []
    for net in sorted(nets, key=lambda n: int(n.network_address)):
        start = int(net.network_address)
        end = int(net.broadcast_address)
        if merged and start <= merged[-1][1] + 1:
            merged[-1][1] = max(merged[-1][1], end)
        else:
            merged.append([start, end])
    return [(s, e) for s, e in merged]


def complement_cidrs(merged: list[tuple[int, int]]) -> list[str]:
    cidrs: list[str] = []
    cursor = 0
    for start, end in merged:
        if cursor < start:
            cidrs.extend(
                str(c)
                for c in ipaddress.summarize_address_range(
                    ipaddress.IPv4Address(cursor),
                    ipaddress.IPv4Address(start - 1),
                )
            )
        cursor = max(cursor, end + 1)
    if cursor <= 2**32 - 1:
        cidrs.extend(
            str(c)
            for c in ipaddress.summarize_address_range(
                ipaddress.IPv4Address(cursor),
                ipaddress.IPv4Address(2**32 - 1),
            )
        )
    return cidrs


def main() -> None:
    lines = urllib.request.urlopen(URL, timeout=60).read().decode().splitlines()
    v4 = [
        ipaddress.ip_network(line.strip(), strict=False)
        for line in lines
        if line.strip() and ":" not in line
    ]
    covered = cover_with_prefix(v4, COVER_PREFIX)
    merged = to_merged_ranges(covered)
    cidrs = complement_cidrs(merged)
    if len(cidrs) > 500:
        raise SystemExit(f"Too many AllowedIPs ({len(cidrs)}); raise COVER_PREFIX")

    payload = ",".join(cidrs).encode()
    OUT.parent.mkdir(parents=True, exist_ok=True)
    with gzip.open(OUT, "wb", compresslevel=9) as gz:
        gz.write(payload)
    print(
        f"Wrote {OUT} (cover /{COVER_PREFIX}, {len(merged)} RU blocks, "
        f"{len(cidrs)} AllowedIPs, {len(payload)} bytes raw, {OUT.stat().st_size} bytes gz)"
    )


if __name__ == "__main__":
    main()
