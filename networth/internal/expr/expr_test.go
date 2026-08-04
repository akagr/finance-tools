package expr

import (
	"math/big"
	"strings"
	"testing"
)

// fakeBalances stands in for the database while testing the language itself.
type fakeBalances map[string]int64

func (f fakeBalances) Balance(sel string) (*big.Rat, bool) {
	v, ok := f[sel]
	if !ok {
		return nil, false
	}
	return new(big.Rat).SetInt64(v), true
}

func env() *BaseEnv {
	return &BaseEnv{
		Vars: map[string]*big.Rat{
			"income":     big.NewRat(200000, 1),
			"investable": big.NewRat(120000, 1),
			"remaining":  big.NewRat(70000, 1),
			"allocated":  big.NewRat(50000, 1),
		},
		Accounts: fakeBalances{
			"Spending":             12000,
			"HDFC Card":            -35000,
			"Amex":                 -8000,
			"tag:type=credit_card": -43000,
			"kind:liability":       -4300000,
		},
		Lines: func(label string) (*big.Rat, bool) {
			if label == "Domestic equity" {
				return big.NewRat(28000, 1), true
			}
			return nil, false
		},
	}
}

func eval(t *testing.T, src string) *big.Rat {
	t.Helper()
	v, err := Eval(src, env())
	if err != nil {
		t.Fatalf("Eval(%q): %v", src, err)
	}
	return v
}

func wantEq(t *testing.T, src string, want int64) {
	t.Helper()
	got := eval(t, src)
	if got.Cmp(new(big.Rat).SetInt64(want)) != 0 {
		t.Errorf("%s = %s, want %d", src, got.FloatString(2), want)
	}
}

func TestArithmetic(t *testing.T) {
	wantEq(t, "100000", 100000)
	wantEq(t, "1,00,000", 100000)
	wantEq(t, "₹1,00,000", 100000)
	wantEq(t, "50000 + 50000", 100000)
	wantEq(t, "(100000 + 20000) / 2", 60000)
	wantEq(t, "-5000 + 15000", 10000)
	wantEq(t, "12 * 5000", 60000)
}

func TestPercent(t *testing.T) {
	wantEq(t, "40% * investable", 48000)
	wantEq(t, "investable * 40%", 48000)
	wantEq(t, "10% * 100000", 10000)
}

func TestVariables(t *testing.T) {
	wantEq(t, "income", 200000)
	wantEq(t, "investable - allocated", 70000)
	wantEq(t, "40% * remaining", 28000)
}

// The requirement that motivated the whole language: top up a spending account
// to cover a month's spending plus the credit cards, less what is already there.
func TestSpendingTopUpFormula(t *testing.T) {
	// 1,00,000 needed + 43,000 owed on cards - 12,000 already in the account.
	wantEq(t, `100000 + owed("tag:type=credit_card") - balance("Spending")`, 131000)
}

func TestOwedIsPositiveAndBalanceIsSigned(t *testing.T) {
	wantEq(t, `balance("HDFC Card")`, -35000)
	wantEq(t, `owed("HDFC Card")`, 35000)
	wantEq(t, `owed("HDFC Card", "Amex")`, 43000)
}

func TestLineReferencesAnotherRule(t *testing.T) {
	wantEq(t, `line("Domestic equity") / 2`, 14000)
}

func TestMathFunctions(t *testing.T) {
	wantEq(t, "min(50000, 80000)", 50000)
	wantEq(t, "max(50000, 80000)", 80000)
	wantEq(t, "abs(0 - 4200)", 4200)
	wantEq(t, "round(1234.6)", 1235)
	wantEq(t, "floor(1234.6)", 1234)
	wantEq(t, "ceil(1234.2)", 1235)
	wantEq(t, "clamp(150000, 0, 100000)", 100000)
	wantEq(t, "clamp(-500, 0, 100000)", 0)
	// A top-up should never be negative just because the account is already full.
	wantEq(t, `max(0, 5000 - balance("Spending"))`, 0)
}

// A typo in an account name must fail loudly. Treating it as zero would produce
// a plausible-looking number and move the wrong amount of money.
func TestUnknownAccountIsAnError(t *testing.T) {
	_, err := Eval(`balance("Spendng")`, env())
	if err == nil {
		t.Fatal("a misspelt account name should be an error, not zero")
	}
	if !strings.Contains(err.Error(), "Spendng") {
		t.Errorf("the error should name the account it could not find: %v", err)
	}
}

func TestUnknownNameIsAnError(t *testing.T) {
	for _, src := range []string{"salary", "unknownfn(1)", "balance()"} {
		if _, err := Eval(src, env()); err == nil {
			t.Errorf("Eval(%q) should have failed", src)
		}
	}
}

func TestFunctionWrittenWithoutBracketsIsExplained(t *testing.T) {
	_, err := Eval("balance", env())
	if err == nil || !strings.Contains(err.Error(), "balance(") {
		t.Errorf("error should suggest the bracket form, got %v", err)
	}
}

func TestSyntaxErrors(t *testing.T) {
	for _, src := range []string{"", "1 +", "(1 + 2", "1 / 0", "* 5"} {
		if _, err := Eval(src, env()); err == nil {
			t.Errorf("Eval(%q) should have failed", src)
		}
	}
}

// Parsing must be exact: a third of a lakh has no float representation, and the
// error would compound across a year of plans.
func TestArithmeticIsExact(t *testing.T) {
	v := eval(t, "100000 / 3")
	if v.RatString() != "100000/3" {
		t.Errorf("100000/3 = %s, want an exact rational", v.RatString())
	}
}

func TestNamesReportsDependencies(t *testing.T) {
	n, err := Parse(`100000 + owed("tag:type=credit_card") - balance("Spending") + remaining`)
	if err != nil {
		t.Fatal(err)
	}
	vars, calls := Names(n)
	if len(vars) != 1 || vars[0] != "remaining" {
		t.Errorf("vars = %v, want [remaining]", vars)
	}
	if len(calls) != 2 {
		t.Errorf("calls = %v, want owed and balance", calls)
	}
}

func TestParseIsReusable(t *testing.T) {
	n, err := Parse("40% * remaining")
	if err != nil {
		t.Fatal(err)
	}
	for range 3 {
		v, err := Evaluate(n, env())
		if err != nil {
			t.Fatal(err)
		}
		if v.Cmp(big.NewRat(28000, 1)) != 0 {
			t.Errorf("got %s, want 28000", v.FloatString(2))
		}
	}
}
