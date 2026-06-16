# tax-tools

[![CI](https://github.com/akagr/tax-tools/actions/workflows/ci.yml/badge.svg)](https://github.com/akagr/tax-tools/actions/workflows/ci.yml)

A monorepo of small, focused tools for Indian tax filing from broker/financial data.
Each tool is an isolated Go module under its own directory, tied together by a root
`go.work` workspace.

> The badge and module paths assume the repo slug `github.com/akagr/tax-tools`; adjust if
> your remote differs (also in `schedule-fa/go.mod` and `go.work`).

## Tools

| Tool            | Directory                      | Status           | What it does                                                                                                                      |
|-----------------|--------------------------------|------------------|-----------------------------------------------------------------------------------------------------------------------------------|
| **schedule-fa** | [`schedule-fa/`](schedule-fa/) | complete (M0–M7) | Generates a ready-to-use **Schedule FA** (Foreign Assets) report for the Indian ITR from **Interactive Brokers (IBKR)** holdings. |

## Layout

```
tax-tools/
  go.work            # ties all tool modules together
  schedule-fa/       # each tool: its own go.mod, cmd/, internal/, docs/, data/
  …                  # future tools as sibling directories
```

## Building

Requires Go (not currently installed on this machine — `brew install go`).

```sh
go build ./...        # from repo root, builds every module in the workspace
```

> **Disclaimer:** Nothing here is tax advice. Output is a working draft to be verified by
> the taxpayer or a qualified professional before filing.

## License

[MIT](LICENSE) © Akash Agrawal
</content>
