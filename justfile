# tax-tools — common commands. Run `just` (or `just --list`) to see them.
# Requires Go (the schedule-fa module) and, for `prices`, python3.

mod := "schedule-fa"

# list available recipes
default:
    @just --list

# run the full test suite (race detector)
test:
    cd {{mod}} && go test -race ./...

# run tests for one package, e.g. `just test-one ./internal/peak`
test-one pkg:
    cd {{mod}} && go test -race {{pkg}}

# build the CLI into schedule-fa/schedulefa (gitignored)
build:
    cd {{mod}} && go build -o schedulefa ./cmd/schedulefa

# format all Go code in place
fmt:
    cd {{mod}} && gofmt -w .

# CI gate: gofmt check + vet + build + race tests
check:
    cd {{mod}} && unformatted="$(gofmt -l .)"; if [ -n "$unformatted" ]; then echo "needs gofmt:"; echo "$unformatted"; exit 1; fi
    cd {{mod}} && go vet ./...
    cd {{mod}} && go build ./...
    cd {{mod}} && go test -race ./...

# regenerate the offline golden fixtures (after an intended output change)
golden:
    cd {{mod}} && go test ./internal/pipeline -update

# download SBI TTBR (USD) rate data into data/ttbr/
rates:
    cd {{mod}} && curl -L -o data/ttbr/SBI_REFERENCE_RATES_USD.csv \
      https://raw.githubusercontent.com/sahilgupta/sbi-fx-ratekeeper/main/csv_files/SBI_REFERENCE_RATES_USD.csv

# fetch daily prices for a calendar year (tickers in scripts/tickers.txt)
prices year:
    cd {{mod}} && scripts/fetch-prices.py {{year}}

# run the generator; pass flags through, e.g.
#   just generate --year 2026 --statement private/flex-2026.xml --rates data/ttbr/SBI_REFERENCE_RATES_USD.csv
generate *args:
    cd {{mod}} && go run ./cmd/schedulefa generate {{args}}

# remove build artifacts
clean:
    rm -f {{mod}}/schedulefa
