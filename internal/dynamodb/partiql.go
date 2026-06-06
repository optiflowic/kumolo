package dynamodb

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

type pqStmtKind int

const (
	pqSelect pqStmtKind = iota
	pqInsert
	pqUpdate
	pqDelete
)

// pqStmt is the parsed result of a single PartiQL statement.
type pqStmt struct {
	kind      pqStmtKind
	tableName string
	// SELECT / UPDATE / DELETE: AND-joined WHERE conditions
	where []pqCond
	// INSERT: complete item map (attr → DynamoDB typed value)
	item map[string]any
	// UPDATE: SET assignments
	sets []pqSet
	// SELECT: LIMIT from the statement itself (separate from API-level Limit)
	stmtLimit *int
}

// pqCond represents one condition in a WHERE clause.
type pqCond struct {
	attr string
	// "=", "<>", "<", "<=", ">", ">="  — simple comparison
	// "BETWEEN"                          — val=lo, val2=hi
	// "IN"                               — vals holds the value list
	op   string
	val  map[string]any
	val2 map[string]any   // BETWEEN upper bound
	vals []map[string]any // IN list
}

// pqSet is one SET assignment in an UPDATE statement.
type pqSet struct {
	attr string
	val  map[string]any
}

// ---- tokenizer ----

type pqTokKind int

const (
	pqTokEOF    pqTokKind = iota
	pqTokIdent            // bare identifier or unquoted keyword
	pqTokStr              // single-quoted literal or double/backtick-quoted identifier
	pqTokNum              // numeric literal (integer or decimal, may be negative)
	pqTokStar             // *
	pqTokComma            // ,
	pqTokLParen           // (
	pqTokRParen           // )
	pqTokLBrace           // {
	pqTokRBrace           // }
	pqTokLBrack           // [
	pqTokRBrack           // ]
	pqTokEq               // =
	pqTokLt               // <
	pqTokGt               // >
	pqTokLe               // <=
	pqTokGe               // >=
	pqTokNe               // <> or !=
	pqTokColon            // :
	pqTokQ                // ?
	pqTokDot              // .
)

type pqTok struct {
	kind   pqTokKind
	val    string // raw token text
	strVal string // unquoted value (pqTokStr only)
}

func pqTokenize(s string) ([]pqTok, error) {
	var toks []pqTok
	i := 0
	for i < len(s) {
		c := s[i]
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' {
			i++
			continue
		}
		switch c {
		case '*':
			toks = append(toks, pqTok{kind: pqTokStar, val: "*"})
			i++
		case ',':
			toks = append(toks, pqTok{kind: pqTokComma, val: ","})
			i++
		case '(':
			toks = append(toks, pqTok{kind: pqTokLParen, val: "("})
			i++
		case ')':
			toks = append(toks, pqTok{kind: pqTokRParen, val: ")"})
			i++
		case '{':
			toks = append(toks, pqTok{kind: pqTokLBrace, val: "{"})
			i++
		case '}':
			toks = append(toks, pqTok{kind: pqTokRBrace, val: "}"})
			i++
		case '[':
			toks = append(toks, pqTok{kind: pqTokLBrack, val: "["})
			i++
		case ']':
			toks = append(toks, pqTok{kind: pqTokRBrack, val: "]"})
			i++
		case ':':
			toks = append(toks, pqTok{kind: pqTokColon, val: ":"})
			i++
		case '?':
			toks = append(toks, pqTok{kind: pqTokQ, val: "?"})
			i++
		case '.':
			toks = append(toks, pqTok{kind: pqTokDot, val: "."})
			i++
		case '=':
			toks = append(toks, pqTok{kind: pqTokEq, val: "="})
			i++
		case '<':
			if i+1 < len(s) && s[i+1] == '=' {
				toks = append(toks, pqTok{kind: pqTokLe, val: "<="})
				i += 2
			} else if i+1 < len(s) && s[i+1] == '>' {
				toks = append(toks, pqTok{kind: pqTokNe, val: "<>"})
				i += 2
			} else {
				toks = append(toks, pqTok{kind: pqTokLt, val: "<"})
				i++
			}
		case '>':
			if i+1 < len(s) && s[i+1] == '=' {
				toks = append(toks, pqTok{kind: pqTokGe, val: ">="})
				i += 2
			} else {
				toks = append(toks, pqTok{kind: pqTokGt, val: ">"})
				i++
			}
		case '!':
			if i+1 < len(s) && s[i+1] == '=' {
				toks = append(toks, pqTok{kind: pqTokNe, val: "!="})
				i += 2
			} else {
				return nil, fmt.Errorf("unexpected character '!' at position %d", i)
			}
		case '\'':
			// single-quoted string literal
			j := i + 1
			for j < len(s) && s[j] != '\'' {
				j++
			}
			if j >= len(s) {
				return nil, fmt.Errorf("unterminated string literal at position %d", i)
			}
			toks = append(toks, pqTok{kind: pqTokStr, val: s[i : j+1], strVal: s[i+1 : j]})
			i = j + 1
		case '"':
			// double-quoted identifier
			j := i + 1
			for j < len(s) && s[j] != '"' {
				j++
			}
			if j >= len(s) {
				return nil, fmt.Errorf("unterminated quoted identifier at position %d", i)
			}
			toks = append(toks, pqTok{kind: pqTokStr, val: s[i : j+1], strVal: s[i+1 : j]})
			i = j + 1
		case '`':
			// backtick-quoted identifier
			j := i + 1
			for j < len(s) && s[j] != '`' {
				j++
			}
			if j >= len(s) {
				return nil, fmt.Errorf("unterminated backtick identifier at position %d", i)
			}
			toks = append(toks, pqTok{kind: pqTokStr, val: s[i : j+1], strVal: s[i+1 : j]})
			i = j + 1
		default:
			if unicode.IsDigit(rune(c)) {
				j := i
				for j < len(s) && (unicode.IsDigit(rune(s[j])) || s[j] == '.') {
					j++
				}
				toks = append(toks, pqTok{kind: pqTokNum, val: s[i:j]})
				i = j
			} else if c == '-' && i+1 < len(s) && unicode.IsDigit(rune(s[i+1])) {
				// negative number literal
				j := i + 1
				for j < len(s) && (unicode.IsDigit(rune(s[j])) || s[j] == '.') {
					j++
				}
				toks = append(toks, pqTok{kind: pqTokNum, val: s[i:j]})
				i = j
			} else if unicode.IsLetter(rune(c)) || c == '_' {
				j := i
				for j < len(s) && (unicode.IsLetter(rune(s[j])) || unicode.IsDigit(rune(s[j])) || s[j] == '_') {
					j++
				}
				toks = append(toks, pqTok{kind: pqTokIdent, val: s[i:j]})
				i = j
			} else {
				return nil, fmt.Errorf("unexpected character %q at position %d", c, i)
			}
		}
	}
	toks = append(toks, pqTok{kind: pqTokEOF})
	return toks, nil
}

// ---- parser ----

type pqParser struct {
	toks     []pqTok
	pos      int
	params   []map[string]any
	paramIdx int
}

// parsePartiQL parses a single PartiQL statement, substituting params for ? placeholders.
func parsePartiQL(stmt string, params []map[string]any) (*pqStmt, error) {
	toks, err := pqTokenize(stmt)
	if err != nil {
		return nil, err
	}
	p := &pqParser{toks: toks, params: params}
	return p.parseStatement()
}

func (p *pqParser) peek() pqTok {
	if p.pos >= len(p.toks) {
		return pqTok{
			kind: pqTokEOF,
		} // unreachable: EOF token is always appended and consume bounds-checks pos
	}
	return p.toks[p.pos]
}

func (p *pqParser) consume() pqTok {
	t := p.peek()
	if p.pos < len(p.toks) {
		p.pos++
	}
	return t
}

func (p *pqParser) expectIdent(kw string) error {
	t := p.peek()
	if t.kind != pqTokIdent || !strings.EqualFold(t.val, kw) {
		return fmt.Errorf("expected %q, got %q", kw, t.val)
	}
	p.pos++
	return nil
}

func (p *pqParser) expectPunct(kind pqTokKind, desc string) error {
	t := p.peek()
	if t.kind != kind {
		return fmt.Errorf("expected %s, got %q", desc, t.val)
	}
	p.pos++
	return nil
}

func (p *pqParser) nextParam() (map[string]any, error) {
	if p.paramIdx >= len(p.params) {
		return nil, fmt.Errorf(
			"too few parameters: statement needs parameter %d but only %d provided",
			p.paramIdx+1, len(p.params),
		)
	}
	v := p.params[p.paramIdx]
	p.paramIdx++
	return v, nil
}

func (p *pqParser) parseStatement() (*pqStmt, error) {
	if p.peek().kind != pqTokIdent {
		return nil, fmt.Errorf(
			"expected SELECT, INSERT, UPDATE, or DELETE; got %q", p.peek().val,
		)
	}
	switch strings.ToUpper(p.peek().val) {
	case "SELECT":
		return p.parseSelect()
	case "INSERT":
		return p.parseInsert()
	case "UPDATE":
		return p.parseUpdate()
	case "DELETE":
		return p.parseDelete()
	default:
		return nil, fmt.Errorf("unsupported PartiQL statement type %q", p.peek().val)
	}
}

func (p *pqParser) parseSelect() (*pqStmt, error) {
	if err := p.expectIdent("SELECT"); err != nil {
		return nil, err // unreachable: parseStatement pre-checks keyword before calling parseSelect
	}
	// skip projection clause (* or column list) until FROM
	for p.peek().kind != pqTokEOF && !strings.EqualFold(p.peek().val, "FROM") {
		p.consume()
	}
	if err := p.expectIdent("FROM"); err != nil {
		return nil, err
	}
	tableName, err := p.parseName()
	if err != nil {
		return nil, err
	}
	stmt := &pqStmt{kind: pqSelect, tableName: tableName}
	if p.peek().kind == pqTokIdent && strings.EqualFold(p.peek().val, "WHERE") {
		p.consume()
		stmt.where, err = p.parseConditions()
		if err != nil {
			return nil, err
		}
	}
	// skip ORDER BY (not used for execution strategy)
	if p.peek().kind == pqTokIdent && strings.EqualFold(p.peek().val, "ORDER") {
		p.consume()
		if err := p.expectIdent("BY"); err != nil {
			return nil, err
		}
		if _, err := p.parseName(); err != nil {
			return nil, err
		}
		if p.peek().kind == pqTokIdent &&
			(strings.EqualFold(p.peek().val, "ASC") || strings.EqualFold(p.peek().val, "DESC")) {
			p.consume()
		}
	}
	if p.peek().kind == pqTokIdent && strings.EqualFold(p.peek().val, "LIMIT") {
		p.consume()
		t := p.peek()
		if t.kind != pqTokNum {
			return nil, fmt.Errorf("expected number after LIMIT, got %q", t.val)
		}
		p.consume()
		n, err := strconv.Atoi(t.val)
		if err != nil {
			return nil, fmt.Errorf("invalid LIMIT %q: %v", t.val, err)
		}
		stmt.stmtLimit = &n
	}
	return stmt, nil
}

func (p *pqParser) parseInsert() (*pqStmt, error) {
	if err := p.expectIdent("INSERT"); err != nil {
		return nil, err // unreachable: parseStatement pre-checks keyword before calling parseInsert
	}
	if err := p.expectIdent("INTO"); err != nil {
		return nil, err
	}
	tableName, err := p.parseName()
	if err != nil {
		return nil, err
	}
	if err := p.expectIdent("VALUE"); err != nil {
		return nil, err
	}
	item, err := p.parseDocMap()
	if err != nil {
		return nil, err
	}
	return &pqStmt{kind: pqInsert, tableName: tableName, item: item}, nil
}

func (p *pqParser) parseUpdate() (*pqStmt, error) {
	if err := p.expectIdent("UPDATE"); err != nil {
		return nil, err // unreachable: parseStatement pre-checks keyword before calling parseUpdate
	}
	tableName, err := p.parseName()
	if err != nil {
		return nil, err
	}
	if err := p.expectIdent("SET"); err != nil {
		return nil, err
	}
	sets, err := p.parseSetList()
	if err != nil {
		return nil, err
	}
	if p.peek().kind != pqTokIdent || !strings.EqualFold(p.peek().val, "WHERE") {
		return nil, fmt.Errorf("UPDATE statement requires a WHERE clause")
	}
	p.consume()
	stmt := &pqStmt{kind: pqUpdate, tableName: tableName, sets: sets}
	stmt.where, err = p.parseConditions()
	if err != nil {
		return nil, err
	}
	return stmt, nil
}

func (p *pqParser) parseDelete() (*pqStmt, error) {
	if err := p.expectIdent("DELETE"); err != nil {
		return nil, err // unreachable: parseStatement pre-checks keyword before calling parseDelete
	}
	if err := p.expectIdent("FROM"); err != nil {
		return nil, err
	}
	tableName, err := p.parseName()
	if err != nil {
		return nil, err
	}
	if p.peek().kind != pqTokIdent || !strings.EqualFold(p.peek().val, "WHERE") {
		return nil, fmt.Errorf("DELETE statement requires a WHERE clause")
	}
	p.consume()
	stmt := &pqStmt{kind: pqDelete, tableName: tableName}
	stmt.where, err = p.parseConditions()
	if err != nil {
		return nil, err
	}
	return stmt, nil
}

// parseName parses a table name or attribute name, quoted or bare.
func (p *pqParser) parseName() (string, error) {
	t := p.consume()
	switch t.kind {
	case pqTokStr:
		return t.strVal, nil
	case pqTokIdent:
		return t.val, nil
	default:
		return "", fmt.Errorf("expected identifier or quoted name, got %q", t.val)
	}
}

// parseConditions parses a list of AND-joined conditions.
func (p *pqParser) parseConditions() ([]pqCond, error) {
	cond, err := p.parseOneCondition()
	if err != nil {
		return nil, err
	}
	conds := []pqCond{cond}
	for p.peek().kind == pqTokIdent && strings.EqualFold(p.peek().val, "AND") {
		p.consume()
		cond, err = p.parseOneCondition()
		if err != nil {
			return nil, err
		}
		conds = append(conds, cond)
	}
	return conds, nil
}

// parseOneCondition parses a single comparison condition.
func (p *pqParser) parseOneCondition() (pqCond, error) {
	attr, err := p.parseName()
	if err != nil {
		return pqCond{}, err
	}
	t := p.peek()
	switch t.kind {
	case pqTokEq, pqTokNe, pqTokLt, pqTokLe, pqTokGt, pqTokGe:
		op := t.val
		p.consume()
		val, err := p.parseValue()
		if err != nil {
			return pqCond{}, err
		}
		return pqCond{attr: attr, op: op, val: val}, nil
	case pqTokIdent:
		switch strings.ToUpper(t.val) {
		case "BETWEEN":
			p.consume()
			lo, err := p.parseValue()
			if err != nil {
				return pqCond{}, err
			}
			if err := p.expectIdent("AND"); err != nil {
				return pqCond{}, fmt.Errorf("BETWEEN requires AND: %v", err)
			}
			hi, err := p.parseValue()
			if err != nil {
				return pqCond{}, err
			}
			return pqCond{attr: attr, op: "BETWEEN", val: lo, val2: hi}, nil
		case "IN":
			p.consume()
			if err := p.expectPunct(pqTokLParen, "'('"); err != nil {
				return pqCond{}, err
			}
			var vals []map[string]any
			for p.peek().kind != pqTokRParen && p.peek().kind != pqTokEOF {
				v, err := p.parseValue()
				if err != nil {
					return pqCond{}, err
				}
				vals = append(vals, v)
				if p.peek().kind == pqTokComma {
					p.consume()
				}
			}
			if err := p.expectPunct(pqTokRParen, "')'"); err != nil {
				return pqCond{}, err
			}
			return pqCond{attr: attr, op: "IN", vals: vals}, nil
		}
	}
	return pqCond{}, fmt.Errorf("expected comparison operator, BETWEEN, or IN; got %q", t.val)
}

// parseValue parses a single PartiQL value: ?, string literal, number, boolean, NULL,
// or a nested document/list.
func (p *pqParser) parseValue() (map[string]any, error) {
	t := p.peek()
	switch t.kind {
	case pqTokQ:
		p.consume()
		return p.nextParam()
	case pqTokNum:
		p.consume()
		return map[string]any{"N": t.val}, nil
	case pqTokStr:
		p.consume()
		return map[string]any{"S": t.strVal}, nil
	case pqTokIdent:
		switch strings.ToUpper(t.val) {
		case "TRUE":
			p.consume()
			return map[string]any{"BOOL": true}, nil
		case "FALSE":
			p.consume()
			return map[string]any{"BOOL": false}, nil
		case "NULL", "MISSING":
			p.consume()
			return map[string]any{"NULL": true}, nil
		}
		return nil, fmt.Errorf("expected value, got identifier %q", t.val)
	case pqTokLBrace:
		m, err := p.parseDocMap()
		if err != nil {
			return nil, err
		}
		return map[string]any{"M": m}, nil
	case pqTokLBrack:
		items, err := p.parseList()
		if err != nil {
			return nil, err
		}
		return map[string]any{"L": items}, nil
	default:
		return nil, fmt.Errorf("expected value, got %q", t.val)
	}
}

// parseDocMap parses {key: val, ...} and returns the raw map (no M wrapper).
// Used for INSERT VALUE {...}.
func (p *pqParser) parseDocMap() (map[string]any, error) {
	if err := p.expectPunct(pqTokLBrace, "'{'"); err != nil {
		return nil, err
	}
	m := map[string]any{}
	for p.peek().kind != pqTokRBrace && p.peek().kind != pqTokEOF {
		key, err := p.parseName()
		if err != nil {
			return nil, fmt.Errorf("expected map key: %v", err)
		}
		if err := p.expectPunct(pqTokColon, "':'"); err != nil {
			return nil, err
		}
		val, err := p.parseValue()
		if err != nil {
			return nil, err
		}
		m[key] = val
		if p.peek().kind == pqTokComma {
			p.consume()
		}
	}
	if err := p.expectPunct(pqTokRBrace, "'}'"); err != nil {
		return nil, err
	}
	return m, nil
}

// parseList parses [val, val, ...] and returns []any (each element is a DynamoDB typed value).
func (p *pqParser) parseList() ([]any, error) {
	if err := p.expectPunct(pqTokLBrack, "'['"); err != nil {
		return nil, err // unreachable: parseValue only calls parseList after confirming pqTokLBrack
	}
	var items []any
	for p.peek().kind != pqTokRBrack && p.peek().kind != pqTokEOF {
		val, err := p.parseValue()
		if err != nil {
			return nil, err
		}
		items = append(items, val)
		if p.peek().kind == pqTokComma {
			p.consume()
		}
	}
	if err := p.expectPunct(pqTokRBrack, "']'"); err != nil {
		return nil, err
	}
	return items, nil
}

// parseSetList parses "col = val [, col2 = val2 ...]" for UPDATE SET.
func (p *pqParser) parseSetList() ([]pqSet, error) {
	s, err := p.parseOneSet()
	if err != nil {
		return nil, err
	}
	sets := []pqSet{s}
	for p.peek().kind == pqTokComma {
		p.consume()
		s, err = p.parseOneSet()
		if err != nil {
			return nil, err
		}
		sets = append(sets, s)
	}
	return sets, nil
}

func (p *pqParser) parseOneSet() (pqSet, error) {
	attr, err := p.parseName()
	if err != nil {
		return pqSet{}, err
	}
	if err := p.expectPunct(pqTokEq, "'='"); err != nil {
		return pqSet{}, err
	}
	val, err := p.parseValue()
	if err != nil {
		return pqSet{}, err
	}
	return pqSet{attr: attr, val: val}, nil
}

// ---- helpers used by handlers ----

// extractExactKey builds a DynamoDB primary key map from WHERE equality conditions.
// Returns an error if any required key attribute has no equality condition.
func extractExactKey(where []pqCond, meta TableMetadata) (map[string]any, error) {
	key := make(map[string]any, len(meta.KeySchema))
	for _, k := range meta.KeySchema {
		found := false
		for _, c := range where {
			if c.attr == k.AttributeName && c.op == "=" {
				key[k.AttributeName] = c.val
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf(
				"%w: WHERE clause must specify equality condition for key attribute %q",
				ErrValidationException, k.AttributeName,
			)
		}
	}
	return key, nil
}

// pqCondToSortKey converts a pqCond into a SortKeyCondition.
// Returns nil for operators not mappable to SortKeyCondition.
func pqCondToSortKey(c *pqCond) *SortKeyCondition {
	switch c.op {
	case "=":
		return &SortKeyCondition{Name: c.attr, Operator: OpEQ, Value: c.val}
	case "<":
		return &SortKeyCondition{Name: c.attr, Operator: OpLT, Value: c.val}
	case "<=":
		return &SortKeyCondition{Name: c.attr, Operator: OpLTE, Value: c.val}
	case ">":
		return &SortKeyCondition{Name: c.attr, Operator: OpGT, Value: c.val}
	case ">=":
		return &SortKeyCondition{Name: c.attr, Operator: OpGTE, Value: c.val}
	case "BETWEEN":
		return &SortKeyCondition{Name: c.attr, Operator: OpBETWEEN, Value: c.val, Value2: c.val2}
	default:
		return nil
	}
}

// pqCondsToFilterExpr converts remaining (non-key) WHERE conditions into a
// DynamoDB filter expression suitable for applyFilterExpression.
func pqCondsToFilterExpr(
	conds []pqCond,
) (filterExpr string, names map[string]string, values map[string]any) {
	if len(conds) == 0 {
		return "", nil, nil
	}
	names = make(map[string]string, len(conds))
	values = make(map[string]any, len(conds)*2)
	parts := make([]string, 0, len(conds))
	for i, c := range conds {
		nameRef := fmt.Sprintf("#pqf%d", i)
		names[nameRef] = c.attr
		switch c.op {
		case "=", "<>", "!=", "<", "<=", ">", ">=":
			op := c.op
			if op == "!=" {
				op = "<>"
			}
			valRef := fmt.Sprintf(":pqf%d", i)
			values[valRef] = c.val
			parts = append(parts, nameRef+" "+op+" "+valRef)
		case "BETWEEN":
			loRef := fmt.Sprintf(":pqf%dlo", i)
			hiRef := fmt.Sprintf(":pqf%dhi", i)
			values[loRef] = c.val
			values[hiRef] = c.val2
			parts = append(parts, nameRef+" BETWEEN "+loRef+" AND "+hiRef)
		case "IN":
			valRefs := make([]string, len(c.vals))
			for j, v := range c.vals {
				vRef := fmt.Sprintf(":pqf%d_%d", i, j)
				values[vRef] = v
				valRefs[j] = vRef
			}
			parts = append(parts, nameRef+" IN ("+strings.Join(valRefs, ", ")+")")
		}
	}
	return strings.Join(parts, " AND "), names, values
}

// pqMinLimit returns the smaller of two *int limits (nil means unlimited).
func pqMinLimit(a, b *int) *int {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	if *a <= *b {
		return a
	}
	return b
}
