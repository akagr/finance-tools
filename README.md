# finance-tools

[![CI](https://github.com/akagr/finance-tools/actions/workflows/ci.yml/badge.svg)](https://github.com/akagr/finance-tools/actions/workflows/ci.yml)

Personal-finance tooling for Indian investors, built from broker data. Currently one tool:

| Tool            | Directory                      | Status           | What it does                                                                                                                      |
|-----------------|--------------------------------|------------------|-----------------------------------------------------------------------------------------------------------------------------------|
| **itr-foreign** | [`itr-foreign/`](itr-foreign/) | complete (M0–M8) | Generates ready-to-use **Schedule FA** (Foreign Assets, calendar year) plus **Schedule FSI/TR** (foreign income and tax relief, financial year, Rule 115) and a Form 67 worksheet for the Indian ITR, from **Interactive Brokers (IBKR)** holdings. |

It is an isolated Go module (stdlib only) under its own directory, with a root `go.work`
workspace so future tools can be added as sibling directories.

> The badge and module path assume the repo slug `github.com/akagr/finance-tools`; adjust if
> your remote differs (also in `itr-foreign/go.mod` and `go.work`).

## Layout

```
finance-tools/
  go.work            # workspace
  itr-foreign/       # the tool: its own go.mod, cmd/, internal/, docs/, data/
  …                  # future tools as sibling directories
```

## Building

Requires Go 1.26. Run `go` commands from inside the module directory:

```sh
cd itr-foreign
go build ./cmd/itrforeign
go test ./...
```

See [`itr-foreign/README.md`](itr-foreign/README.md) for usage, the IBKR Flex Query setup and
the data files the tool needs.

> **Disclaimer:** Nothing here is tax advice. Output is a working draft to be verified by
> the taxpayer or a qualified professional before filing.

## License

[MIT](LICENSE) © Akash Agrawal
