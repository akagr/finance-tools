// Package money represents monetary amounts and quantities as exact integers
// scaled by a fixed factor (1e8), so that every figure is exact, orderable, and
// still summable by plain SQL in the database — a float64 column would silently
// break exactness, and a text rational could not be SUM()ed by ad-hoc queries.
//
// int64 at this scale spans about ±9.2e10 units, which is ample headroom for a
// net worth in rupees while leaving 8 decimal places for fund units.
package money

import (
	"errors"
	"fmt"
	"math/big"
	"strings"
)

// Scale is the fixed multiplier applied to every stored amount. It is recorded
// in app_meta so a database is interpretable without this code.
const Scale = 100_000_000

// Places is the number of decimal digits Scale preserves.
const Places = 8

var scaleRat = new(big.Rat).SetInt64(Scale)

// Amount is a value scaled by Scale. Zero value is a zero amount.
type Amount int64

// FromRat converts an exact rational to the nearest Amount (half away from zero).
func FromRat(r *big.Rat) Amount {
	scaled := new(big.Rat).Mul(r, scaleRat)
	num, den := scaled.Num(), scaled.Denom()
	q, rem := new(big.Int).QuoRem(num, den, new(big.Int))
	// Round half away from zero.
	rem.Abs(rem)
	rem.Lsh(rem, 1)
	if rem.Cmp(den) >= 0 {
		if scaled.Sign() < 0 {
			q.Sub(q, big.NewInt(1))
		} else {
			q.Add(q, big.NewInt(1))
		}
	}
	return Amount(q.Int64())
}

// Rat returns the exact rational value of a.
func (a Amount) Rat() *big.Rat { return new(big.Rat).SetFrac64(int64(a), Scale) }

// Float returns a as a float64. Use only for ratios and display, never to
// round-trip a stored amount.
func (a Amount) Float() float64 { return float64(a) / float64(Scale) }

// FromFloat converts a float64 to an Amount. Use only for values that were
// never exact to begin with (market prices from an API, computed ratios).
func FromFloat(f float64) Amount {
	r := new(big.Rat).SetFloat64(f)
	if r == nil {
		return 0
	}
	return FromRat(r)
}

// FromMajor returns an Amount for a whole number of currency units.
func FromMajor(units int64) Amount { return Amount(units * Scale) }

// Sign reports the sign of a: -1, 0 or +1.
func (a Amount) Sign() int {
	switch {
	case a < 0:
		return -1
	case a > 0:
		return 1
	}
	return 0
}

// Abs returns the absolute value of a.
func (a Amount) Abs() Amount {
	if a < 0 {
		return -a
	}
	return a
}

// ErrEmpty is returned by Parse when the input has no digits.
var ErrEmpty = errors.New("no amount given")

// Parse reads a human-typed amount. It is deliberately forgiving: currency
// symbols, spaces, underscores and thousands separators (both 1,234,567 and the
// Indian 12,34,567) are ignored, a leading or trailing minus negates, and
// parenthesised values are treated as negative, as in accounting.
//
// It also accepts a whole arithmetic expression ("450000 + 62000", "1200*12"),
// evaluated exactly, so that the freedom of typing a formula into a spreadsheet
// cell is not lost. Percentages are supported as a suffix ("5%" is 0.05).
func Parse(s string) (Amount, error) {
	r, err := ParseRat(s)
	if err != nil {
		return 0, err
	}
	return FromRat(r), nil
}

// ParseRat is Parse without the final rounding to Amount.
func ParseRat(s string) (*big.Rat, error) {
	t := strings.TrimSpace(s)
	if t == "" {
		return nil, ErrEmpty
	}
	// Accounting negatives: (1,234) means -1234.
	neg := false
	if strings.HasPrefix(t, "(") && strings.HasSuffix(t, ")") {
		neg = true
		t = t[1 : len(t)-1]
	}
	p := &parser{src: []rune(clean(t))}
	v, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	p.skipSpace()
	if p.pos < len(p.src) {
		return nil, fmt.Errorf("unexpected %q in amount", string(p.src[p.pos:]))
	}
	if neg {
		v.Neg(v)
	}
	return v, nil
}

// clean strips currency symbols, digit-grouping commas and cosmetic separators,
// leaving only characters the expression parser understands.
func clean(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case ',', '_', '\u00a0', '₹', '$', '€', '£', '¥':
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// parser is a recursive-descent evaluator over exact rationals, supporting
// + - * / , parentheses and a % suffix.
type parser struct {
	src []rune
	pos int
}

func (p *parser) skipSpace() {
	for p.pos < len(p.src) && (p.src[p.pos] == ' ' || p.src[p.pos] == '\t') {
		p.pos++
	}
}

func (p *parser) peek() rune {
	p.skipSpace()
	if p.pos >= len(p.src) {
		return 0
	}
	return p.src[p.pos]
}

func (p *parser) parseExpr() (*big.Rat, error) {
	left, err := p.parseTerm()
	if err != nil {
		return nil, err
	}
	for {
		switch p.peek() {
		case '+':
			p.pos++
			right, err := p.parseTerm()
			if err != nil {
				return nil, err
			}
			left = new(big.Rat).Add(left, right)
		case '-':
			p.pos++
			right, err := p.parseTerm()
			if err != nil {
				return nil, err
			}
			left = new(big.Rat).Sub(left, right)
		default:
			return left, nil
		}
	}
}

func (p *parser) parseTerm() (*big.Rat, error) {
	left, err := p.parseFactor()
	if err != nil {
		return nil, err
	}
	for {
		switch p.peek() {
		case '*':
			p.pos++
			right, err := p.parseFactor()
			if err != nil {
				return nil, err
			}
			left = new(big.Rat).Mul(left, right)
		case '/':
			p.pos++
			right, err := p.parseFactor()
			if err != nil {
				return nil, err
			}
			if right.Sign() == 0 {
				return nil, errors.New("division by zero")
			}
			left = new(big.Rat).Quo(left, right)
		default:
			return left, nil
		}
	}
}

func (p *parser) parseFactor() (*big.Rat, error) {
	switch p.peek() {
	case '-':
		p.pos++
		v, err := p.parseFactor()
		if err != nil {
			return nil, err
		}
		return new(big.Rat).Neg(v), nil
	case '+':
		p.pos++
		return p.parseFactor()
	case '(':
		p.pos++
		v, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		if p.peek() != ')' {
			return nil, errors.New("missing )")
		}
		p.pos++
		return p.applyPercent(v), nil
	}
	start := p.pos
	for p.pos < len(p.src) && (p.src[p.pos] >= '0' && p.src[p.pos] <= '9' || p.src[p.pos] == '.') {
		p.pos++
	}
	if p.pos == start {
		if p.pos >= len(p.src) {
			return nil, ErrEmpty
		}
		return nil, fmt.Errorf("unexpected %q in amount", string(p.src[p.pos]))
	}
	v, ok := new(big.Rat).SetString(string(p.src[start:p.pos]))
	if !ok {
		return nil, fmt.Errorf("bad number %q", string(p.src[start:p.pos]))
	}
	return p.applyPercent(v), nil
}

func (p *parser) applyPercent(v *big.Rat) *big.Rat {
	if p.peek() == '%' {
		p.pos++
		return new(big.Rat).Quo(v, new(big.Rat).SetInt64(100))
	}
	return v
}
