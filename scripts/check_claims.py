#!/usr/bin/env python3
"""Validate the evidence ledger's shape and status values."""
import csv
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
LEDGER = ROOT / "evidence" / "claims.csv"
HEADER = ["claim_id", "text", "kind", "source_or_run", "commit", "script", "status", "verified_on"]
STATUS = {"✅", "⚠️", "❌", "❓"}

def main() -> int:
    with LEDGER.open(newline="", encoding="utf-8") as f:
        reader = csv.DictReader(f)
        if reader.fieldnames != HEADER:
            print(f"bad header: {reader.fieldnames}", file=sys.stderr)
            return 1
        rows = list(reader)
    seen = set()
    for row in rows:
        cid = row["claim_id"]
        if not cid or cid in seen:
            print(f"duplicate or empty claim id: {cid!r}", file=sys.stderr)
            return 1
        seen.add(cid)
        if row["status"] not in STATUS:
            print(f"{cid}: bad status {row['status']!r}", file=sys.stderr)
            return 1
        if row["kind"] not in {"own-run", "primary-source"}:
            print(f"{cid}: bad kind {row['kind']!r}", file=sys.stderr)
            return 1
        if not row["source_or_run"].strip():
            print(f"{cid}: empty source", file=sys.stderr)
            return 1
    counts = {}
    for row in rows:
        counts[row["status"]] = counts.get(row["status"], 0) + 1
    print(f"claims={len(rows)} status={counts}")
    return 0

if __name__ == "__main__":
    raise SystemExit(main())
