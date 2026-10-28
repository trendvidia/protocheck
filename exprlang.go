package protocheck

// The RFC-001 §5.4 expression language — the one language every @validate
// rule is written in, made normative on 2026-09-07 (protowire#282) from the
// dialect this file shipped, plus now(). The compiler parses each capture
// against the same grammar (protocompile#211); this file parses it again at
// descriptor-load time and evaluates it, checking operand kinds as it goes.
//
//	expr    := or
//	or      := and ("||" and)*
//	and     := unary ("&&" unary)*
//	unary   := "!" unary | cmp
//	cmp     := term (("=="|"!="|"<="|">="|"<"|">") term)? | term "in" term
//	term    := INT | FLOAT | STRING | "true" | "false" | list
//	         | "this" postfix* | IDENT ("." IDENT)* "(" args ")" | "(" expr ")"
//	postfix := "." IDENT "(" args ")"      (builtin method)
//	list    := "[" (term ("," term))* "]"
//
// Bare function calls resolve against the expression's compiler-extracted
// call list (Expression.calls, complete for declared functions per RFC-001
// §8.1) and dispatch to the RFC-001 §9.2 registry — including Unimplemented
// placeholders, whose returned violation propagates as the failure cause.
// Builtins on values, in method form and in function form with the receiver
// first: size, starts_with, ends_with, contains, matches. now() has no
// receiver and yields the run's instant — the same for every rule evaluated
// in one Validate call (§6.2 rule 5). Temporal values (§6.2 rule 2: a
// Timestamp binds as time.Time, a Duration as time.Duration) compare with
// their own kind only.

import (
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// compiledExpr is a parsed rule expression, ready for repeated evaluation.
type compiledExpr struct {
	src  string
	root exprNode
}

// exprEnv is one evaluation's bindings.
type exprEnv struct {
	this any
	// now is the run's instant, bound once per Validate call so every
	// now() in the run agrees (RFC-001 §6.2 rule 5).
	now time.Time
	// call dispatches a declared function by FQN.
	call func(fqn string, args []any) (bool, *Violation)
	// failure records the last function-returned violation, used as the
	// cause when the whole expression evaluates false.
	failure *Violation
}

type exprNode interface {
	eval(env *exprEnv) (any, error)
}

// ─── Compilation ────────────────────────────────────────────────────────────

// compileExpr parses src. calls maps a callee's local spelling (its FQN's
// trailing segments) to the declared-function FQN, from Expression.calls.
func compileExpr(src string, calls map[string]string) (*compiledExpr, error) {
	return parseExpr(src, &exprParser{lex: newExprLexer(src), calls: calls})
}

// compileCheckExpr parses a protocheckpb Check expression in the §5.4
// language, for validators with no expression engine. Check rules carry no
// compiler-extracted call list, so callees resolves function spellings
// against the registry: see checkCallees.
func compileCheckExpr(src string, callees map[string]string) (*compiledExpr, error) {
	return parseExpr(src, &exprParser{lex: newExprLexer(src), calls: callees, exact: true})
}

func parseExpr(src string, p *exprParser) (*compiledExpr, error) {
	p.next()
	root, err := p.parseOr()
	if err != nil {
		return nil, fmt.Errorf("parsing %q: %w", src, err)
	}
	if p.tok.kind != tokEOF {
		return nil, fmt.Errorf("parsing %q: unexpected %q", src, p.tok.text)
	}
	return &compiledExpr{src: src, root: root}, nil
}

// eval runs the expression. A false result carries the function-returned
// cause when a declared-function call produced one.
func (e *compiledExpr) eval(this any, now time.Time, call func(fqn string, args []any) (bool, *Violation)) (bool, *Violation, error) {
	env := &exprEnv{this: this, now: now, call: call}
	out, err := e.root.eval(env)
	if err != nil {
		return false, nil, err
	}
	b, ok := out.(bool)
	if !ok {
		return false, nil, fmt.Errorf("expression %q is not boolean (got %T)", e.src, out)
	}
	if b {
		return true, nil, nil
	}
	return false, env.failure, nil
}

// ─── Lexer ──────────────────────────────────────────────────────────────────

type tokKind int

const (
	tokEOF tokKind = iota
	tokIdent
	tokInt
	tokFloat
	tokString
	tokPunct // ( ) [ ] , .
	tokOp    // == != <= >= < > && || !
)

type token struct {
	kind tokKind
	text string
}

type exprLexer struct {
	src string
	pos int
}

func newExprLexer(src string) *exprLexer { return &exprLexer{src: src} }

func (l *exprLexer) lex() (token, error) {
	for l.pos < len(l.src) && (l.src[l.pos] == ' ' || l.src[l.pos] == '\t' || l.src[l.pos] == '\n' || l.src[l.pos] == '\r') {
		l.pos++
	}
	if l.pos >= len(l.src) {
		return token{kind: tokEOF}, nil
	}
	c := l.src[l.pos]

	switch {
	case c == '"':
		start := l.pos
		l.pos++
		for l.pos < len(l.src) && l.src[l.pos] != '"' {
			if l.src[l.pos] == '\\' {
				l.pos++
			}
			l.pos++
		}
		if l.pos >= len(l.src) {
			return token{}, fmt.Errorf("unterminated string literal")
		}
		l.pos++
		return token{kind: tokString, text: l.src[start:l.pos]}, nil

	case c >= '0' && c <= '9', c == '-' && l.pos+1 < len(l.src) && l.src[l.pos+1] >= '0' && l.src[l.pos+1] <= '9':
		start := l.pos
		if c == '-' {
			l.pos++
		}
		isFloat := false
		for l.pos < len(l.src) {
			ch := l.src[l.pos]
			if ch >= '0' && ch <= '9' {
				l.pos++
			} else if ch == '.' && !isFloat && l.pos+1 < len(l.src) && l.src[l.pos+1] >= '0' && l.src[l.pos+1] <= '9' {
				isFloat = true
				l.pos++
			} else {
				break
			}
		}
		kind := tokInt
		if isFloat {
			kind = tokFloat
		}
		return token{kind: kind, text: l.src[start:l.pos]}, nil

	case c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z'):
		start := l.pos
		for l.pos < len(l.src) {
			ch := l.src[l.pos]
			if ch == '_' || (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') {
				l.pos++
			} else {
				break
			}
		}
		return token{kind: tokIdent, text: l.src[start:l.pos]}, nil

	case strings.ContainsRune("()[],.", rune(c)):
		l.pos++
		return token{kind: tokPunct, text: string(c)}, nil

	default:
		for _, op := range [...]string{"==", "!=", "<=", ">=", "&&", "||", "<", ">", "!"} {
			if strings.HasPrefix(l.src[l.pos:], op) {
				l.pos += len(op)
				return token{kind: tokOp, text: op}, nil
			}
		}
		return token{}, fmt.Errorf("unexpected character %q", c)
	}
}

// ─── Parser ─────────────────────────────────────────────────────────────────

type exprParser struct {
	lex   *exprLexer
	tok   token
	err   error
	calls map[string]string
	// exact limits callee resolution to calls' own keys. Check
	// expressions resolve against the whole function registry, where a
	// loose suffix scan could pick between same-named functions
	// arbitrarily.
	exact bool
}

func (p *exprParser) next() {
	if p.err != nil {
		return
	}
	p.tok, p.err = p.lex.lex()
}

func (p *exprParser) expect(kind tokKind, text string) error {
	if p.err != nil {
		return p.err
	}
	if p.tok.kind != kind || p.tok.text != text {
		return fmt.Errorf("expected %q, got %q", text, p.tok.text)
	}
	p.next()
	return p.err
}

func (p *exprParser) parseOr() (exprNode, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for p.tok.kind == tokOp && p.tok.text == "||" {
		p.next()
		right, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		left = &binaryNode{op: "||", left: left, right: right}
	}
	return left, p.err
}

func (p *exprParser) parseAnd() (exprNode, error) {
	left, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	for p.tok.kind == tokOp && p.tok.text == "&&" {
		p.next()
		right, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		left = &binaryNode{op: "&&", left: left, right: right}
	}
	return left, p.err
}

func (p *exprParser) parseUnary() (exprNode, error) {
	if p.tok.kind == tokOp && p.tok.text == "!" {
		p.next()
		inner, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return &notNode{inner: inner}, nil
	}
	return p.parseCmp()
}

func (p *exprParser) parseCmp() (exprNode, error) {
	left, err := p.parseTerm()
	if err != nil {
		return nil, err
	}
	if p.tok.kind == tokOp {
		switch op := p.tok.text; op {
		case "==", "!=", "<=", ">=", "<", ">":
			p.next()
			right, err := p.parseTerm()
			if err != nil {
				return nil, err
			}
			return &binaryNode{op: op, left: left, right: right}, nil
		}
	}
	if p.tok.kind == tokIdent && p.tok.text == "in" {
		p.next()
		right, err := p.parseTerm()
		if err != nil {
			return nil, err
		}
		return &binaryNode{op: "in", left: left, right: right}, nil
	}
	return left, p.err
}

func (p *exprParser) parseTerm() (exprNode, error) {
	if p.err != nil {
		return nil, p.err
	}
	switch {
	case p.tok.kind == tokInt:
		n, err := strconv.ParseInt(p.tok.text, 10, 64)
		if err != nil {
			return nil, err
		}
		p.next()
		return &literalNode{val: n}, p.err

	case p.tok.kind == tokFloat:
		f, err := strconv.ParseFloat(p.tok.text, 64)
		if err != nil {
			return nil, err
		}
		p.next()
		return &literalNode{val: f}, p.err

	case p.tok.kind == tokString:
		s, err := strconv.Unquote(p.tok.text)
		if err != nil {
			return nil, fmt.Errorf("bad string literal %s: %w", p.tok.text, err)
		}
		p.next()
		return &literalNode{val: s}, p.err

	case p.tok.kind == tokPunct && p.tok.text == "[":
		p.next()
		var elems []exprNode
		for !(p.tok.kind == tokPunct && p.tok.text == "]") {
			if len(elems) > 0 {
				if err := p.expect(tokPunct, ","); err != nil {
					return nil, err
				}
			}
			el, err := p.parseTerm()
			if err != nil {
				return nil, err
			}
			elems = append(elems, el)
		}
		p.next()
		return &listNode{elems: elems}, p.err

	case p.tok.kind == tokPunct && p.tok.text == "(":
		p.next()
		inner, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		if err := p.expect(tokPunct, ")"); err != nil {
			return nil, err
		}
		return inner, p.err

	case p.tok.kind == tokIdent && p.tok.text == "true":
		p.next()
		return &literalNode{val: true}, p.err
	case p.tok.kind == tokIdent && p.tok.text == "false":
		p.next()
		return &literalNode{val: false}, p.err

	case p.tok.kind == tokIdent && p.tok.text == "this":
		p.next()
		var node exprNode = &thisNode{}
		for p.tok.kind == tokPunct && p.tok.text == "." {
			p.next()
			if p.tok.kind != tokIdent {
				return nil, fmt.Errorf("expected method name after '.', got %q", p.tok.text)
			}
			method := p.tok.text
			p.next()
			args, err := p.parseArgs()
			if err != nil {
				return nil, err
			}
			if method == "now" {
				return nil, fmt.Errorf("now() has no receiver: write now(), not this.now()")
			}
			node = &methodNode{recv: node, method: method, args: args}
		}
		return node, p.err

	case p.tok.kind == tokIdent:
		// Qualified or bare declared-function call.
		name := p.tok.text
		p.next()
		for p.tok.kind == tokPunct && p.tok.text == "." {
			p.next()
			if p.tok.kind != tokIdent {
				return nil, fmt.Errorf("expected identifier after '.', got %q", p.tok.text)
			}
			name += "." + p.tok.text
			p.next()
		}
		args, err := p.parseArgs()
		if err != nil {
			return nil, err
		}
		if fqn, ok := p.resolveCallee(name); ok {
			return &callNode{fqn: fqn, args: args}, p.err
		}
		// Not a declared function: a §5.4 builtin, absent from
		// Expression.calls. now() stands alone — no receiver, no
		// arguments; the others take their receiver first.
		if name == "now" {
			if len(args) != 0 {
				return nil, fmt.Errorf("now() takes no arguments, got %d", len(args))
			}
			return &nowNode{}, p.err
		}
		if isBuiltinFunc(name) {
			if len(args) == 0 {
				return nil, fmt.Errorf("builtin %s expects a receiver argument", name)
			}
			return &methodNode{recv: args[0], method: name, args: args[1:]}, p.err
		}
		return nil, fmt.Errorf("unknown function %q (neither a declared function of this expression nor an engine builtin)", name)

	default:
		return nil, fmt.Errorf("unexpected token %q", p.tok.text)
	}
}

func (p *exprParser) parseArgs() ([]exprNode, error) {
	if err := p.expect(tokPunct, "("); err != nil {
		return nil, err
	}
	var args []exprNode
	for !(p.tok.kind == tokPunct && p.tok.text == ")") {
		if len(args) > 0 {
			if err := p.expect(tokPunct, ","); err != nil {
				return nil, err
			}
		}
		a, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		args = append(args, a)
	}
	p.next()
	return args, p.err
}

// resolveCallee maps a call site's spelling to a declared-function FQN. The
// compiler records resolved call sites in Expression.calls; a spelling
// matches when it equals the FQN or a dot-boundary suffix of it.
func (p *exprParser) resolveCallee(name string) (string, bool) {
	if fqn, ok := p.calls[name]; ok {
		return fqn, true
	}
	if p.exact {
		return "", false
	}
	for _, fqn := range p.calls {
		if fqn == name || strings.HasSuffix(fqn, "."+name) {
			return fqn, true
		}
	}
	return "", false
}

// ─── Nodes ──────────────────────────────────────────────────────────────────

type literalNode struct{ val any }

func (n *literalNode) eval(*exprEnv) (any, error) { return n.val, nil }

type thisNode struct{}

func (n *thisNode) eval(env *exprEnv) (any, error) { return env.this, nil }

// nowNode is the now() builtin: the run's instant (RFC-001 §6.2 rule 5).
type nowNode struct{}

func (n *nowNode) eval(env *exprEnv) (any, error) { return env.now, nil }

type listNode struct{ elems []exprNode }

func (n *listNode) eval(env *exprEnv) (any, error) {
	out := make([]any, len(n.elems))
	for i, el := range n.elems {
		v, err := el.eval(env)
		if err != nil {
			return nil, err
		}
		out[i] = v
	}
	return out, nil
}

type notNode struct{ inner exprNode }

func (n *notNode) eval(env *exprEnv) (any, error) {
	v, err := n.inner.eval(env)
	if err != nil {
		return nil, err
	}
	b, ok := v.(bool)
	if !ok {
		return nil, fmt.Errorf("operand of ! is not boolean (got %T)", v)
	}
	return !b, nil
}

type binaryNode struct {
	op          string
	left, right exprNode
}

func (n *binaryNode) eval(env *exprEnv) (any, error) {
	l, err := n.left.eval(env)
	if err != nil {
		return nil, err
	}
	// Short-circuit boolean operators.
	if n.op == "&&" || n.op == "||" {
		lb, ok := l.(bool)
		if !ok {
			return nil, fmt.Errorf("operand of %s is not boolean (got %T)", n.op, l)
		}
		if (n.op == "&&" && !lb) || (n.op == "||" && lb) {
			return lb, nil
		}
		r, err := n.right.eval(env)
		if err != nil {
			return nil, err
		}
		rb, ok := r.(bool)
		if !ok {
			return nil, fmt.Errorf("operand of %s is not boolean (got %T)", n.op, r)
		}
		return rb, nil
	}

	r, err := n.right.eval(env)
	if err != nil {
		return nil, err
	}

	if n.op == "in" {
		list, ok := r.([]any)
		if !ok {
			return nil, fmt.Errorf("right operand of 'in' is not a list (got %T)", r)
		}
		for _, el := range list {
			if eq, err := valuesEqual(l, el); err == nil && eq {
				return true, nil
			}
		}
		return false, nil
	}

	switch n.op {
	case "==":
		eq, err := valuesEqual(l, r)
		return eq, err
	case "!=":
		eq, err := valuesEqual(l, r)
		return !eq, err
	default: // < <= > >=
		c, err := compareValues(l, r)
		if err != nil {
			return nil, err
		}
		switch n.op {
		case "<":
			return c < 0, nil
		case "<=":
			return c <= 0, nil
		case ">":
			return c > 0, nil
		default:
			return c >= 0, nil
		}
	}
}

type callNode struct {
	fqn  string
	args []exprNode
}

func (n *callNode) eval(env *exprEnv) (any, error) {
	args := make([]any, len(n.args))
	for i, a := range n.args {
		v, err := a.eval(env)
		if err != nil {
			return nil, err
		}
		args[i] = v
	}
	ok, viol := env.call(n.fqn, args)
	if !ok && viol != nil {
		env.failure = viol
	}
	return ok, nil
}

type methodNode struct {
	recv   exprNode
	method string
	args   []exprNode
}

func (n *methodNode) eval(env *exprEnv) (any, error) {
	recv, err := n.recv.eval(env)
	if err != nil {
		return nil, err
	}
	args := make([]any, len(n.args))
	for i, a := range n.args {
		v, err := a.eval(env)
		if err != nil {
			return nil, err
		}
		args[i] = v
	}
	return evalBuiltinMethod(recv, n.method, args)
}

// isBuiltinFunc reports whether name is an engine builtin in function form
// — `matches(this, p)` is `this.matches(p)` with the receiver first.
func isBuiltinFunc(name string) bool {
	switch name {
	case "size", "starts_with", "ends_with", "contains", "matches":
		return true
	}
	return false
}

// evalBuiltinMethod implements the value methods of the protocheck dialect.
func evalBuiltinMethod(recv any, method string, args []any) (any, error) {
	strArg := func() (string, error) {
		if len(args) != 1 {
			return "", fmt.Errorf("%s expects 1 argument, got %d", method, len(args))
		}
		s, ok := args[0].(string)
		if !ok {
			return "", fmt.Errorf("%s expects a string argument, got %T", method, args[0])
		}
		return s, nil
	}

	switch method {
	case "size":
		if len(args) != 0 {
			return nil, fmt.Errorf("size expects no arguments, got %d", len(args))
		}
		switch v := recv.(type) {
		case string:
			return int64(utf8.RuneCountInString(v)), nil
		case []byte:
			return int64(len(v)), nil
		case []any:
			return int64(len(v)), nil
		case map[string]any:
			return int64(len(v)), nil
		default:
			return nil, fmt.Errorf("size is not defined on %T", recv)
		}
	case "starts_with", "ends_with", "contains", "matches":
		s, ok := recv.(string)
		if !ok {
			return nil, fmt.Errorf("%s is not defined on %T", method, recv)
		}
		arg, err := strArg()
		if err != nil {
			return nil, err
		}
		switch method {
		case "starts_with":
			return strings.HasPrefix(s, arg), nil
		case "ends_with":
			return strings.HasSuffix(s, arg), nil
		case "contains":
			return strings.Contains(s, arg), nil
		default: // matches
			re, err := getRegexp(arg)
			if err != nil {
				return nil, fmt.Errorf("matches: %w", err)
			}
			return re.MatchString(s), nil
		}
	default:
		return nil, fmt.Errorf("unknown method %q", method)
	}
}

// ─── Value semantics ────────────────────────────────────────────────────────

// numeric widens a numeric value for cross-type comparison.
func numeric(v any) (float64, bool) {
	switch n := v.(type) {
	case int64:
		return float64(n), true
	case int:
		return float64(n), true
	case uint64:
		return float64(n), true
	case float64:
		return n, true
	default:
		return 0, false
	}
}

// temporalEqual and temporalCompare implement RFC-001 §6.2 rule 2:
// temporal values compare with their own kind — a Timestamp with a
// Timestamp, a Duration with a Duration — and with nothing else. ok is
// false when neither operand is temporal, so the numeric and string
// paths take over.
func temporalEqual(a, b any) (eq, ok bool, err error) {
	switch av := a.(type) {
	case time.Time:
		bv, isT := b.(time.Time)
		if !isT {
			return false, true, fmt.Errorf("cannot compare %T with %T", a, b)
		}
		return av.Equal(bv), true, nil
	case time.Duration:
		bv, isD := b.(time.Duration)
		if !isD {
			return false, true, fmt.Errorf("cannot compare %T with %T", a, b)
		}
		return av == bv, true, nil
	}
	if _, isT := b.(time.Time); isT {
		return false, true, fmt.Errorf("cannot compare %T with %T", a, b)
	}
	if _, isD := b.(time.Duration); isD {
		return false, true, fmt.Errorf("cannot compare %T with %T", a, b)
	}
	return false, false, nil
}

func temporalCompare(a, b any) (c int, ok bool, err error) {
	switch av := a.(type) {
	case time.Time:
		bv, isT := b.(time.Time)
		if !isT {
			return 0, true, fmt.Errorf("cannot order %T against %T", a, b)
		}
		return av.Compare(bv), true, nil
	case time.Duration:
		bv, isD := b.(time.Duration)
		if !isD {
			return 0, true, fmt.Errorf("cannot order %T against %T", a, b)
		}
		switch {
		case av < bv:
			return -1, true, nil
		case av > bv:
			return 1, true, nil
		}
		return 0, true, nil
	}
	if _, isT := b.(time.Time); isT {
		return 0, true, fmt.Errorf("cannot order %T against %T", a, b)
	}
	if _, isD := b.(time.Duration); isD {
		return 0, true, fmt.Errorf("cannot order %T against %T", a, b)
	}
	return 0, false, nil
}

func valuesEqual(a, b any) (bool, error) {
	if eq, ok, err := temporalEqual(a, b); ok {
		return eq, err
	}
	if fa, ok := numeric(a); ok {
		fb, ok := numeric(b)
		if !ok {
			return false, fmt.Errorf("cannot compare %T with %T", a, b)
		}
		return fa == fb, nil
	}
	switch av := a.(type) {
	case string:
		bv, ok := b.(string)
		return ok && av == bv, nil
	case bool:
		bv, ok := b.(bool)
		return ok && av == bv, nil
	default:
		return false, fmt.Errorf("cannot compare %T with %T", a, b)
	}
}

func compareValues(a, b any) (int, error) {
	if c, ok, err := temporalCompare(a, b); ok {
		return c, err
	}
	if fa, ok := numeric(a); ok {
		fb, ok := numeric(b)
		if !ok {
			return 0, fmt.Errorf("cannot order %T against %T", a, b)
		}
		switch {
		case fa < fb:
			return -1, nil
		case fa > fb:
			return 1, nil
		default:
			return 0, nil
		}
	}
	if as, ok := a.(string); ok {
		bs, ok := b.(string)
		if !ok {
			return 0, fmt.Errorf("cannot order %T against %T", a, b)
		}
		return strings.Compare(as, bs), nil
	}
	return 0, fmt.Errorf("cannot order %T values", a)
}
