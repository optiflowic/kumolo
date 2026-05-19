package dynamodb

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

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

// exprOperand resolves to a DynamoDB typed value from an item, names, or values.
type exprOperand interface {
	resolve(item map[string]any, names map[string]string, values map[string]any) (any, error)
	// attrName returns the attribute name for exists checks; ("", false) if not a path.
	attrName(names map[string]string) (string, bool)
}

type nameRefOperand struct{ ref string }

// Callers must invoke validateExprRefs before eval to guarantee o.ref is present.
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

type valRefOperand struct{ ref string }

// Callers must invoke validateExprRefs before eval to guarantee o.ref is present.
func (o valRefOperand) resolve(
	_ map[string]any,
	_ map[string]string,
	values map[string]any,
) (any, error) {
	return values[o.ref], nil
}

func (o valRefOperand) attrName(_ map[string]string) (string, bool) { return "", false }

type plainOperand struct{ name string }

func (o plainOperand) resolve(
	item map[string]any,
	_ map[string]string,
	_ map[string]any,
) (any, error) {
	return item[o.name], nil
}

func (o plainOperand) attrName(_ map[string]string) (string, bool) { return o.name, true }

type sizeOperand struct{ path exprOperand }

func (o sizeOperand) resolve(
	item map[string]any,
	names map[string]string,
	values map[string]any,
) (any, error) {
	val, err := o.path.resolve(item, names, values)
	if err != nil {
		return nil, err
	}
	if val == nil {
		return nil, nil
	}
	n, err := dynamoAttrSize(val)
	if err != nil {
		return nil, err
	}
	return map[string]any{"N": fmt.Sprintf("%d", n)}, nil
}

func (o sizeOperand) attrName(_ map[string]string) (string, bool) { return "", false }

func dynamoAttrSize(val any) (int, error) {
	m, ok := val.(map[string]any)
	if !ok {
		return 0, nil
	}
	if s, ok := m["S"].(string); ok {
		return len(s), nil
	}
	if n, ok := m["N"].(string); ok {
		return len(n), nil
	}
	if b, ok := m["B"].(string); ok {
		decoded, err := base64.StdEncoding.DecodeString(b)
		if err != nil {
			return 0, nil
		}
		return len(decoded), nil
	}
	for _, setKey := range []string{"SS", "NS", "BS"} {
		if elems, ok := m[setKey].([]any); ok {
			return len(elems), nil
		}
	}
	if elems, ok := m["L"].([]any); ok {
		return len(elems), nil
	}
	if entries, ok := m["M"].(map[string]any); ok {
		return len(entries), nil
	}
	return 0, fmt.Errorf("size() does not support this attribute type")
}

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

type inCondNode struct {
	attr   exprOperand
	values []exprOperand
}

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
	if err != nil {
		return false, err
	}
	return !v, nil
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
	if n.op == "=" || n.op == "<>" {
		// json.Marshal only fails for unmarshalable types (channels, funcs), not DynamoDB values
		lj, _ := json.Marshal(lv)
		rj, _ := json.Marshal(rv)
		eq := string(lj) == string(rj)
		if n.op == "=" {
			return eq, nil
		}
		return !eq, nil
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
		// Reachable only when operand is not an attr path (e.g. valRefOperand).
		// #nameRef operands are guaranteed valid by validateExprRefs before eval.
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
	if as, ok := pm["S"].(string); ok {
		if bs, ok := sm["S"].(string); ok {
			return strings.Contains(as, bs), nil
		}
		return false, nil
	}
	if al, ok := pm["L"].([]any); ok {
		sj, _ := json.Marshal(searchVal)
		for _, elem := range al {
			ej, _ := json.Marshal(elem)
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

func (n inCondNode) eval(
	item map[string]any,
	names map[string]string,
	values map[string]any,
) (bool, error) {
	attrVal, err := n.attr.resolve(item, names, values)
	if err != nil {
		return false, err
	}
	attrJ, _ := json.Marshal(attrVal)
	resolved := make([]string, len(n.values))
	for i, v := range n.values {
		val, err := v.resolve(item, names, values)
		if err != nil {
			return false, err
		}
		vj, _ := json.Marshal(val)
		resolved[i] = string(vj)
	}
	attrJStr := string(attrJ)
	for _, vj := range resolved {
		if attrJStr == vj {
			return true, nil
		}
	}
	return false, nil
}

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

var tokenKindNames = map[tokenKind]string{
	tokLParen: "'('",
	tokRParen: "')'",
	tokComma:  "','",
}

func tokenKindName(k tokenKind) string {
	if s, ok := tokenKindNames[k]; ok {
		return s
	}
	return fmt.Sprintf("token(%d)", k)
}

func (p *exprParser) expectKind(kind tokenKind) (exprToken, error) {
	t := p.consume()
	if t.kind != kind {
		return exprToken{}, fmt.Errorf("expected %s, got %q", tokenKindName(kind), t.val)
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
	if p.isKeyword("SIZE") {
		return p.parseSizeFunc()
	}
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

func (p *exprParser) parseSizeFunc() (exprOperand, error) {
	p.consume()
	if _, err := p.expectKind(tokLParen); err != nil {
		return nil, err
	}
	path, err := p.parseAttrPath()
	if err != nil {
		return nil, err
	}
	if _, err := p.expectKind(tokRParen); err != nil {
		return nil, err
	}
	return sizeOperand{path}, nil
}

func (p *exprParser) parseComparison() (condNode, error) {
	// LHS of any comparison or BETWEEN must be an attribute path or size(), not a value ref.
	var left exprOperand
	var err error
	if p.isKeyword("SIZE") {
		left, err = p.parseSizeFunc()
	} else {
		left, err = p.parseAttrPath()
	}
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

	if p.isKeyword("IN") {
		p.consume()
		if _, err := p.expectKind(tokLParen); err != nil {
			return nil, err
		}
		first, err := p.parseOperand()
		if err != nil {
			return nil, err
		}
		inValues := []exprOperand{first}
		for p.peek().kind == tokComma {
			p.consume()
			val, err := p.parseOperand()
			if err != nil {
				return nil, err
			}
			inValues = append(inValues, val)
			if len(inValues) > 100 {
				return nil, fmt.Errorf(
					"IN operator supports at most 100 values, got %d",
					len(inValues),
				)
			}
		}
		if _, err := p.expectKind(tokRParen); err != nil {
			return nil, err
		}
		return inCondNode{left, inValues}, nil
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

// validateExprRefs tokenizes expr and returns an error if any :valRef token is
// absent from attrValues or any #nameRef token is absent from attrNames.
// Called after parseFilterExpr succeeds and before item evaluation so that missing
// references are caught even when the result set is empty.
func validateExprRefs(
	expr string,
	attrNames map[string]string,
	attrValues map[string]any,
) error {
	toks, _ := tokenizeExpr(expr) // always succeeds: same expr already accepted by parseFilterExpr
	for _, tok := range toks {
		switch tok.kind {
		case tokValRef:
			if _, ok := attrValues[tok.val]; !ok {
				return fmt.Errorf("ExpressionAttributeValues missing %q", tok.val)
			}
		case tokNameRef:
			if _, ok := attrNames[tok.val]; !ok {
				return fmt.Errorf("ExpressionAttributeNames missing %q", tok.val)
			}
		}
	}
	return nil
}

// evalFilterExpr evaluates a DynamoDB filter/condition expression against an item.
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
	if err := validateExprRefs(expr, attrNames, attrValues); err != nil {
		return false, err
	}
	return node.eval(item, attrNames, attrValues)
}

// projSegment is one step in a DynamoDB document path.
// Either an attribute name step (attr != "") or a list-index step (attr == "").
type projSegment struct {
	attr  string
	index int // only meaningful when attr == ""
}

// projNode is a node in a projection tree.
// isLeaf means "take the whole value at this level" (no further descent).
type projNode struct {
	isLeaf   bool // set when this path was projected as a whole (e.g. "address")
	children map[string]*projNode
	listIdxs map[int]*projNode
}

func (n *projNode) addPath(segs []projSegment) {
	if n.isLeaf {
		// Already projecting the whole value; sub-paths are redundant.
		return
	}
	if len(segs) == 0 {
		// This path projects the whole value; discard any narrower sub-paths.
		n.isLeaf = true
		n.children = nil
		n.listIdxs = nil
		return
	}
	seg := segs[0]
	rest := segs[1:]
	if seg.attr != "" {
		if n.children == nil {
			n.children = map[string]*projNode{}
		}
		child, ok := n.children[seg.attr]
		if !ok {
			child = &projNode{}
			n.children[seg.attr] = child
		}
		child.addPath(rest)
	} else {
		if n.listIdxs == nil {
			n.listIdxs = map[int]*projNode{}
		}
		child, ok := n.listIdxs[seg.index]
		if !ok {
			child = &projNode{}
			n.listIdxs[seg.index] = child
		}
		child.addPath(rest)
	}
}

func parseProjPath(token string, attrNames map[string]string) ([]projSegment, error) {
	var segs []projSegment
	dotParts := strings.Split(token, ".")
	for _, part := range dotParts {
		part = strings.TrimSpace(part)
		if part == "" {
			return nil, fmt.Errorf("invalid projection path %q", token)
		}
		lb := strings.Index(part, "[")
		if lb == 0 {
			return nil, fmt.Errorf("invalid projection path %q: attribute name expected", token)
		}
		// Attribute name before the first '[' (or the whole part if no '[')
		attrToken := part
		if lb > 0 {
			attrToken = part[:lb]
		}
		name, err := resolveAttrName(attrToken, attrNames)
		if err != nil {
			return nil, fmt.Errorf("projection expression: %w", err)
		}
		segs = append(segs, projSegment{attr: name})
		if lb == -1 {
			continue
		}
		rest := part[lb:]
		for len(rest) > 0 {
			if rest[0] != '[' {
				return nil, fmt.Errorf("invalid projection path %q: unexpected %q", token, rest)
			}
			rb := strings.Index(rest, "]")
			if rb == -1 {
				return nil, fmt.Errorf("invalid projection path %q: missing ']'", token)
			}
			idxStr := rest[1:rb]
			n, err := strconv.Atoi(idxStr)
			if err != nil || n < 0 {
				return nil, fmt.Errorf("invalid list index %q in projection path %q", idxStr, token)
			}
			segs = append(segs, projSegment{attr: "", index: n})
			rest = rest[rb+1:]
		}
	}
	return segs, nil
}

func projectValue(val any, n *projNode) any {
	if len(n.children) == 0 && len(n.listIdxs) == 0 {
		return val
	}
	valMap, ok := val.(map[string]any)
	if !ok {
		return val
	}
	if len(n.children) > 0 {
		mRaw, ok := valMap["M"]
		if !ok {
			return val
		}
		mMap, ok := mRaw.(map[string]any)
		if !ok {
			return val
		}
		projected := make(map[string]any, len(n.children))
		for attrName, child := range n.children {
			subVal, ok := mMap[attrName]
			if !ok {
				continue
			}
			projected[attrName] = projectValue(subVal, child)
		}
		return map[string]any{"M": projected}
	}
	lRaw, ok := valMap["L"]
	if !ok {
		return val
	}
	lSlice, ok := lRaw.([]any)
	if !ok {
		return val
	}
	idxs := make([]int, 0, len(n.listIdxs))
	for idx := range n.listIdxs {
		idxs = append(idxs, idx)
	}
	sort.Ints(idxs)
	projected := make([]any, 0, len(idxs))
	for _, idx := range idxs {
		if idx >= len(lSlice) {
			continue
		}
		projected = append(projected, projectValue(lSlice[idx], n.listIdxs[idx]))
	}
	return map[string]any{"L": projected}
}

type parsedProjection struct {
	root *projNode
}

func parseProjectionExpression(
	expr string,
	attrNames map[string]string,
) (*parsedProjection, error) {
	root := &projNode{}
	for _, tok := range strings.Split(expr, ",") {
		tok = strings.TrimSpace(tok)
		if tok == "" {
			continue
		}
		segs, err := parseProjPath(tok, attrNames)
		if err != nil {
			return nil, err
		}
		root.addPath(segs)
	}
	return &parsedProjection{root: root}, nil
}

func (p *parsedProjection) apply(item map[string]any) map[string]any {
	result := make(map[string]any, len(p.root.children))
	for attrName, child := range p.root.children {
		val, ok := item[attrName]
		if !ok {
			continue
		}
		result[attrName] = projectValue(val, child)
	}
	return result
}

// Returns the item unchanged when projExpr is empty.
func applyProjection(
	item map[string]any,
	projExpr string,
	attrNames map[string]string,
) (map[string]any, error) {
	if projExpr == "" {
		return item, nil
	}
	parsed, err := parseProjectionExpression(projExpr, attrNames)
	if err != nil {
		return nil, err
	}
	return parsed.apply(item), nil
}

// Returns the slice unchanged when projExpr is empty.
func applyProjectionToItems(
	items []map[string]any,
	projExpr string,
	attrNames map[string]string,
) ([]map[string]any, error) {
	if projExpr == "" {
		return items, nil
	}
	parsed, err := parseProjectionExpression(projExpr, attrNames)
	if err != nil {
		return nil, err
	}
	result := make([]map[string]any, len(items))
	for i, item := range items {
		result[i] = parsed.apply(item)
	}
	return result, nil
}

// applyFilterExpression filters items by a DynamoDB filter expression.
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
	if err := validateExprRefs(filterExpr, attrNames, attrValues); err != nil {
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
