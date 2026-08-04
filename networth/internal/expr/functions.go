package expr

import (
	"fmt"
	"math/big"
	"strings"
)

// Balances is what an expression can ask about the world: the balance of an
// account, or of a set of them.
//
// A selector is one of:
//
//	"ICICI Savings"            an exact account name
//	"tag:type=credit_card"     every account carrying that tag
//	"kind:liability"           every account of that kind
//	"*"                        everything inside net worth
//
// Selecting by tag rather than by name is what keeps a formula working after a
// new credit card is added: tag it and the rule already covers it.
type Balances interface {
	// Balance returns the signed total for a selector, and whether anything
	// matched. A liability's balance is negative, as it is everywhere else here.
	Balance(selector string) (*big.Rat, bool)
}

// Suggester is an optional extra a Balances implementation can provide so that a
// selector matching nothing can say what would have worked. A bare "no match" is
// a dead end when the vocabulary is invisible: the whole difficulty is knowing
// that a tag has to exist on an account before tag:… can select it.
type Suggester interface {
	Selectors() []string
}

// BaseEnv implements Env with a set of variables and the standard functions.
type BaseEnv struct {
	Vars     map[string]*big.Rat
	Accounts Balances
	// Lines resolves line("Some rule") to an amount already computed in this
	// plan, so one rule can be expressed in terms of another.
	Lines func(label string) (*big.Rat, bool)
}

// Var implements Env.
func (e *BaseEnv) Var(name string) (*big.Rat, bool) {
	v, ok := e.Vars[strings.ToLower(name)]
	return v, ok
}

// Functions lists the callable names, for help text and completion.
var Functions = []struct {
	Name, Args, Help string
}{
	{"balance", `"selector", …`, "signed balance of the matching accounts (a debt is negative)"},
	{"owed", `"selector", …`, "what is owed on the matching accounts, as a positive number"},
	{"line", `"rule name"`, "the amount another rule in this plan came to"},
	{"min", "a, b, …", "the smallest of its arguments"},
	{"max", "a, b, …", "the largest of its arguments"},
	{"abs", "x", "drops the sign"},
	{"round", "x", "to the nearest whole unit"},
	{"floor", "x", "down to a whole unit"},
	{"ceil", "x", "up to a whole unit"},
	{"clamp", "x, lo, hi", "keeps x between lo and hi"},
}

// Variables lists the bare names available, for the same reason.
var Variables = []struct{ Name, Help string }{
	{"income", "the income you entered for this month"},
	{"setaside", "everything set aside for spending so far"},
	{"investable", "income less the set-asides"},
	{"allocated", "how much of the investable amount is already claimed"},
	{"remaining", "investable less allocated — what is still unspoken for"},
}

// Call implements Env.
func (e *BaseEnv) Call(name string, args []Value) (*big.Rat, error) {
	switch name {
	case "balance", "owed":
		if len(args) == 0 {
			return nil, fmt.Errorf("%s() needs at least one account or selector", name)
		}
		if e.Accounts == nil {
			return nil, fmt.Errorf("account balances are not available here")
		}
		total := new(big.Rat)
		var missing []string
		for _, a := range args {
			if !a.IsStr {
				return nil, fmt.Errorf("%s() takes account names in quotes", name)
			}
			v, ok := e.Accounts.Balance(a.Str)
			if !ok {
				missing = append(missing, a.Str)
				continue
			}
			total.Add(total, v)
		}
		if len(missing) > 0 {
			// Silently treating an unknown account as zero would produce a
			// plausible number from a typo, and move the wrong amount of money.
			return nil, fmt.Errorf("no account matches %s%s",
				strings.Join(quoteAll(missing), " or "), e.suggest(missing[0]))
		}
		if name == "owed" {
			return total.Neg(total), nil
		}
		return total, nil

	case "line":
		if len(args) != 1 || !args[0].IsStr {
			return nil, fmt.Errorf(`line() takes one rule name in quotes`)
		}
		if e.Lines == nil {
			return nil, fmt.Errorf("other rules are not available here")
		}
		v, ok := e.Lines(args[0].Str)
		if !ok {
			return nil, fmt.Errorf("no earlier rule named %q; only rules with a lower "+
				"priority number are available", args[0].Str)
		}
		return v, nil

	case "min", "max":
		if len(args) == 0 {
			return nil, fmt.Errorf("%s() needs at least one number", name)
		}
		best, err := args[0].Number()
		if err != nil {
			return nil, err
		}
		for _, a := range args[1:] {
			v, err := a.Number()
			if err != nil {
				return nil, err
			}
			if (name == "min") == (v.Cmp(best) < 0) {
				best = v
			}
		}
		return new(big.Rat).Set(best), nil

	case "abs":
		v, err := one(name, args)
		if err != nil {
			return nil, err
		}
		return new(big.Rat).Abs(v), nil

	case "round", "floor", "ceil":
		v, err := one(name, args)
		if err != nil {
			return nil, err
		}
		return roundRat(v, name), nil

	case "clamp":
		if len(args) != 3 {
			return nil, fmt.Errorf("clamp() takes a value, a minimum and a maximum")
		}
		v, err := args[0].Number()
		if err != nil {
			return nil, err
		}
		lo, err := args[1].Number()
		if err != nil {
			return nil, err
		}
		hi, err := args[2].Number()
		if err != nil {
			return nil, err
		}
		if v.Cmp(lo) < 0 {
			return new(big.Rat).Set(lo), nil
		}
		if v.Cmp(hi) > 0 {
			return new(big.Rat).Set(hi), nil
		}
		return new(big.Rat).Set(v), nil
	}
	return nil, fmt.Errorf("unknown function %q", name)
}

func one(name string, args []Value) (*big.Rat, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("%s() takes one number", name)
	}
	return args[0].Number()
}

func quoteAll(ss []string) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = fmt.Sprintf("%q", s)
	}
	return out
}

func roundRat(v *big.Rat, mode string) *big.Rat {
	num, den := v.Num(), v.Denom()
	q, rem := new(big.Int).QuoRem(num, den, new(big.Int))
	if rem.Sign() == 0 {
		return new(big.Rat).SetInt(q)
	}
	switch mode {
	case "floor":
		if v.Sign() < 0 {
			q.Sub(q, oneInt)
		}
	case "ceil":
		if v.Sign() > 0 {
			q.Add(q, oneInt)
		}
	default: // round half away from zero
		twice := new(big.Int).Abs(rem)
		twice.Lsh(twice, 1)
		if twice.Cmp(den) >= 0 {
			if v.Sign() < 0 {
				q.Sub(q, oneInt)
			} else {
				q.Add(q, oneInt)
			}
		}
	}
	return new(big.Rat).SetInt(q)
}

var oneInt = big.NewInt(1)

// suggest builds the "; try …" half of a failed-selector message, listing the
// selectors of the same shape as the one that missed.
func (e *BaseEnv) suggest(missed string) string {
	s, ok := e.Accounts.(Suggester)
	if !ok {
		return ""
	}
	all := s.Selectors()
	wantTag := strings.HasPrefix(strings.ToLower(missed), "tag:")
	var same []string
	for _, sel := range all {
		if strings.HasPrefix(sel, "tag:") == wantTag {
			same = append(same, sel)
		}
	}
	if len(same) == 0 {
		if wantTag {
			return "; no account carries any tag yet — add one on the Accounts screen, " +
				"for example type=credit_card"
		}
		return ""
	}
	if len(same) > 6 {
		same = append(same[:6], "…")
	}
	return "; try " + strings.Join(same, ", ")
}
