// Package expr evaluates the small arithmetic language used by allocation rules.
//
// It exists because real routing decisions are not percentages of income. They
// refer to other accounts: "move enough into the spending account to cover a
// lakh of spending plus what is owed on the cards, less whatever is already
// sitting there". That is a formula, and the freedom to write one is the main
// thing a spreadsheet offers that a fixed set of rule types cannot.
//
// Arithmetic is exact (math/big.Rat) for the same reason the rest of this
// program is: these numbers become instructions to move real money.
package expr

import (
	"fmt"
	"math/big"
	"strings"
)

// Value is an argument to a function: either a number or a string, since
// selecting an account by name needs text.
type Value struct {
	Num *big.Rat
	Str string
	// IsStr distinguishes a genuine string from an empty one.
	IsStr bool
}

// Number returns the numeric value, or an error if this was a string.
func (v Value) Number() (*big.Rat, error) {
	if v.IsStr {
		return nil, fmt.Errorf("expected a number, got %q", v.Str)
	}
	return v.Num, nil
}

// Env resolves the names an expression can refer to. The plan package supplies
// one backed by the current balances in the database.
type Env interface {
	// Var resolves a bare name such as income or remaining.
	Var(name string) (*big.Rat, bool)
	// Call resolves a function such as balance("Bank") or min(a, b).
	Call(name string, args []Value) (*big.Rat, error)
}

// Node is a parsed expression.
type Node interface {
	eval(Env) (*big.Rat, error)
}

// Parse compiles an expression once so it can be evaluated repeatedly and, more
// importantly, so a mistake is reported while it is being typed rather than when
// money is about to move.
func Parse(src string) (Node, error) {
	p := &parser{toks: lex(src)}
	if len(p.toks) == 0 {
		return nil, fmt.Errorf("empty expression")
	}
	n, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	if p.pos < len(p.toks) {
		return nil, fmt.Errorf("unexpected %q", p.toks[p.pos].text)
	}
	return n, nil
}

// Eval parses and evaluates in one step.
func Eval(src string, env Env) (*big.Rat, error) {
	n, err := Parse(src)
	if err != nil {
		return nil, err
	}
	return n.eval(env)
}

// Evaluate runs an already-parsed expression.
func Evaluate(n Node, env Env) (*big.Rat, error) { return n.eval(env) }

// Names lists the variables and functions an expression refers to, so the
// interface can explain what a rule depends on and warn about names that do not
// resolve.
func Names(n Node) (vars, calls []string) {
	var walk func(Node)
	walk = func(node Node) {
		switch x := node.(type) {
		case *varNode:
			vars = append(vars, x.name)
		case *callNode:
			calls = append(calls, x.name)
			for _, a := range x.args {
				walk(a)
			}
		case *binNode:
			walk(x.left)
			walk(x.right)
		case *unaryNode:
			walk(x.operand)
		case *percentNode:
			walk(x.operand)
		}
	}
	walk(n)
	return vars, calls
}

type numNode struct{ v *big.Rat }
type strNode struct{ s string }
type varNode struct{ name string }
type callNode struct {
	name string
	args []Node
}
type binNode struct {
	op          byte
	left, right Node
}
type unaryNode struct {
	neg     bool
	operand Node
}
type percentNode struct{ operand Node }

func (n *numNode) eval(Env) (*big.Rat, error) { return n.v, nil }

func (n *strNode) eval(Env) (*big.Rat, error) {
	return nil, fmt.Errorf("%q is text, not a number; it can only be an argument", n.s)
}

func (n *varNode) eval(env Env) (*big.Rat, error) {
	if env == nil {
		return nil, fmt.Errorf("unknown name %q", n.name)
	}
	if v, ok := env.Var(n.name); ok {
		return v, nil
	}
	// A bare name that is not a variable is most likely a function written
	// without its brackets, which is worth saying rather than "unknown name".
	if isFunction(n.name) {
		return nil, fmt.Errorf("%s is a function; write %s(…)", n.name, n.name)
	}
	return nil, fmt.Errorf("unknown name %q", n.name)
}

// isFunction reports whether a name is one of the built-in functions.
func isFunction(name string) bool {
	for _, f := range Functions {
		if f.Name == name {
			return true
		}
	}
	return false
}

func (n *callNode) eval(env Env) (*big.Rat, error) {
	if env == nil {
		return nil, fmt.Errorf("unknown function %q", n.name)
	}
	args := make([]Value, 0, len(n.args))
	for _, a := range n.args {
		if s, ok := a.(*strNode); ok {
			args = append(args, Value{Str: s.s, IsStr: true})
			continue
		}
		v, err := a.eval(env)
		if err != nil {
			return nil, err
		}
		args = append(args, Value{Num: v})
	}
	return env.Call(n.name, args)
}

func (n *binNode) eval(env Env) (*big.Rat, error) {
	l, err := n.left.eval(env)
	if err != nil {
		return nil, err
	}
	r, err := n.right.eval(env)
	if err != nil {
		return nil, err
	}
	switch n.op {
	case '+':
		return new(big.Rat).Add(l, r), nil
	case '-':
		return new(big.Rat).Sub(l, r), nil
	case '*':
		return new(big.Rat).Mul(l, r), nil
	case '/':
		if r.Sign() == 0 {
			return nil, fmt.Errorf("division by zero")
		}
		return new(big.Rat).Quo(l, r), nil
	}
	return nil, fmt.Errorf("unknown operator %q", string(n.op))
}

func (n *unaryNode) eval(env Env) (*big.Rat, error) {
	v, err := n.operand.eval(env)
	if err != nil {
		return nil, err
	}
	if n.neg {
		return new(big.Rat).Neg(v), nil
	}
	return v, nil
}

func (n *percentNode) eval(env Env) (*big.Rat, error) {
	v, err := n.operand.eval(env)
	if err != nil {
		return nil, err
	}
	return new(big.Rat).Quo(v, big.NewRat(100, 1)), nil
}

type tokKind int

const (
	tokNum tokKind = iota
	tokStr
	tokIdent
	tokOp
)

type token struct {
	kind tokKind
	text string
	num  *big.Rat
}

// lex splits the source into tokens. Digit grouping and currency symbols are
// stripped here so that a figure pasted from a statement just works.
func lex(src string) []token {
	var toks []token
	rs := []rune(src)
	for i := 0; i < len(rs); {
		c := rs[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		case c == ',' && i+1 < len(rs) && isDigit(rs[i+1]) && len(toks) > 0 && toks[len(toks)-1].kind == tokNum:
			// A comma between digits is grouping, not an argument separator.
			i++
			start := i
			for i < len(rs) && isDigit(rs[i]) {
				i++
			}
			last := &toks[len(toks)-1]
			last.text += string(rs[start:i])
			last.num, _ = new(big.Rat).SetString(last.text)
		case isDigit(c) || c == '.':
			start := i
			for i < len(rs) && (isDigit(rs[i]) || rs[i] == '.') {
				i++
			}
			text := string(rs[start:i])
			v, ok := new(big.Rat).SetString(text)
			if !ok {
				v = new(big.Rat)
			}
			toks = append(toks, token{kind: tokNum, text: text, num: v})
		case c == '"' || c == '\'':
			quote := c
			i++
			start := i
			for i < len(rs) && rs[i] != quote {
				i++
			}
			toks = append(toks, token{kind: tokStr, text: string(rs[start:i])})
			if i < len(rs) {
				i++
			}
		case isIdentStart(c):
			start := i
			for i < len(rs) && isIdentPart(rs[i]) {
				i++
			}
			toks = append(toks, token{kind: tokIdent, text: string(rs[start:i])})
		case c == '₹' || c == '$' || c == '€' || c == '£' || c == '_':
			i++
		default:
			toks = append(toks, token{kind: tokOp, text: string(c)})
			i++
		}
	}
	return toks
}

func isDigit(r rune) bool      { return r >= '0' && r <= '9' }
func isIdentStart(r rune) bool { return r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' }
func isIdentPart(r rune) bool  { return isIdentStart(r) || isDigit(r) }

type parser struct {
	toks []token
	pos  int
}

func (p *parser) peek() *token {
	if p.pos >= len(p.toks) {
		return nil
	}
	return &p.toks[p.pos]
}

func (p *parser) isOp(s string) bool {
	t := p.peek()
	return t != nil && t.kind == tokOp && t.text == s
}

func (p *parser) parseExpr() (Node, error) {
	left, err := p.parseTerm()
	if err != nil {
		return nil, err
	}
	for {
		switch {
		case p.isOp("+"):
			p.pos++
			right, err := p.parseTerm()
			if err != nil {
				return nil, err
			}
			left = &binNode{op: '+', left: left, right: right}
		case p.isOp("-"):
			p.pos++
			right, err := p.parseTerm()
			if err != nil {
				return nil, err
			}
			left = &binNode{op: '-', left: left, right: right}
		default:
			return left, nil
		}
	}
}

func (p *parser) parseTerm() (Node, error) {
	left, err := p.parseFactor()
	if err != nil {
		return nil, err
	}
	for {
		switch {
		case p.isOp("*"):
			p.pos++
			right, err := p.parseFactor()
			if err != nil {
				return nil, err
			}
			left = &binNode{op: '*', left: left, right: right}
		case p.isOp("/"):
			p.pos++
			right, err := p.parseFactor()
			if err != nil {
				return nil, err
			}
			left = &binNode{op: '/', left: left, right: right}
		default:
			return left, nil
		}
	}
}

func (p *parser) parseFactor() (Node, error) {
	if p.isOp("-") {
		p.pos++
		v, err := p.parseFactor()
		if err != nil {
			return nil, err
		}
		return &unaryNode{neg: true, operand: v}, nil
	}
	if p.isOp("+") {
		p.pos++
		return p.parseFactor()
	}
	n, err := p.parsePrimary()
	if err != nil {
		return nil, err
	}
	if p.isOp("%") {
		p.pos++
		n = &percentNode{operand: n}
	}
	return n, nil
}

func (p *parser) parsePrimary() (Node, error) {
	t := p.peek()
	if t == nil {
		return nil, fmt.Errorf("expression ends too soon")
	}
	switch {
	case t.kind == tokNum:
		p.pos++
		return &numNode{v: t.num}, nil
	case t.kind == tokStr:
		p.pos++
		return &strNode{s: t.text}, nil
	case t.kind == tokIdent:
		name := strings.ToLower(t.text)
		p.pos++
		if p.isOp("(") {
			p.pos++
			var args []Node
			if !p.isOp(")") {
				for {
					a, err := p.parseExpr()
					if err != nil {
						return nil, err
					}
					args = append(args, a)
					if p.isOp(",") {
						p.pos++
						continue
					}
					break
				}
			}
			if !p.isOp(")") {
				return nil, fmt.Errorf("missing ) after %s(", name)
			}
			p.pos++
			return &callNode{name: name, args: args}, nil
		}
		return &varNode{name: name}, nil
	case t.kind == tokOp && t.text == "(":
		p.pos++
		n, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		if !p.isOp(")") {
			return nil, fmt.Errorf("missing )")
		}
		p.pos++
		return n, nil
	}
	return nil, fmt.Errorf("unexpected %q", t.text)
}
