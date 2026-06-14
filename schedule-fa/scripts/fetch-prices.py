#!/usr/bin/env python3
"""Fetch daily close prices into the CSV that `schedulefa --prices` expects.

Usage:
    scripts/fetch-prices.py <year> [tickers-file]

tickers-file lines: <symbol> <yahoo-symbol> <isin> [currency]
  (currency defaults to USD; lines starting with # are ignored)
  default file:  scripts/tickers.txt
Output:          data/prices/prices-<year>.csv   (gitignored)

Source: Yahoo Finance chart API (JSON, no key, Python stdlib only). Uses the RAW
close — NOT the dividend/split-adjusted close — which is what Schedule FA wants.
Weekends/holidays are simply absent; the tool falls back to the preceding day.
"""
import calendar
import csv
import json
import os
import sys
import time
import urllib.request
from datetime import datetime, timezone


def fetch(yahoo_symbol, year):
    p1 = calendar.timegm((year, 1, 1, 0, 0, 0))
    p2 = calendar.timegm((year, 12, 31, 0, 0, 0)) + 86400
    url = (
        f"https://query1.finance.yahoo.com/v8/finance/chart/{yahoo_symbol}"
        f"?period1={p1}&period2={p2}&interval=1d"
    )
    req = urllib.request.Request(url, headers={"User-Agent": "Mozilla/5.0"})
    with urllib.request.urlopen(req, timeout=30) as resp:
        data = json.load(resp)
    result = (data.get("chart") or {}).get("result")
    if not result:
        return []
    result = result[0]
    timestamps = result.get("timestamp") or []
    quote = (result.get("indicators") or {}).get("quote") or [{}]
    closes = quote[0].get("close") or []
    rows = []
    for ts, close in zip(timestamps, closes):
        if close is None:
            continue
        day = datetime.fromtimestamp(ts, timezone.utc).strftime("%Y-%m-%d")
        rows.append((day, f"{close:.4f}"))
    return rows


def main():
    if len(sys.argv) < 2:
        sys.exit("usage: fetch-prices.py <year> [tickers-file]")
    year = int(sys.argv[1])
    here = os.path.dirname(os.path.abspath(__file__))
    tickers_path = sys.argv[2] if len(sys.argv) > 2 else os.path.join(here, "tickers.txt")
    out_dir = os.path.join(here, "..", "data", "prices")
    os.makedirs(out_dir, exist_ok=True)
    out_path = os.path.join(out_dir, f"prices-{year}.csv")

    total = 0
    with open(out_path, "w", newline="") as f, open(tickers_path) as tf:
        writer = csv.writer(f)
        writer.writerow(["date", "symbol", "isin", "close", "currency"])
        for line in tf:
            line = line.strip()
            if not line or line.startswith("#"):
                continue
            parts = line.split()
            if len(parts) < 3:
                print(f"WARN: skipping malformed line: {line}", file=sys.stderr)
                continue
            sym, yahoo, isin = parts[0], parts[1], parts[2]
            currency = parts[3] if len(parts) > 3 else "USD"
            try:
                rows = fetch(yahoo, year)
            except Exception as e:  # noqa: BLE001 - report and continue
                print(f"WARN: {sym} ({yahoo}) failed: {e}", file=sys.stderr)
                continue
            for day, close in rows:
                writer.writerow([day, sym, isin, close, currency])
            tag = f"{len(rows)} rows" if rows else "0 rows (check the Yahoo symbol)"
            print(f"  {sym} ({yahoo}): {tag}", file=sys.stderr)
            total += len(rows)
            time.sleep(0.5)

    print(f"wrote {total} rows to {out_path}", file=sys.stderr)
    if total == 0:
        sys.exit(1)


if __name__ == "__main__":
    main()
