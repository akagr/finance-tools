// Package account holds a paper-trading account's persistent configuration and
// state, and knows how to turn its stored strategy config into a runnable
// strategy from the shared strategy package.
package account

import (
	"fmt"
	"time"

	"github.com/akagr/finance-tools/papertrade/internal/broker"
	"github.com/akagr/finance-tools/papertrade/internal/portfolio"
	"github.com/akagr/finance-tools/papertrade/internal/strategy"
)

// StrategyConfig is the serialisable description of which rule the account
// trades and with what parameters. Only the fields relevant to Name are used.
type StrategyConfig struct {
	Name          string  `json:"name"`
	Fast          int     `json:"fast,omitempty"`
	Slow          int     `json:"slow,omitempty"`
	Lookback      int     `json:"lookback,omitempty"`
	RSIPeriod     int     `json:"rsi_period,omitempty"`
	RSIThreshold  float64 `json:"rsi_threshold,omitempty"`
	DonchianEntry int     `json:"entry,omitempty"`
	DonchianExit  int     `json:"exit,omitempty"`
	VolTarget     float64 `json:"vol_target,omitempty"` // annualised fraction, 0 = off
	VolLookback   int     `json:"vol_lookback,omitempty"`
}

// Account is the full persisted record: config plus live portfolio state.
type Account struct {
	Name           string              `json:"name"`
	Symbol         string              `json:"symbol"`       // label used in the report/state
	YahooSymbol    string              `json:"yahoo_symbol"` // symbol passed to the data feed
	Strategy       StrategyConfig      `json:"strategy"`
	Costs          broker.Costs        `json:"costs"`
	InitialCapital float64             `json:"initial_capital"`
	Portfolio      portfolio.Portfolio `json:"portfolio"`
	LastBarDate    string              `json:"last_bar_date"` // YYYY-MM-DD of the most recent bar acted on
	CreatedAt      time.Time           `json:"created_at"`
	UpdatedAt      time.Time           `json:"updated_at"`
}

// BuildStrategy constructs the runnable strategy described by the account's
// config, wrapping it in a volatility-targeting overlay when configured.
func (a *Account) BuildStrategy() (strategy.Strategy, error) {
	c := a.Strategy
	base, err := buildBase(c)
	if err != nil {
		return nil, err
	}
	if c.VolTarget > 0 {
		lb := c.VolLookback
		if lb == 0 {
			lb = 20
		}
		return strategy.NewVolTarget(base, c.VolTarget, lb)
	}
	return base, nil
}

func buildBase(c StrategyConfig) (strategy.Strategy, error) {
	switch c.Name {
	case "", "sma-cross":
		return strategy.NewSMACross(orInt(c.Fast, 20), orInt(c.Slow, 50))
	case "ema-cross":
		return strategy.NewEMACross(orInt(c.Fast, 20), orInt(c.Slow, 50))
	case "momentum":
		return strategy.NewMomentum(orInt(c.Lookback, 120))
	case "rsi":
		return strategy.NewRSI(orInt(c.RSIPeriod, 14), orFloat(c.RSIThreshold, 30))
	case "donchian":
		return strategy.NewDonchian(orInt(c.DonchianEntry, 20), orInt(c.DonchianExit, 10))
	case "buy-hold":
		return strategy.BuyHold{}, nil
	default:
		return nil, fmt.Errorf("account: unknown strategy %q", c.Name)
	}
}

func orInt(v, def int) int {
	if v == 0 {
		return def
	}
	return v
}

func orFloat(v, def float64) float64 {
	if v == 0 {
		return def
	}
	return v
}
