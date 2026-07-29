# Entity metadata

Issuer details that IBKR does not provide cleanly but Schedule FA Table A3 requires
(name, address, ZIP, country code, nature of entity). Keyed by ISIN or symbol; used by
`internal/fa` (M5) with user overrides.

Expected columns (header required):

```csv
isin,symbol,entity_name,address,zip,country_code,nature
US0378331005,AAPL,Apple Inc,"One Apple Park Way, Cupertino, CA",95014,2,Listed company
```

- `country_code` — the ITR country code (e.g. United States = `2`), not the ISO code.
- `nature` — free text, e.g. `Listed company`, `ETF`.

Rows here override defaults (US-listed → United States). Unmatched securities are emitted
with blanks and a manual-review flag.
