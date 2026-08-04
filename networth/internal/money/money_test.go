package money

import "testing"

func TestParse(t *testing.T) {
	cases := []struct {
		in   string
		want Amount
	}{
		{"100", FromMajor(100)},
		{"1,00,000", FromMajor(100000)},
		{"1,000,000", FromMajor(1000000)},
		{"₹4,52,000.50", FromRat(mustRat(t, "452000.5"))},
		{"-250", FromMajor(-250)},
		{"(250)", FromMajor(-250)},
		{" 12 345 ", 0}, // space-separated digits are not a number
		{"450000+62000", FromMajor(512000)},
		{"1200*12", FromMajor(14400)},
		{"(1000+500)/2", FromMajor(750)},
		{"10%", FromRat(mustRat(t, "0.1"))},
		{"100000*8%", FromMajor(8000)},
		{"0.00000001", 1},
	}
	for _, c := range cases {
		got, err := Parse(c.in)
		if c.in == " 12 345 " {
			if err == nil {
				t.Errorf("Parse(%q) = %v, want error", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("Parse(%q) error: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("Parse(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestParseErrors(t *testing.T) {
	for _, in := range []string{"", "   ", "abc", "1/0", "(1+2", "1 2"} {
		if got, err := Parse(in); err == nil {
			t.Errorf("Parse(%q) = %v, want error", in, got)
		}
	}
}

func TestFormatIndianGrouping(t *testing.T) {
	cases := []struct {
		amt  Amount
		want string
	}{
		{FromMajor(0), "0.00"},
		{FromMajor(100), "100.00"},
		{FromMajor(1000), "1,000.00"},
		{FromMajor(100000), "1,00,000.00"},
		{FromMajor(12452000), "1,24,52,000.00"},
		{FromMajor(-452000), "-4,52,000.00"},
	}
	for _, c := range cases {
		if got := c.amt.Format(2, GroupIndian); got != c.want {
			t.Errorf("Format(%d) = %q, want %q", c.amt, got, c.want)
		}
	}
}

func TestFormatWesternGrouping(t *testing.T) {
	if got := FromMajor(12452000).Format(2, GroupWestern); got != "12,452,000.00" {
		t.Errorf("western grouping = %q", got)
	}
}

func TestCompact(t *testing.T) {
	cases := []struct {
		amt  Amount
		g    Grouping
		want string
	}{
		{FromMajor(12452000), GroupIndian, "1.25 Cr"},
		{FromMajor(452000), GroupIndian, "4.52 L"},
		{FromMajor(62000), GroupIndian, "62.0 K"},
		{FromMajor(500), GroupIndian, "500"},
		{FromMajor(-12452000), GroupIndian, "-1.25 Cr"},
		{FromMajor(12452000), GroupWestern, "12.5M"},
	}
	for _, c := range cases {
		if got := c.amt.Compact(c.g); got != c.want {
			t.Errorf("Compact(%d) = %q, want %q", c.amt, got, c.want)
		}
	}
}

func TestRatRoundTripIsExact(t *testing.T) {
	r := mustRat(t, "123456.78901234")
	if got := FromRat(r).Rat().RatString(); got != r.RatString() {
		t.Errorf("round trip = %s, want %s", got, r.RatString())
	}
}

func TestFromRatRoundsHalfAwayFromZero(t *testing.T) {
	// One half of the smallest unit, in both directions.
	if got := FromRat(mustRat(t, "0.000000005")); got != 1 {
		t.Errorf("positive half = %d, want 1", got)
	}
	if got := FromRat(mustRat(t, "-0.000000005")); got != -1 {
		t.Errorf("negative half = %d, want -1", got)
	}
}
