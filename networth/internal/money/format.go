package money

import (
	"math/big"
	"strconv"
	"strings"
)

// Grouping selects how digits are separated for display.
type Grouping string

const (
	// GroupIndian is the lakh/crore convention: 1,23,45,678.
	GroupIndian Grouping = "indian"
	// GroupWestern is the thousands convention: 12,345,678.
	GroupWestern Grouping = "western"
	// GroupNone writes digits unseparated, which is what machines want.
	GroupNone Grouping = "none"
)

// Format renders a with the given number of decimal places and digit grouping.
func (a Amount) Format(places int, g Grouping) string {
	neg := a < 0
	r := a.Rat()
	if neg {
		r = new(big.Rat).Neg(r)
	}
	s := r.FloatString(places)
	intPart, frac, _ := strings.Cut(s, ".")
	out := group(intPart, g)
	if frac != "" {
		out += "." + frac
	}
	if neg {
		out = "-" + out
	}
	return out
}

// String renders a with two decimal places and no grouping, for logs and errors.
func (a Amount) String() string { return a.Format(2, GroupNone) }

func group(digits string, g Grouping) string {
	switch g {
	case GroupNone:
		return digits
	case GroupIndian:
		if len(digits) <= 3 {
			return digits
		}
		head, tail := digits[:len(digits)-3], digits[len(digits)-3:]
		var parts []string
		for len(head) > 2 {
			parts = append([]string{head[len(head)-2:]}, parts...)
			head = head[:len(head)-2]
		}
		if head != "" {
			parts = append([]string{head}, parts...)
		}
		return strings.Join(parts, ",") + "," + tail
	default:
		var parts []string
		for len(digits) > 3 {
			parts = append([]string{digits[len(digits)-3:]}, parts...)
			digits = digits[:len(digits)-3]
		}
		if digits != "" {
			parts = append([]string{digits}, parts...)
		}
		return strings.Join(parts, ",")
	}
}

// Compact renders a in the largest natural unit for its size, for headline
// figures where the exact paise are noise: 1.25 Cr, 4.5 L, 62.0 K. Indian
// grouping uses lakh and crore; western uses M and K.
func (a Amount) Compact(g Grouping) string {
	f := a.Float()
	sign := ""
	if f < 0 {
		sign, f = "-", -f
	}
	unit := func(div float64, suffix string) string {
		v := f / div
		prec := 2
		if v >= 100 {
			prec = 0
		} else if v >= 10 {
			prec = 1
		}
		return sign + strconv.FormatFloat(v, 'f', prec, 64) + suffix
	}
	if g == GroupIndian {
		switch {
		case f >= 1e7:
			return unit(1e7, " Cr")
		case f >= 1e5:
			return unit(1e5, " L")
		case f >= 1e3:
			return unit(1e3, " K")
		}
	} else {
		switch {
		case f >= 1e9:
			return unit(1e9, "B")
		case f >= 1e6:
			return unit(1e6, "M")
		case f >= 1e3:
			return unit(1e3, "K")
		}
	}
	return sign + strconv.FormatFloat(f, 'f', 0, 64)
}
