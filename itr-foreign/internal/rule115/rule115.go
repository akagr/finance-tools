// Package rule115 implements the exchange-rate rules that govern TAXABLE INCOME
// — Rule 115 of the Income-tax Rules, 1962 for the income itself and Rule 128(8)
// for the foreign tax credited.
//
// This is deliberately NOT the rule Schedule FA uses. Schedule FA discloses
// asset values at the SBI TTBR of the relevant event date; Rule 115 computes
// income at the SBI TTBR of the "specified date", which is the last day of the
// month IMMEDIATELY PRECEDING the month of the event. The two therefore produce
// different rupee figures for the same dollar, and both are correct in their own
// schedule. Anything that tries to reconcile them figure-for-figure is wrong.
//
// Specified dates (Rule 115(1), Explanation 2):
//
//	Capital gains          last day of the month before the month of TRANSFER
//	Dividend               last day of the month before the month of PAYMENT
//	Interest on securities last day of the month before the month it fell DUE
//	Other income           31 March of the previous year (the financial year end)
//	Salary                 last day of the month before the month it became due
//
// Rule 128(8) converts foreign tax at the TTBR of the last day of the month
// immediately preceding the month in which the tax was PAID OR DEDUCTED — the
// same month-end-before rule, applied to a different date.
//
// The lookup itself reuses fx.Store, whose preceding-working-day fallback is
// exactly what a month-end that falls on a weekend or holiday needs.
package rule115

import (
	"time"

	"github.com/akagr/finance-tools/itr-foreign/internal/fx"
	"github.com/akagr/finance-tools/itr-foreign/internal/model"
)

// Head is a head of income, which decides the specified date.
type Head string

const (
	CapitalGains Head = "Capital Gains"
	Dividend     Head = "Dividend"
	IntOnSecs    Head = "Interest on securities"
	OtherIncome  Head = "Other income"
	Salary       Head = "Salary"
	ForeignTax   Head = "Foreign tax" // Rule 128(8)
)

// MonthEndBefore returns the last day of the month immediately preceding the
// month of d — the "specified date" for most heads, and for Rule 128(8).
func MonthEndBefore(d time.Time) time.Time {
	if d.IsZero() {
		return time.Time{}
	}
	firstOfMonth := time.Date(d.Year(), d.Month(), 1, 0, 0, 0, 0, time.UTC)
	return firstOfMonth.AddDate(0, 0, -1)
}

// FYEnd returns 31 March of the financial year beginning in fyStart (e.g.
// FYEnd(2025) is 2026-03-31): the specified date for "other income".
func FYEnd(fyStart int) time.Time {
	return time.Date(fyStart+1, time.March, 31, 0, 0, 0, 0, time.UTC)
}

// SpecifiedDate returns the Rule 115 specified date for an event under a head.
// fyStart is the financial year's starting calendar year, needed only for the
// heads that are pinned to the year end rather than to the event.
func SpecifiedDate(h Head, event time.Time, fyStart int) time.Time {
	switch h {
	case OtherIncome:
		return FYEnd(fyStart)
	default:
		return MonthEndBefore(event)
	}
}

// Convert applies the TTBR of the specified date for `head` to `amount` and
// returns the INR result together with its audit record.
func Convert(store fx.Store, amount model.Money, head Head, event time.Time, fyStart int) (fx.Conversion, error) {
	return fx.Convert(store, amount, SpecifiedDate(head, event, fyStart))
}
