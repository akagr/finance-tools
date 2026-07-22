package main

import (
	"flag"
	"testing"
)

func TestStrategyFlagsConfigMapping(t *testing.T) {
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	sf := registerStrategyFlags(fs)
	if err := fs.Parse([]string{
		"--strategy", "ema-cross", "--fast", "12", "--slow", "48",
		"--vol-target", "15", "--vol-lookback", "30",
		"--stt-bps", "8", "--slippage-bps", "3",
	}); err != nil {
		t.Fatal(err)
	}
	cfg := sf.config()
	if cfg.Name != "ema-cross" || cfg.Fast != 12 || cfg.Slow != 48 {
		t.Errorf("config mapping wrong: %+v", cfg)
	}
	if cfg.VolTarget != 0.15 { // percent flag → fraction
		t.Errorf("vol target = %v, want 0.15", cfg.VolTarget)
	}
	if cfg.VolLookback != 30 {
		t.Errorf("vol lookback = %v, want 30", cfg.VolLookback)
	}
	costs := sf.costs()
	if costs.STTBps != 8 || costs.SlippageBps != 3 {
		t.Errorf("costs mapping wrong: %+v", costs)
	}
}

func TestOrNone(t *testing.T) {
	if orNone("") != "(none yet)" {
		t.Errorf("orNone(empty) = %q", orNone(""))
	}
	if orNone("2024-01-01") != "2024-01-01" {
		t.Errorf("orNone(date) = %q", orNone("2024-01-01"))
	}
}
