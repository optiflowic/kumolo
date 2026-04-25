package dynamodb

import (
	"encoding/json"
	"fmt"
	"strings"
)

// --- Tokenizer ---

type tokenKind int

const (
	tokEOF     tokenKind = iota
	tokIdent             // AND, OR, NOT, BETWEEN, function names, plain attr names
	tokNameRef           // #attrName
	tokValRef            // :valRef
	tokLParen            // (
	tokRParen            // )
	tokComma             // ,
	tokEQ                // =
	tokNEQ               // <>
	tokLT                // <
	tokLTE               // <=
	tokGT                // >
	tokGTE               // >=
)

type exprToken struct {
	kind tokenKind
	val  string
}

func isExprLetter(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '_'
}

func isExprDigit(c byte) bool {
	return c >= '0' && c <= '9'
}

func tokenizeExpr(expr string) ([]exprToken, error) {
	var toks []exprToken
	i := 0
	for i < len(expr) {
		switch {
		case expr[i] == ' ' || expr[i] == '\t' || expr[i] == '\n' || expr[i] == '\r':
			i++
		case expr[i] == '(':
			toks = append(toks, exprToken{tokLParen, "("})
			i++
		case expr[i] == ')':
			toks = append(toks, exprToken{tokRParen, ")"})
			i++
		case expr[i] == ',':
			toks = append(toks, exprToken{tokComma, ","})
			i++
		case expr[i] == '=':
			toks = append(toks, exprToken{tokEQ, "="})
			i++
		case expr[i] == '<':
			if i+1 < len(expr) && expr[i+1] == '>' {
				toks = append(toks, exprToken{tokNEQ, "<>"})
				i += 2
			} else if i+1 < len(expr) && expr[i+1] == '=' {
				toks = append(toks, exprToken{tokLTE, "<="})
				i += 2
			} else {
				toks = append(toks, exprToken{tokLT, "<"})
				i++
			}
		case expr[i] == '>':
			if i+1 < len(expr) && expr[i+1] == '=' {
				toks = append(toks, exprToken{tokGTE, ">="})
				i += 2
			} else {
				toks = append(toks, exprToken{tokGT, ">"})
				i++
			}
		case expr[i] == '#':
			j := i + 1
			for j < len(expr) && (isExprLetter(expr[j]) || isExprDigit(expr[j])) {
				j++
			}
			if j == i+1 {
				return nil, fmt.Errorf("invalid attribute name placeholder at position %d", i)
			}
			toks = append(toks, exprToken{tokNameRef, expr[i:j]})
			i = j
		case expr[i] == ':':
			j := i + 1
			for j < len(expr) && (isExprLetter(expr[j]) || isExprDigit(expr[j])) {
				j++
			}
			if j == i+1 {
				return nil, fmt.Errorf("invalid attribute value placeholder at position %d", i)
			}
			toks = append(toks, exprToken{tokValRef, expr[i:j]})
			i = j
		case isExprLetter(expr[i]):
			j := i
			for j < len(expr) && (isExprLetter(expr[j]) || isExprDigit(expr[j])) {
				j++
			}
			toks = append(toks, exprToken{tokIdent, expr[i:j]})
			i = j
		default:
			return nil, fmt.Errorf("unexpected character %q at position %d", expr[i], i)
		}
	}
	toks = append(toks, exprToken{tokEOF, ""})
	return toks, nil
}

// --- Operands ---

// exprOperand resolves to a DynamoDB typed value from an item, names, or values.
type exprOperand interface {
	resolve(item map[string]any, names map[string]string, values map[string]any) (any, error)
	// attrName returns the plain attribute name for attribute_exists/not_exists checks.
	// Returns ("", false) when the operand is not an attribute path.
	attrName(names map[string]string) (string, bool)
}

// nameRefOperand resolves a #name reference → item attribute value.
type nameRefOperand struct{ ref string }

func (o nameRefOperand) resolve(
	item map[string]any,
	names map[string]string,
	_ map[string]any,
) (any, error) {
	actual, ok := names[o.ref]
	if !ok {
		return nil, fmt.Errorf("ExpressionAttributeNames missing %q", o.ref)
	}
	return item[actual], nil
}

func (o nameRefOperand) attrName(names map[string]string) (string, bool) {
	actual, ok := names[o.ref]
	return actual, ok
}

// valRefOperand resolves a :val reference → ExpressionAttributeValues entry.
type valRefOperand struct{ ref string }

func (o valRefOperand) resolve(
	_ map[string]any,
	_ map[string]string,
	values map[string]any,
) (any, error) {
	v, ok := values[o.ref]
	if !ok {
		return nil, fmt.Errorf("ExpressionAttributeValues missing %q", o.ref)
	}
	return v, nil
}

func (o valRefOperand) attrName(_ map[string]string) (string, bool) { return "", false }

// plainOperand resolves a bare attribute name → item attribute value.
type plainOperand struct{ name string }

func (o plainOperand) resolve(
	item map[string]any,
	_ map[string]string,
	_ map[string]any,
) (any, error) {
	return item[o.name], nil
}

func (o plainOperand) attrName(_ map[string]string) (string, bool) { return o.name, true }

// --- AST nodes ---

type condNode interface {
	eval(item map[string]any, names map[string]string, values map[string]any) (bool, error)
}

type andCondNode struct{ left, right condNode }
type orCondNode struct{ left, right condNode }
type notCondNode struct{ operand condNode }

type cmpCondNode struct {
	left  exprOperand
	op    string
	right exprOperand
}

type attrExistsCondNode struct {
	operand exprOperand
	negate  bool
}

type beginsWithCondNode struct {
	path   exprOperand
	prefix exprOperand
}

type containsCondNode struct {
	path exprOperand
	val  exprOperand
}

type betweenCondNode struct {
	attr exprOperand
	lo   exprOperand
	hi   exprOperand
}

// --- Evaluators ---

func (n andCondNode) eval(
	item map[string]any,
	names map[string]string,
	values map[string]any,
) (bool, error) {
	l, err := n.left.eval(item, names, values)
	if err != nil || !l {
		return false, err
	}
	return n.right.eval(item, names, values)
}

func (n orCondNode) eval(
	item map[string]any,
	names map[string]string,
	values map[string]any,
) (bool, error) {
	l, err := n.left.eval(item, names, values)
	if err != nil {
		return false, err
	}
	if l {
		return true, nil
	}
	return n.right.eval(item, names, values)
}

func (n notCondNode) eval(
	item map[string]any,
	names map[string]string,
	values map[string]any,
) (bool, error) {
	v, err := n.operand.eval(item, names, values)
	return !v, err
}

func (n cmpCondNode) eval(
	item map[string]any,
	names map[string]string,
	values map[string]any,
) (bool, error) {
	lv, err := n.left.resolve(item, names, values)
	if err != nil {
		return false, err
	}
	rv, err := n.right.resolve(item, names, values)
	if err != nil {
		return false, err
	}
	if n.op == "=" {
		lj, _ := json.Marshal(lv) // json.Marshal only fails for unmarshalable types
		rj, _ := json.Marshal(rv) // json.Marshal only fails for unmarshalable types
		return string(lj) == string(rj), nil
	}
	if n.op == "<>" {
		lj, _ := json.Marshal(lv) // json.Marshal only fails for unmarshalable types
		rj, _ := json.Marshal(rv) // json.Marshal only fails for unmarshalable types
		return string(lj) != string(rj), nil
	}
	cmp, err := dynamoValueCmp(lv, rv)
	if err != nil {
		return false, nil // type mismatch → no match
	}
	switch n.op {
	case "<":
		return cmp < 0, nil
	case "<=":
		return cmp <= 0, nil
	case ">":
		return cmp > 0, nil
	case ">=":
		return cmp >= 0, nil
	}
	return false, fmt.Errorf("unknown operator %q", n.op)
}

func (n attrExistsCondNode) eval(
	item map[string]any,
	names map[string]string,
	_ map[string]any,
) (bool, error) {
	attrName, ok := n.operand.attrName(names)
	if !ok {
		// nameRefOperand returns false when the #ref is absent from ExpressionAttributeNames.
		if nr, isRef := n.operand.(nameRefOperand); isRef {
			return false, fmt.Errorf("ExpressionAttributeNames missing %q", nr.ref)
		}
		return false, fmt.Errorf("attribute_exists/attribute_not_exists requires an attribute path")
	}
	_, exists := item[attrName]
	if n.negate {
		return !exists, nil
	}
	return exists, nil
}

func (n beginsWithCondNode) eval(
	item map[string]any,
	names map[string]string,
	values map[string]any,
) (bool, error) {
	pathVal, err := n.path.resolve(item, names, values)
	if err != nil {
		return false, err
	}
	prefixVal, err := n.prefix.resolve(item, names, values)
	if err != nil {
		return false, err
	}
	pm, pok := pathVal.(map[string]any)
	fm, fok := prefixVal.(map[string]any)
	if !pok || !fok {
		return false, nil
	}
	as, aok := pm["S"].(string)
	bs, bok := fm["S"].(string)
	return aok && bok && strings.HasPrefix(as, bs), nil
}

func (n containsCondNode) eval(
	item map[string]any,
	names map[string]string,
	values map[string]any,
) (bool, error) {
	pathVal, err := n.path.resolve(item, names, values)
	if err != nil {
		return false, err
	}
	searchVal, err := n.val.resolve(item, names, values)
	if err != nil {
		return false, err
	}
	pm, pok := pathVal.(map[string]any)
	sm, sok := searchVal.(map[string]any)
	if !pok || !sok {
		return false, nil
	}
	// String contains substring
	if as, ok := pm["S"].(string); ok {
		if bs, ok := sm["S"].(string); ok {
			return strings.Contains(as, bs), nil
		}
		return false, nil
	}
	// List contains element
	if al, ok := pm["L"].([]any); ok {
		sj, _ := json.Marshal(searchVal) // json.Marshal only fails for unmarshalable types
		for _, elem := range al {
			ej, _ := json.Marshal(elem) // json.Marshal only fails for unmarshalable types
			if string(ej) == string(sj) {
				return true, nil
			}
		}
		return false, nil
	}
	return false, nil
}

func (n betweenCondNode) eval(
	item map[string]any,
	names map[string]string,
	values map[string]any,
) (bool, error) {
	attrVal, err := n.attr.resolve(item, names, values)
	if err != nil {
		return false, err
	}
	loVal, err := n.lo.resolve(item, names, values)
	if err != nil {
		return false, err
	}
	hiVal, err := n.hi.resolve(item, names, values)
	if err != nil {
		return false, err
	}
	c1, err1 := dynamoValueCmp(attrVal, loVal)
	c2, err2 := dynamoValueCmp(attrVal, hiVal)
	return err1 == nil && err2 == nil && c1 >= 0 && c2 <= 0, nil
}

// --- Parser ---

type exprParser struct {
	toks []exprToken
	pos  int
}

func (p *exprParser) peek() exprToken {
	return p.toks[p.pos]
}

func (p *exprParser) consume() exprToken {
	t := p.toks[p.pos]
	p.pos++
	return t
}

func (p *exprParser) expectKind(kind tokenKind) (exprToken, error) {
	t := p.consume()
	if t.kind != kind {
		return exprToken{}, fmt.Errorf("expected token %d, got %q", kind, t.val)
	}
	return t, nil
}

func (p *exprParser) isKeyword(upper string) bool {
	t := p.peek()
	return t.kind == tokIdent && strings.ToUpper(t.val) == upper
}

func (p *exprParser) parseOr() (condNode, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for p.isKeyword("OR") {
		p.consume()
		right, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		left = orCondNode{left, right}
	}
	return left, nil
}

func (p *exprParser) parseAnd() (condNode, error) {
	left, err := p.parseNot()
	if err != nil {
		return nil, err
	}
	for p.isKeyword("AND") {
		p.consume()
		right, err := p.parseNot()
		if err != nil {
			return nil, err
		}
		left = andCondNode{left, right}
	}
	return left, nil
}

func (p *exprParser) parseNot() (condNode, error) {
	if p.isKeyword("NOT") {
		p.consume()
		operand, err := p.parsePrimary()
		if err != nil {
			return nil, err
		}
		return notCondNode{operand}, nil
	}
	return p.parsePrimary()
}

func (p *exprParser) parsePrimary() (condNode, error) {
	t := p.peek()

	if t.kind == tokLParen {
		p.consume()
		node, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		if _, err := p.expectKind(tokRParen); err != nil {
			return nil, err
		}
		return node, nil
	}

	if t.kind == tokIdent {
		switch strings.ToLower(t.val) {
		case "attribute_exists":
			return p.parseAttrExistsFunc(false)
		case "attribute_not_exists":
			return p.parseAttrExistsFunc(true)
		case "begins_with":
			return p.parseBeginsWithFunc()
		case "contains":
			return p.parseContainsFunc()
		}
	}

	return p.parseComparison()
}

func (p *exprParser) parseAttrPath() (exprOperand, error) {
	t := p.peek()
	switch t.kind {
	case tokNameRef:
		p.consume()
		return nameRefOperand{t.val}, nil
	case tokIdent:
		p.consume()
		return plainOperand{t.val}, nil
	}
	return nil, fmt.Errorf("expected attribute path, got %q", t.val)
}

func (p *exprParser) parseOperand() (exprOperand, error) {
	t := p.peek()
	switch t.kind {
	case tokValRef:
		p.consume()
		return valRefOperand{t.val}, nil
	case tokNameRef:
		p.consume()
		return nameRefOperand{t.val}, nil
	case tokIdent:
		p.consume()
		return plainOperand{t.val}, nil
	}
	return nil, fmt.Errorf("expected operand, got %q", t.val)
}

func (p *exprParser) parseAttrExistsFunc(negate bool) (condNode, error) {
	p.consume()
	if _, err := p.expectKind(tokLParen); err != nil {
		return nil, err
	}
	op, err := p.parseAttrPath()
	if err != nil {
		return nil, err
	}
	if _, err := p.expectKind(tokRParen); err != nil {
		return nil, err
	}
	return attrExistsCondNode{op, negate}, nil
}

func (p *exprParser) parseBeginsWithFunc() (condNode, error) {
	p.consume()
	if _, err := p.expectKind(tokLParen); err != nil {
		return nil, err
	}
	path, err := p.parseAttrPath()
	if err != nil {
		return nil, err
	}
	if _, err := p.expectKind(tokComma); err != nil {
		return nil, err
	}
	prefix, err := p.parseOperand()
	if err != nil {
		return nil, err
	}
	if _, err := p.expectKind(tokRParen); err != nil {
		return nil, err
	}
	return beginsWithCondNode{path, prefix}, nil
}

func (p *exprParser) parseContainsFunc() (condNode, error) {
	p.consume()
	if _, err := p.expectKind(tokLParen); err != nil {
		return nil, err
	}
	path, err := p.parseAttrPath()
	if err != nil {
		return nil, err
	}
	if _, err := p.expectKind(tokComma); err != nil {
		return nil, err
	}
	val, err := p.parseOperand()
	if err != nil {
		return nil, err
	}
	if _, err := p.expectKind(tokRParen); err != nil {
		return nil, err
	}
	return containsCondNode{path, val}, nil
}

func (p *exprParser) parseComparison() (condNode, error) {
	// LHS of any comparison or BETWEEN must be an attribute path, not a value ref.
	left, err := p.parseAttrPath()
	if err != nil {
		return nil, err
	}

	if p.isKeyword("BETWEEN") {
		p.consume()
		lo, err := p.parseOperand()
		if err != nil {
			return nil, err
		}
		if !p.isKeyword("AND") {
			return nil, fmt.Errorf("expected AND in BETWEEN expression, got %q", p.peek().val)
		}
		p.consume()
		hi, err := p.parseOperand()
		if err != nil {
			return nil, err
		}
		return betweenCondNode{left, lo, hi}, nil
	}

	t := p.peek()
	var op string
	switch t.kind {
	case tokEQ:
		op = "="
	case tokNEQ:
		op = "<>"
	case tokLT:
		op = "<"
	case tokLTE:
		op = "<="
	case tokGT:
		op = ">"
	case tokGTE:
		op = ">="
	default:
		return nil, fmt.Errorf("expected comparison operator, got %q", t.val)
	}
	p.consume()

	right, err := p.parseOperand()
	if err != nil {
		return nil, err
	}
	return cmpCondNode{left, op, right}, nil
}

// parseFilterExpr parses a DynamoDB filter/condition expression into an AST.
func parseFilterExpr(expr string) (condNode, error) {
	toks, err := tokenizeExpr(expr)
	if err != nil {
		return nil, err
	}
	p := &exprParser{toks: toks}
	node, err := p.parseOr()
	if err != nil {
		return nil, err
	}
	if p.peek().kind != tokEOF {
		return nil, fmt.Errorf("unexpected token %q", p.peek().val)
	}
	return node, nil
}

// evalFilterExpr evaluates a DynamoDB filter/condition expression against an item.
// Returns true if the item satisfies the expression.
func evalFilterExpr(
	expr string,
	item map[string]any,
	attrNames map[string]string,
	attrValues map[string]any,
) (bool, error) {
	node, err := parseFilterExpr(expr)
	if err != nil {
		return false, err
	}
	return node.eval(item, attrNames, attrValues)
}

// applyFilterExpression filters items by a DynamoDB filter expression.
// Returns filtered items and an error if the expression is invalid.
func applyFilterExpression(
	items []map[string]any,
	filterExpr string,
	attrNames map[string]string,
	attrValues map[string]any,
) ([]map[string]any, error) {
	if filterExpr == "" {
		return items, nil
	}
	node, err := parseFilterExpr(filterExpr)
	if err != nil {
		return nil, err
	}
	var filtered []map[string]any
	for _, item := range items {
		ok, err := node.eval(item, attrNames, attrValues)
		if err != nil {
			return nil, err
		}
		if ok {
			filtered = append(filtered, item)
		}
	}
	return filtered, nil
}
