package s3

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode"
)

// selectRow holds a single data row for S3 Select processing.
// headers preserves column order for CSV/JSON output; vals provides O(1) lookup.
type selectRow struct {
	headers []string
	vals    map[string]string
}

func newSelectRow(headers []string) selectRow {
	return selectRow{headers: headers, vals: make(map[string]string, len(headers))}
}

// parsedQuery is the result of parsing an S3 Select SQL expression.
type parsedQuery struct {
	countStar  bool
	columns    []colRef // nil = SELECT *
	tableAlias string
	where      whereNode
	limit      int // 0 = no limit
}

// colRef names a column, optionally prefixed by a table alias.
type colRef struct {
	tableAlias string
	name       string
}

// --- AST nodes for WHERE expressions ---

type whereNode interface {
	evalRow(r selectRow) (bool, error)
}

type andNode struct{ left, right whereNode }
type orNode struct{ left, right whereNode }
type notNode struct{ inner whereNode }

type isNullNode struct {
	val   valNode
	notOp bool
}

type cmpNode struct {
	left, right valNode
	op          string
}

type likeNode struct {
	val     valNode
	pattern string
	notOp   bool
}

func (n *andNode) evalRow(r selectRow) (bool, error) {
	l, err := n.left.evalRow(r)
	if err != nil || !l {
		return false, err
	}
	return n.right.evalRow(r)
}

func (n *orNode) evalRow(r selectRow) (bool, error) {
	l, err := n.left.evalRow(r)
	if err != nil {
		return false, err
	}
	if l {
		return true, nil
	}
	return n.right.evalRow(r)
}

func (n *notNode) evalRow(r selectRow) (bool, error) {
	v, err := n.inner.evalRow(r)
	return !v, err
}

func (n *isNullNode) evalRow(r selectRow) (bool, error) {
	_, isNull := n.val.evalVal(r)
	if n.notOp {
		return !isNull, nil
	}
	return isNull, nil
}

func (n *cmpNode) evalRow(r selectRow) (bool, error) {
	lv, lNull := n.left.evalVal(r)
	rv, rNull := n.right.evalVal(r)
	if lNull || rNull {
		return false, nil
	}
	return compareValues(lv, n.op, rv)
}

func (n *likeNode) evalRow(r selectRow) (bool, error) {
	v, isNull := n.val.evalVal(r)
	if isNull {
		return false, nil
	}
	matched := matchLike(v, n.pattern)
	if n.notOp {
		return !matched, nil
	}
	return matched, nil
}

// matchLike matches s against a SQL LIKE pattern (% = any sequence, _ = one char).
func matchLike(s, pattern string) bool {
	if len(pattern) == 0 {
		return len(s) == 0
	}
	if pattern[0] == '%' {
		rest := pattern[1:]
		if len(rest) == 0 {
			return true
		}
		for i := range len(s) + 1 {
			if matchLike(s[i:], rest) {
				return true
			}
		}
		return false
	}
	if len(s) == 0 {
		return false
	}
	if pattern[0] == '_' || pattern[0] == s[0] {
		return matchLike(s[1:], pattern[1:])
	}
	return false
}

// compareValues compares two string values using op, preferring numeric comparison.
func compareValues(left, op, right string) (bool, error) {
	ln, lerr := strconv.ParseFloat(left, 64)
	rn, rerr := strconv.ParseFloat(right, 64)
	if lerr == nil && rerr == nil {
		switch op {
		case "=":
			return ln == rn, nil
		case "!=", "<>":
			return ln != rn, nil
		case "<":
			return ln < rn, nil
		case "<=":
			return ln <= rn, nil
		case ">":
			return ln > rn, nil
		case ">=":
			return ln >= rn, nil
		}
	}
	switch op {
	case "=":
		return left == right, nil
	case "!=", "<>":
		return left != right, nil
	case "<":
		return left < right, nil
	case "<=":
		return left <= right, nil
	case ">":
		return left > right, nil
	case ">=":
		return left >= right, nil
	}
	return false, fmt.Errorf("unknown operator: %s", op)
}

// --- Value nodes ---

type valNode interface {
	evalVal(r selectRow) (val string, isNull bool)
}

type colValNode struct{ ref colRef }
type strLitNode struct{ val string }
type numLitNode struct{ val float64 }
type nullLitNode struct{}
type castNode struct {
	inner  valNode
	toType string
}

func (n *colValNode) evalVal(r selectRow) (string, bool) {
	v, ok := r.vals[n.ref.name]
	if !ok {
		return "", true
	}
	return v, false
}

func (n *strLitNode) evalVal(_ selectRow) (string, bool) { return n.val, false }

func (n *numLitNode) evalVal(_ selectRow) (string, bool) {
	if n.val == math.Trunc(n.val) && !math.IsInf(n.val, 0) {
		return strconv.FormatInt(int64(n.val), 10), false
	}
	return strconv.FormatFloat(n.val, 'f', -1, 64), false
}

func (n *nullLitNode) evalVal(_ selectRow) (string, bool) { return "", true }

func (n *castNode) evalVal(r selectRow) (string, bool) {
	v, isNull := n.inner.evalVal(r)
	if isNull {
		return "", true
	}
	switch strings.ToUpper(n.toType) {
	case "INT", "INTEGER", "SMALLINT", "TINYINT", "BIGINT":
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return "0", false
		}
		return strconv.FormatInt(int64(f), 10), false
	case "FLOAT", "DOUBLE", "DECIMAL", "NUMERIC", "REAL":
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return "0", false
		}
		return strconv.FormatFloat(f, 'f', -1, 64), false
	case "STRING", "VARCHAR", "CHAR", "TEXT":
		return v, false
	case "BOOL", "BOOLEAN":
		if v == "1" || strings.EqualFold(v, "true") {
			return "true", false
		}
		return "false", false
	}
	return v, false
}

// --- Lexer ---

type sqlTokenKind int

const (
	sqlTokEOF sqlTokenKind = iota
	sqlTokIdent
	sqlTokNumber
	sqlTokString
	sqlTokStar
	sqlTokDot
	sqlTokComma
	sqlTokLParen
	sqlTokRParen
	sqlTokEq
	sqlTokNE
	sqlTokLT
	sqlTokLE
	sqlTokGT
	sqlTokGE
)

type sqlToken struct {
	kind sqlTokenKind
	val  string
}

type sqlLexer struct {
	input  []rune
	pos    int
	peeked *sqlToken
}

func newSQLLexer(input string) *sqlLexer {
	return &sqlLexer{input: []rune(input)}
}

func (l *sqlLexer) next() sqlToken {
	if l.peeked != nil {
		t := *l.peeked
		l.peeked = nil
		return t
	}
	return l.scan()
}

func (l *sqlLexer) peek() sqlToken {
	if l.peeked == nil {
		t := l.scan()
		l.peeked = &t
	}
	return *l.peeked
}

func (l *sqlLexer) skipWS() {
	for l.pos < len(l.input) && unicode.IsSpace(l.input[l.pos]) {
		l.pos++
	}
}

func (l *sqlLexer) scan() sqlToken {
	l.skipWS()
	if l.pos >= len(l.input) {
		return sqlToken{kind: sqlTokEOF}
	}
	ch := l.input[l.pos]
	switch {
	case ch == '\'':
		return l.scanString()
	case ch == '*':
		l.pos++
		return sqlToken{kind: sqlTokStar, val: "*"}
	case ch == '.':
		l.pos++
		return sqlToken{kind: sqlTokDot, val: "."}
	case ch == ',':
		l.pos++
		return sqlToken{kind: sqlTokComma, val: ","}
	case ch == '(':
		l.pos++
		return sqlToken{kind: sqlTokLParen, val: "("}
	case ch == ')':
		l.pos++
		return sqlToken{kind: sqlTokRParen, val: ")"}
	case ch == '=':
		l.pos++
		return sqlToken{kind: sqlTokEq, val: "="}
	case ch == '<':
		l.pos++
		if l.pos < len(l.input) {
			switch l.input[l.pos] {
			case '=':
				l.pos++
				return sqlToken{kind: sqlTokLE, val: "<="}
			case '>':
				l.pos++
				return sqlToken{kind: sqlTokNE, val: "<>"}
			}
		}
		return sqlToken{kind: sqlTokLT, val: "<"}
	case ch == '>':
		l.pos++
		if l.pos < len(l.input) && l.input[l.pos] == '=' {
			l.pos++
			return sqlToken{kind: sqlTokGE, val: ">="}
		}
		return sqlToken{kind: sqlTokGT, val: ">"}
	case ch == '!' && l.pos+1 < len(l.input) && l.input[l.pos+1] == '=':
		l.pos += 2
		return sqlToken{kind: sqlTokNE, val: "!="}
	case unicode.IsDigit(ch) || (ch == '-' && l.pos+1 < len(l.input) && unicode.IsDigit(l.input[l.pos+1])):
		return l.scanNumber()
	case unicode.IsLetter(ch) || ch == '_':
		return l.scanIdent()
	}
	l.pos++
	return sqlToken{kind: sqlTokEOF}
}

func (l *sqlLexer) scanString() sqlToken {
	l.pos++ // skip opening quote
	var sb strings.Builder
	for l.pos < len(l.input) {
		ch := l.input[l.pos]
		if ch == '\'' {
			l.pos++
			if l.pos < len(l.input) && l.input[l.pos] == '\'' {
				sb.WriteRune('\'')
				l.pos++
				continue
			}
			break
		}
		sb.WriteRune(ch)
		l.pos++
	}
	return sqlToken{kind: sqlTokString, val: sb.String()}
}

func (l *sqlLexer) scanNumber() sqlToken {
	start := l.pos
	if l.input[l.pos] == '-' {
		l.pos++
	}
	for l.pos < len(l.input) && (unicode.IsDigit(l.input[l.pos]) || l.input[l.pos] == '.') {
		l.pos++
	}
	if l.pos < len(l.input) && (l.input[l.pos] == 'e' || l.input[l.pos] == 'E') {
		l.pos++
		if l.pos < len(l.input) && (l.input[l.pos] == '+' || l.input[l.pos] == '-') {
			l.pos++
		}
		for l.pos < len(l.input) && unicode.IsDigit(l.input[l.pos]) {
			l.pos++
		}
	}
	return sqlToken{kind: sqlTokNumber, val: string(l.input[start:l.pos])}
}

func (l *sqlLexer) scanIdent() sqlToken {
	start := l.pos
	for l.pos < len(l.input) && (unicode.IsLetter(l.input[l.pos]) || unicode.IsDigit(l.input[l.pos]) || l.input[l.pos] == '_') {
		l.pos++
	}
	return sqlToken{kind: sqlTokIdent, val: string(l.input[start:l.pos])}
}

// --- Parser ---

type sqlParser struct{ lex *sqlLexer }

// parseSQL parses an S3 Select SQL expression into a parsedQuery.
func parseSQL(expression string) (*parsedQuery, error) {
	p := &sqlParser{lex: newSQLLexer(expression)}
	return p.parseQuery()
}

func (p *sqlParser) expectKW(kw string) error {
	t := p.lex.next()
	if t.kind != sqlTokIdent || !strings.EqualFold(t.val, kw) {
		return fmt.Errorf("expected %q, got %q", kw, t.val)
	}
	return nil
}

func (p *sqlParser) parseQuery() (*parsedQuery, error) {
	if err := p.expectKW("SELECT"); err != nil {
		return nil, err
	}

	q := &parsedQuery{}

	cols, countStar, err := p.parseSelectList()
	if err != nil {
		return nil, err
	}
	q.columns = cols
	q.countStar = countStar

	if err := p.expectKW("FROM"); err != nil {
		return nil, err
	}

	t := p.lex.next()
	if t.kind != sqlTokIdent || !strings.EqualFold(t.val, "S3Object") {
		return nil, fmt.Errorf("expected S3Object in FROM clause, got %q", t.val)
	}

	if pk := p.lex.peek(); pk.kind == sqlTokIdent && !isSQLKeyword(pk.val) {
		p.lex.next()
		q.tableAlias = pk.val
	}

	if pk := p.lex.peek(); pk.kind == sqlTokIdent && strings.EqualFold(pk.val, "WHERE") {
		p.lex.next()
		where, err := p.parseOrExpr()
		if err != nil {
			return nil, fmt.Errorf("WHERE clause: %w", err)
		}
		q.where = where
	}

	if pk := p.lex.peek(); pk.kind == sqlTokIdent && strings.EqualFold(pk.val, "LIMIT") {
		p.lex.next()
		nt := p.lex.next()
		if nt.kind != sqlTokNumber {
			return nil, fmt.Errorf("expected integer after LIMIT, got %q", nt.val)
		}
		n, err := strconv.Atoi(nt.val)
		if err != nil {
			return nil, fmt.Errorf("invalid LIMIT value: %w", err)
		}
		q.limit = n
	}

	return q, nil
}

func (p *sqlParser) parseSelectList() ([]colRef, bool, error) {
	pk := p.lex.peek()

	// SELECT *
	if pk.kind == sqlTokStar {
		p.lex.next()
		return nil, false, nil
	}

	// SELECT COUNT(*)
	if pk.kind == sqlTokIdent && strings.EqualFold(pk.val, "COUNT") {
		p.lex.next()
		if t := p.lex.next(); t.kind != sqlTokLParen {
			return nil, false, fmt.Errorf("expected ( after COUNT")
		}
		if t := p.lex.next(); t.kind != sqlTokStar {
			return nil, false, fmt.Errorf("expected * inside COUNT()")
		}
		if t := p.lex.next(); t.kind != sqlTokRParen {
			return nil, false, fmt.Errorf("expected ) after COUNT(*)")
		}
		return nil, true, nil
	}

	// Column list
	var cols []colRef
	for {
		col, err := p.parseColRef()
		if err != nil {
			return nil, false, err
		}
		if pk2 := p.lex.peek(); pk2.kind == sqlTokIdent && strings.EqualFold(pk2.val, "AS") {
			p.lex.next() // AS
			p.lex.next() // alias (ignored)
		}
		cols = append(cols, col)
		if p.lex.peek().kind != sqlTokComma {
			break
		}
		p.lex.next()
	}
	return cols, false, nil
}

func (p *sqlParser) parseColRef() (colRef, error) {
	t := p.lex.next()
	if t.kind != sqlTokIdent {
		return colRef{}, fmt.Errorf("expected column name, got %q (kind=%d)", t.val, t.kind)
	}
	if p.lex.peek().kind == sqlTokDot {
		p.lex.next() // consume dot
		col := p.lex.next()
		if col.kind != sqlTokIdent {
			return colRef{}, fmt.Errorf("expected column name after '.', got %q", col.val)
		}
		return colRef{tableAlias: t.val, name: col.val}, nil
	}
	return colRef{name: t.val}, nil
}

func (p *sqlParser) parseOrExpr() (whereNode, error) {
	left, err := p.parseAndExpr()
	if err != nil {
		return nil, err
	}
	for {
		if pk := p.lex.peek(); pk.kind != sqlTokIdent || !strings.EqualFold(pk.val, "OR") {
			break
		}
		p.lex.next()
		right, err := p.parseAndExpr()
		if err != nil {
			return nil, err
		}
		left = &orNode{left: left, right: right}
	}
	return left, nil
}

func (p *sqlParser) parseAndExpr() (whereNode, error) {
	left, err := p.parseNotExpr()
	if err != nil {
		return nil, err
	}
	for {
		if pk := p.lex.peek(); pk.kind != sqlTokIdent || !strings.EqualFold(pk.val, "AND") {
			break
		}
		p.lex.next()
		right, err := p.parseNotExpr()
		if err != nil {
			return nil, err
		}
		left = &andNode{left: left, right: right}
	}
	return left, nil
}

func (p *sqlParser) parseNotExpr() (whereNode, error) {
	if pk := p.lex.peek(); pk.kind == sqlTokIdent && strings.EqualFold(pk.val, "NOT") {
		p.lex.next()
		inner, err := p.parseNotExpr()
		if err != nil {
			return nil, err
		}
		return &notNode{inner: inner}, nil
	}
	return p.parsePrimary()
}

func (p *sqlParser) parsePrimary() (whereNode, error) {
	if pk := p.lex.peek(); pk.kind == sqlTokLParen {
		p.lex.next()
		inner, err := p.parseOrExpr()
		if err != nil {
			return nil, err
		}
		if t := p.lex.next(); t.kind != sqlTokRParen {
			return nil, fmt.Errorf("expected ), got %q", t.val)
		}
		return inner, nil
	}

	left, err := p.parseValExpr()
	if err != nil {
		return nil, err
	}

	pk := p.lex.peek()

	// IS [NOT] NULL
	if pk.kind == sqlTokIdent && strings.EqualFold(pk.val, "IS") {
		p.lex.next()
		notOp := false
		if pk2 := p.lex.peek(); pk2.kind == sqlTokIdent && strings.EqualFold(pk2.val, "NOT") {
			p.lex.next()
			notOp = true
		}
		if err := p.expectKW("NULL"); err != nil {
			return nil, fmt.Errorf("IS [NOT] NULL: %w", err)
		}
		return &isNullNode{val: left, notOp: notOp}, nil
	}

	// val [NOT] LIKE pattern
	notLike := false
	if pk.kind == sqlTokIdent && strings.EqualFold(pk.val, "NOT") {
		p.lex.next()
		notLike = true
		pk = p.lex.peek()
	}
	if pk.kind == sqlTokIdent && strings.EqualFold(pk.val, "LIKE") {
		p.lex.next()
		t := p.lex.next()
		if t.kind != sqlTokString {
			return nil, fmt.Errorf("expected string pattern after LIKE, got %q", t.val)
		}
		return &likeNode{val: left, pattern: t.val, notOp: notLike}, nil
	}
	if notLike {
		return nil, fmt.Errorf("expected LIKE after NOT, got %q", pk.val)
	}

	// Comparison operator
	op, err := p.parseOp()
	if err != nil {
		return nil, err
	}
	right, err := p.parseValExpr()
	if err != nil {
		return nil, err
	}
	return &cmpNode{left: left, op: op, right: right}, nil
}

func (p *sqlParser) parseOp() (string, error) {
	t := p.lex.next()
	switch t.kind {
	case sqlTokEq:
		return "=", nil
	case sqlTokNE:
		return t.val, nil
	case sqlTokLT:
		return "<", nil
	case sqlTokLE:
		return "<=", nil
	case sqlTokGT:
		return ">", nil
	case sqlTokGE:
		return ">=", nil
	}
	return "", fmt.Errorf("expected comparison operator, got %q", t.val)
}

func (p *sqlParser) parseValExpr() (valNode, error) {
	pk := p.lex.peek()

	if pk.kind == sqlTokIdent && strings.EqualFold(pk.val, "CAST") {
		p.lex.next()
		if t := p.lex.next(); t.kind != sqlTokLParen {
			return nil, fmt.Errorf("expected ( after CAST")
		}
		inner, err := p.parseValExpr()
		if err != nil {
			return nil, err
		}
		if err := p.expectKW("AS"); err != nil {
			return nil, fmt.Errorf("CAST: %w", err)
		}
		tn := p.lex.next()
		if tn.kind != sqlTokIdent {
			return nil, fmt.Errorf("expected type name in CAST, got %q", tn.val)
		}
		if t := p.lex.next(); t.kind != sqlTokRParen {
			return nil, fmt.Errorf("expected ) after CAST type, got %q", t.val)
		}
		return &castNode{inner: inner, toType: tn.val}, nil
	}

	if pk.kind == sqlTokString {
		p.lex.next()
		return &strLitNode{val: pk.val}, nil
	}

	if pk.kind == sqlTokNumber {
		p.lex.next()
		f, err := strconv.ParseFloat(pk.val, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid numeric literal %q: %w", pk.val, err)
		}
		return &numLitNode{val: f}, nil
	}

	if pk.kind == sqlTokIdent {
		switch strings.ToUpper(pk.val) {
		case "NULL":
			p.lex.next()
			return &nullLitNode{}, nil
		case "TRUE":
			p.lex.next()
			return &strLitNode{val: "true"}, nil
		case "FALSE":
			p.lex.next()
			return &strLitNode{val: "false"}, nil
		}
		// column reference
		p.lex.next()
		if p.lex.peek().kind == sqlTokDot {
			p.lex.next()
			col := p.lex.next()
			if col.kind != sqlTokIdent {
				return nil, fmt.Errorf("expected column name after '.', got %q", col.val)
			}
			return &colValNode{ref: colRef{tableAlias: pk.val, name: col.val}}, nil
		}
		return &colValNode{ref: colRef{name: pk.val}}, nil
	}

	return nil, fmt.Errorf("unexpected token %q in value expression", pk.val)
}

var sqlKeywords = map[string]struct{}{
	"SELECT": {}, "FROM": {}, "WHERE": {}, "AND": {}, "OR": {}, "NOT": {},
	"IS": {}, "NULL": {}, "LIKE": {}, "LIMIT": {}, "AS": {}, "CAST": {},
	"TRUE": {}, "FALSE": {}, "BETWEEN": {}, "IN": {}, "ORDER": {}, "BY": {},
	"GROUP": {}, "HAVING": {}, "DISTINCT": {}, "JOIN": {}, "ON": {},
	"UNION": {}, "COUNT": {},
}

func isSQLKeyword(s string) bool {
	_, ok := sqlKeywords[strings.ToUpper(s)]
	return ok
}

// --- Query execution ---

// execute applies the query to rows and returns the result set.
// For COUNT(*) it returns a single record {"_1": "<count>"}.
func (q *parsedQuery) execute(rows []selectRow) ([]selectRow, error) {
	if q.countStar {
		count := 0
		for _, r := range rows {
			if q.where == nil {
				count++
				continue
			}
			ok, err := q.where.evalRow(r)
			if err != nil {
				return nil, err
			}
			if ok {
				count++
			}
		}
		out := newSelectRow([]string{"_1"})
		out.vals["_1"] = strconv.Itoa(count)
		return []selectRow{out}, nil
	}

	var result []selectRow
	for _, r := range rows {
		if q.where != nil {
			ok, err := q.where.evalRow(r)
			if err != nil {
				return nil, err
			}
			if !ok {
				continue
			}
		}
		result = append(result, q.project(r))
		if q.limit > 0 && len(result) >= q.limit {
			break
		}
	}
	return result, nil
}

// project applies the SELECT column list to produce an output row.
func (q *parsedQuery) project(r selectRow) selectRow {
	if q.columns == nil {
		out := newSelectRow(r.headers)
		for k, v := range r.vals {
			out.vals[k] = v
		}
		return out
	}
	outHeaders := make([]string, 0, len(q.columns))
	for _, c := range q.columns {
		outHeaders = append(outHeaders, c.name)
	}
	out := newSelectRow(outHeaders)
	for _, c := range q.columns {
		if v, ok := r.vals[c.name]; ok {
			out.vals[c.name] = v
		}
	}
	return out
}
