#!/usr/bin/env python3
"""Fetch daily closes (and FX rates) into the CSVs `correlation compute` expects.

Uses the Yahoo Finance chart API (JSON, no key, Python stdlib only).

Prices — writes columns: date,symbol,close,currency
    scripts/fetch.py prices <start> <end> [tickers-file] > data/prices.csv

    tickers-file lines: <label> <yahoo-symbol> [currency]
      (currency defaults to USD; lines starting with # are ignored)
      default file: scripts/tickers.txt
    Example line:  VWRA  VWRA.L  USD
                   Nifty50  ^NSEI  INR

FX — writes columns: date,currency,rate   (rate = value of 1 unit of <currency>
in the base currency, e.g. INR per USD)
    scripts/fetch.py fx <start> <end> <currency>:<yahoo-symbol> [...] > data/fx.csv
    Example:  scripts/fetch.py fx 2020-01-01 2024-12-31 USD:INR=X > data/fx.csv

<start>/<end> are YYYY-MM-DD (inclusive). Weekends/holidays are simply absent;
the tool resamples and (for FX) falls back to the preceding available day.
"""
import calendar
import csv
import json
import os
import sys
import urllib.request
from datetime import datetime, timezone


def _epoch(day):
    d = datetime.strptime(day, "%Y-%m-%d")
    return calendar.timegm((d.year, d.month, d.day, 0, 0, 0))


def fetch(yahoo_symbol, start, end):
    p1 = _epoch(start)
    p2 = _epoch(end) + 86400
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
        rows.append((day, close))
    return rows


def cmd_prices(argv):
    if len(argv) < 2:
        sys.exit("usage: fetch.py prices <start> <end> [tickers-file]")
    start, end = argv[0], argv[1]
    here = os.path.dirname(os.path.abspath(__file__))
    tickers_path = argv[2] if len(argv) > 2 else os.path.join(here, "tickers.txt")
    w = csv.writer(sys.stdout)
    w.writerow(["date", "symbol", "close", "currency"])
    with open(tickers_path) as fh:
        for line in fh:
            line = line.strip()
            if not line or line.startswith("#"):
                continue
            parts = line.split()
            label, yahoo = parts[0], parts[1]
            currency = parts[2] if len(parts) > 2 else "USD"
            for day, close in fetch(yahoo, start, end):
                w.writerow([day, label, f"{close:.4f}", currency])


def cmd_fx(argv):
    if len(argv) < 3:
        sys.exit("usage: fetch.py fx <start> <end> <currency>:<yahoo-symbol> [...]")
    start, end = argv[0], argv[1]
    w = csv.writer(sys.stdout)
    w.writerow(["date", "currency", "rate"])
    for spec in argv[2:]:
        currency, _, yahoo = spec.partition(":")
        if not yahoo:
            sys.exit(f"bad fx spec {spec!r}; want CURRENCY:YAHOO-SYMBOL")
        for day, rate in fetch(yahoo, start, end):
            w.writerow([day, currency, f"{rate:.4f}"])


def main():
    if len(sys.argv) < 2 or sys.argv[1] not in ("prices", "fx"):
        sys.exit(__doc__)
    if sys.argv[1] == "prices":
        cmd_prices(sys.argv[2:])
    else:
        cmd_fx(sys.argv[2:])


if __name__ == "__main__":
    main()
