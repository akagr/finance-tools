// Package pipeline wires the core report-building steps — peak computation and
// table assembly — into one reusable unit shared by the CLI and tests. I/O
// (loading statements, rates, prices; rendering) stays with the caller.
package pipeline

import (
	"github.com/akagr/finance-tools/itr-foreign/internal/entities"
	"github.com/akagr/finance-tools/itr-foreign/internal/fa"
	"github.com/akagr/finance-tools/itr-foreign/internal/fsi"
	"github.com/akagr/finance-tools/itr-foreign/internal/fx"
	"github.com/akagr/finance-tools/itr-foreign/internal/model"
	"github.com/akagr/finance-tools/itr-foreign/internal/peak"
)

// Result is the built report plus how the peak was computed.
type Result struct {
	Report      *fa.Report
	ExactPeak   bool // mode B (a price provider was supplied)
	A2PeakExact bool // the portfolio peak was computed exactly (all held days priced)
}

// BuildFSI is the orchestration seam for the income schedules: compute capital
// gains and other-source income from a statement parsed over the FINANCIAL year,
// then assemble Schedules FSI and TR. It is deliberately separate from
// BuildReport — Schedule FA and Schedule FSI share ingest and FX data but
// nothing else, because they run on different periods under different
// conversion rules.
func BuildFSI(st *model.Statement, store fx.Store, opts fsi.Options) (*fsi.Report, error) {
	return fsi.Build(st, store, opts)
}

// uses the exact daily engine (mode B) and a true portfolio peak; otherwise the
// approximate engine (mode C). ents may be nil.
func BuildReport(st *model.Statement, store fx.Store, prices peak.PriceProvider, ents *entities.Store) (*Result, error) {
	var peaks []peak.Result
	var a2Peak *fx.Conversion
	res := &Result{}

	if prices != nil {
		secs, port, exact, err := peak.ComputeExact(st, store, prices)
		if err != nil {
			return nil, err
		}
		peaks = secs
		res.ExactPeak = true
		res.A2PeakExact = exact
		if exact {
			a2Peak = &port
		}
	} else {
		var err error
		peaks, err = peak.Compute(st, store, peak.ModeApprox, nil)
		if err != nil {
			return nil, err
		}
	}

	rep, err := fa.Build(st, store, peaks, fa.Options{Entities: ents, A2Peak: a2Peak})
	if err != nil {
		return nil, err
	}
	res.Report = rep
	return res, nil
}
