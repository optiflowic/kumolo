package dynamodb

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// validateSelectCommon validates Select+ProjectionExpression for Scan/Query; caller handles ALL_PROJECTED_ATTRIBUTES.
func validateSelectCommon(w http.ResponseWriter, selectVal, projExpr string) bool {
	switch selectVal {
	case "", "ALL_PROJECTED_ATTRIBUTES":
		// "" = default (valid); ALL_PROJECTED_ATTRIBUTES = caller-specific logic
	case "ALL_ATTRIBUTES":
		if projExpr != "" {
			writeError(w, http.StatusBadRequest,
				"com.amazonaws.dynamodb.v20120810#ValidationException",
				"Select type ALL_ATTRIBUTES is not allowed with a ProjectionExpression")
			return false
		}
	case "COUNT":
		if projExpr != "" {
			writeError(w, http.StatusBadRequest,
				"com.amazonaws.dynamodb.v20120810#ValidationException",
				"Select type COUNT is not allowed with a ProjectionExpression")
			return false
		}
	case "SPECIFIC_ATTRIBUTES":
		if projExpr == "" {
			writeError(w, http.StatusBadRequest,
				"com.amazonaws.dynamodb.v20120810#ValidationException",
				"Select type SPECIFIC_ATTRIBUTES is not compatible with the operation specified.")
			return false
		}
	default:
		writeError(w, http.StatusBadRequest,
			"com.amazonaws.dynamodb.v20120810#ValidationException",
			fmt.Sprintf(
				"Value '%s' at 'select' failed to satisfy constraint: Member must satisfy enum value set: [ALL_ATTRIBUTES, ALL_PROJECTED_ATTRIBUTES, SPECIFIC_ATTRIBUTES, COUNT]",
				selectVal,
			))
		return false
	}
	return true
}

// resolveAttrName resolves an expression attribute name reference.
func resolveAttrName(ref string, attrNames map[string]string) (string, error) {
	if !strings.HasPrefix(ref, "#") {
		return ref, nil
	}
	actual, ok := attrNames[ref]
	if !ok {
		return "", fmt.Errorf("ExpressionAttributeNames missing %q", ref)
	}
	return actual, nil
}

// parseUpdatePath parses an UpdateExpression path token (e.g. "#a.#b[0].c") into
// a sequence of projSegments with all #nameRef placeholders resolved.
func parseUpdatePath(token string, attrNames map[string]string) ([]projSegment, error) {
	var segs []projSegment
	dotParts := strings.Split(token, ".")
	for _, part := range dotParts {
		part = strings.TrimSpace(part)
		if part == "" {
			return nil, fmt.Errorf("invalid path: %q", token)
		}
		lb := strings.Index(part, "[")
		if lb == 0 {
			return nil, fmt.Errorf("invalid path %q: attribute name expected before '['", token)
		}
		attrToken := part
		if lb > 0 {
			attrToken = part[:lb]
		}
		name, err := resolveAttrName(attrToken, attrNames)
		if err != nil {
			return nil, err
		}
		segs = append(segs, projSegment{attr: name})
		if lb == -1 {
			continue
		}
		rest := part[lb:]
		for len(rest) > 0 {
			if rest[0] != '[' {
				return nil, fmt.Errorf("invalid path %q: unexpected %q", token, rest)
			}
			rb := strings.Index(rest, "]")
			if rb == -1 {
				return nil, fmt.Errorf("invalid path %q: missing ']'", token)
			}
			idxStr := rest[1:rb]
			n, err := strconv.Atoi(idxStr)
			if err != nil ||
				n < 0 { // negative index is syntactically valid but semantically rejected
				return nil, fmt.Errorf("invalid list index %q in path %q", idxStr, token)
			}
			segs = append(segs, projSegment{attr: "", index: n})
			rest = rest[rb+1:]
		}
	}
	if len(segs)-1 > 32 {
		return nil, fmt.Errorf("nesting levels have exceeded supported limits")
	}
	return segs, nil
}

// addOp is a sentinel stored in the updates map for an ADD clause operation.
type addOp struct{ val any }

// deleteOp is a sentinel stored in the updates map for a DELETE clause operation.
type deleteOp struct{ val any }

// nestedSetOp sets val at a multi-segment document path (e.g. SET meta.count = :v).
// The segs contain already-resolved attribute names (no #nameRef placeholders).
type nestedSetOp struct {
	segs []projSegment
	val  any // resolved value or ifNotExistsOp / listAppendOp sentinel
}

// nestedRemoveOp removes the attribute at a multi-segment document path (e.g. REMOVE meta.count).
type nestedRemoveOp struct {
	segs []projSegment
}

// setOperand evaluates a SET-clause operand against the current item at apply time.
type setOperand interface {
	resolve(item map[string]any) any
}

// setLiteral is a literal ExpressionAttributeValue.
type setLiteral struct{ val any }

func (o setLiteral) resolve(_ map[string]any) any { return o.val }

// setAttrRef resolves an attribute (or nested path) from the current item.
// segs contains already-resolved attribute names (no #nameRef placeholders).
type setAttrRef struct{ segs []projSegment }

func (o setAttrRef) resolve(item map[string]any) any { return resolveUpdatePath(item, o.segs) }

// ifNotExistsOp: SET target = if_not_exists(path, operand).
// Per AWS spec the first argument must be a path (not a value placeholder).
// Implements setOperand so it can appear as an argument to list_append.
type ifNotExistsOp struct {
	segs    []projSegment // already-resolved path segments
	operand setOperand    // value to use when path is absent
}

func (o ifNotExistsOp) resolve(item map[string]any) any {
	if v := resolveUpdatePath(item, o.segs); v != nil {
		return v
	}
	return o.operand.resolve(item)
}

// resolveUpdatePath walks item following segs (already-resolved, no #nameRef).
// Returns nil when any segment is absent (missing attribute or out-of-bounds index).
func resolveUpdatePath(item map[string]any, segs []projSegment) any {
	if len(segs) == 0 { // unreachable: parseUpdatePath always returns ≥1 segment
		return nil
	}
	val, ok := item[segs[0].attr]
	if !ok {
		return nil
	}
	for _, seg := range segs[1:] {
		// All kumolo-stored DynamoDB values are map[string]any; assertion always holds.
		m := val.(map[string]any)
		if seg.attr != "" {
			mRaw, ok := m["M"]
			if !ok {
				return nil
			}
			// kumolo always writes M as map[string]any; assertion always holds.
			mMap := mRaw.(map[string]any)
			val, ok = mMap[seg.attr]
			if !ok {
				return nil
			}
		} else {
			lRaw, ok := m["L"]
			if !ok {
				return nil
			}
			// kumolo always writes L as []any; assertion always holds.
			lSlice := lRaw.([]any)
			if seg.index >= len(lSlice) {
				return nil
			}
			val = lSlice[seg.index]
		}
	}
	return val
}

// listAppendOp: SET target = list_append(left, right)
type listAppendOp struct {
	left  setOperand
	right setOperand
}

// parseUpdateExpression converts an UpdateExpression + attribute maps into a
// flat updates map (attribute name → operation):
//   - nil value   → REMOVE the attribute
//   - addOp       → ADD (numeric increment or set union)
//   - deleteOp    → DELETE (set difference)
//   - other value → SET the attribute to that value
func parseUpdateExpression(
	expr string,
	attrNames map[string]string,
	attrValues map[string]any,
) (map[string]any, error) {
	upper := strings.ToUpper(expr)
	updates := map[string]any{}

	updateExprKeywords := []string{"SET", "REMOVE", "ADD", "DELETE"}
	type section struct{ keyword, content string }
	var sections []section
	for _, kw := range updateExprKeywords {
		idx := strings.Index(upper, kw+" ")
		if idx < 0 {
			continue
		}
		// find where this section ends (at the next keyword)
		end := len(expr)
		for _, kw2 := range updateExprKeywords {
			if j := strings.Index(upper[idx+len(kw)+1:], kw2+" "); j >= 0 {
				if candidate := idx + len(kw) + 1 + j; candidate < end {
					end = candidate
				}
			}
		}
		sections = append(sections, section{kw, strings.TrimSpace(expr[idx+len(kw)+1 : end])})
	}
	if len(sections) == 0 {
		return nil, fmt.Errorf("unsupported UpdateExpression: %q", expr)
	}

	for _, sec := range sections {
		switch sec.keyword {
		case "SET":
			for _, assignment := range splitSetAssignments(sec.content) {
				parts := strings.SplitN(strings.TrimSpace(assignment), "=", 2)
				if len(parts) != 2 {
					return nil, fmt.Errorf("invalid SET clause: %q", assignment)
				}
				lhs := strings.TrimSpace(parts[0])
				segs, err := parseUpdatePath(lhs, attrNames)
				if err != nil {
					return nil, err
				}
				rhs := strings.TrimSpace(parts[1])
				var rhsVal any
				switch {
				case strings.HasPrefix(rhs, "if_not_exists("):
					op, err := parseIfNotExists(rhs, attrNames, attrValues)
					if err != nil {
						return nil, err
					}
					rhsVal = op
				case strings.HasPrefix(rhs, "list_append("):
					openIdx := len("list_append")
					closeIdx := findClose(rhs, openIdx)
					if closeIdx != len(rhs)-1 {
						return nil, fmt.Errorf("invalid list_append: %q", rhs)
					}
					inner := rhs[openIdx+1 : closeIdx]
					leftStr, rightStr, ok := splitTwoArgs(inner)
					if !ok {
						return nil, fmt.Errorf("invalid list_append: %q", rhs)
					}
					left, err := parseOperand(leftStr, attrNames, attrValues)
					if err != nil {
						return nil, err
					}
					right, err := parseOperand(rightStr, attrNames, attrValues)
					if err != nil {
						return nil, err
					}
					rhsVal = listAppendOp{left: left, right: right}
				default:
					val, ok := attrValues[rhs]
					if !ok {
						return nil, fmt.Errorf("ExpressionAttributeValues missing %q", rhs)
					}
					rhsVal = val
				}
				if len(segs) == 1 {
					updates[segs[0].attr] = rhsVal
				} else {
					// Nested path: use the raw LHS as map key (unique per assignment).
					updates[lhs] = nestedSetOp{segs: segs, val: rhsVal}
				}
			}
		case "REMOVE":
			for _, token := range strings.Split(sec.content, ",") {
				tok := strings.TrimSpace(token)
				segs, err := parseUpdatePath(tok, attrNames)
				if err != nil {
					return nil, err
				}
				if len(segs) == 1 {
					updates[segs[0].attr] = nil
				} else {
					updates[tok] = nestedRemoveOp{segs: segs}
				}
			}
		case "ADD":
			for _, pair := range strings.Split(sec.content, ",") {
				parts := strings.Fields(strings.TrimSpace(pair))
				if len(parts) != 2 {
					return nil, fmt.Errorf("invalid ADD clause: %q", pair)
				}
				name, err := resolveAttrName(parts[0], attrNames)
				if err != nil {
					return nil, err
				}
				val, ok := attrValues[parts[1]]
				if !ok {
					return nil, fmt.Errorf("ExpressionAttributeValues missing %q", parts[1])
				}
				updates[name] = addOp{val}
			}
		case "DELETE":
			for _, pair := range strings.Split(sec.content, ",") {
				parts := strings.Fields(strings.TrimSpace(pair))
				if len(parts) != 2 {
					return nil, fmt.Errorf("invalid DELETE clause: %q", pair)
				}
				name, err := resolveAttrName(parts[0], attrNames)
				if err != nil {
					return nil, err
				}
				val, ok := attrValues[parts[1]]
				if !ok {
					return nil, fmt.Errorf("ExpressionAttributeValues missing %q", parts[1])
				}
				updates[name] = deleteOp{val}
			}
		}
	}
	return updates, nil
}

// applyAddOp applies an ADD clause delta to the current attribute value.
// For Number types, adds the numeric delta (creates the attribute if absent).
// For set types (SS/NS/BS), unions the current set with the delta elements.
func applyAddOp(current, delta any) (any, error) {
	dm, ok := delta.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("ADD: delta must be a DynamoDB typed value")
	}
	if dn, ok := dm["N"].(string); ok {
		dv, err := strconv.ParseFloat(dn, 64)
		if err != nil {
			return nil, fmt.Errorf("ADD: invalid number %q", dn)
		}
		var cv float64
		if current != nil {
			cm, ok := current.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("ADD: existing attribute is not a typed value")
			}
			cn, ok := cm["N"].(string)
			if !ok {
				return nil, fmt.Errorf("ADD: existing attribute is not a Number")
			}
			cv, err = strconv.ParseFloat(cn, 64)
			if err != nil {
				return nil, fmt.Errorf("ADD: invalid existing number %q", cn)
			}
		}
		return map[string]any{"N": strconv.FormatFloat(cv+dv, 'f', -1, 64)}, nil
	}
	for _, setKey := range []string{"SS", "NS", "BS"} {
		if deltaElems, ok := dm[setKey]; ok {
			var currentElems []any
			if current != nil {
				cm, ok := current.(map[string]any)
				if !ok {
					return nil, fmt.Errorf("ADD: existing attribute is not a typed value")
				}
				ce, ok := cm[setKey].([]any)
				if !ok {
					return nil, fmt.Errorf(
						"ADD: existing attribute type mismatch: expected %s",
						setKey,
					)
				}
				currentElems = ce
			}
			deltaSlice, _ := deltaElems.([]any)
			return map[string]any{setKey: setUnion(currentElems, deltaSlice)}, nil
		}
	}
	return nil, fmt.Errorf("ADD: unsupported type in delta value")
}

// applyDeleteOp applies a DELETE clause delta to the current attribute value.
// Only set types (SS/NS/BS) are supported. Returns nil when the result is an empty set.
func applyDeleteOp(current, delta any) (any, error) {
	if current == nil {
		return nil, nil
	}
	dm, ok := delta.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("DELETE: delta must be a DynamoDB typed value")
	}
	for _, setKey := range []string{"SS", "NS", "BS"} {
		if deltaElems, ok := dm[setKey]; ok {
			cm, ok := current.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("DELETE: existing attribute is not a typed value")
			}
			ce, ok := cm[setKey].([]any)
			if !ok {
				return nil, fmt.Errorf("DELETE: existing attribute is not a %s", setKey)
			}
			deltaSlice, _ := deltaElems.([]any)
			result := setDifference(ce, deltaSlice)
			if len(result) == 0 {
				return nil, nil // empty set → remove the attribute
			}
			return map[string]any{setKey: result}, nil
		}
	}
	return nil, fmt.Errorf("DELETE: unsupported type; only set types (SS/NS/BS) are valid")
}

// splitSetAssignments splits a SET clause on top-level commas (not inside parentheses).
func splitSetAssignments(s string) []string {
	var result []string
	depth := 0
	start := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			depth--
		case ',':
			if depth == 0 {
				result = append(result, s[start:i])
				start = i + 1
			}
		}
	}
	result = append(result, s[start:])
	return result
}

// findClose returns the index of the ')' that closes the '(' at idx, or -1.
func findClose(s string, idx int) int {
	depth := 0
	for i := idx; i < len(s); i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// splitTwoArgs finds the first top-level comma in s and returns the two trimmed parts.
// ok is false when no top-level comma is found.
func splitTwoArgs(s string) (left, right string, ok bool) {
	depth := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			depth--
		case ',':
			if depth == 0 {
				return strings.TrimSpace(s[:i]), strings.TrimSpace(s[i+1:]), true
			}
		}
	}
	return "", "", false
}

// parseIfNotExists parses an if_not_exists(path, operand) expression.
// Per AWS spec the first argument must be an attribute path, not a value placeholder.
func parseIfNotExists(
	rhs string,
	attrNames map[string]string,
	attrValues map[string]any,
) (ifNotExistsOp, error) {
	openIdx := len("if_not_exists")
	closeIdx := findClose(rhs, openIdx)
	if closeIdx != len(rhs)-1 {
		return ifNotExistsOp{}, fmt.Errorf("invalid if_not_exists: %q", rhs)
	}
	inner := rhs[openIdx+1 : closeIdx]
	pathStr, operandStr, ok := splitTwoArgs(inner)
	if !ok {
		return ifNotExistsOp{}, fmt.Errorf("invalid if_not_exists: %q", rhs)
	}
	if strings.HasPrefix(pathStr, ":") || strings.ContainsRune(pathStr, '(') {
		return ifNotExistsOp{}, fmt.Errorf(
			"invalid if_not_exists: first argument must be a path, not a value: %q", pathStr,
		)
	}
	if strings.HasPrefix(operandStr, "if_not_exists(") {
		return ifNotExistsOp{}, fmt.Errorf(
			"invalid if_not_exists: second argument cannot be a function call: %q", operandStr,
		)
	}
	segs, err := parseUpdatePath(pathStr, attrNames)
	if err != nil {
		return ifNotExistsOp{}, err
	}
	operand, err := parseOperand(operandStr, attrNames, attrValues)
	if err != nil {
		return ifNotExistsOp{}, err
	}
	return ifNotExistsOp{segs: segs, operand: operand}, nil
}

// parseOperand parses a SET-clause operand: ":val" → literal, "if_not_exists(...)" → nested op, else → attr ref.
func parseOperand(
	ref string,
	attrNames map[string]string,
	attrValues map[string]any,
) (setOperand, error) {
	if strings.HasPrefix(ref, ":") {
		v, ok := attrValues[ref]
		if !ok {
			return nil, fmt.Errorf("ExpressionAttributeValues missing %q", ref)
		}
		return setLiteral{val: v}, nil
	}
	if strings.HasPrefix(ref, "if_not_exists(") {
		return parseIfNotExists(ref, attrNames, attrValues)
	}
	segs, err := parseUpdatePath(ref, attrNames)
	if err != nil {
		return nil, err
	}
	return setAttrRef{segs: segs}, nil
}

// applyListAppendOp concatenates two List-typed operands and returns the result.
func applyListAppendOp(item map[string]any, op listAppendOp) (any, error) {
	leftList, err := toListAttr(op.left.resolve(item))
	if err != nil {
		return nil, fmt.Errorf("list_append left: %v", err)
	}
	rightList, err := toListAttr(op.right.resolve(item))
	if err != nil {
		return nil, fmt.Errorf("list_append right: %v", err)
	}
	combined := append(append([]any(nil), leftList...), rightList...)
	return map[string]any{"L": combined}, nil
}

// toListAttr extracts the []any from a DynamoDB L-typed value.
func toListAttr(v any) ([]any, error) {
	if v == nil {
		return nil, fmt.Errorf("not a List type")
	}
	m, ok := v.(map[string]any)
	if !ok { // untestable: DynamoDB item values are always typed maps
		return nil, fmt.Errorf("not a typed value")
	}
	l, ok := m["L"].([]any)
	if !ok {
		return nil, fmt.Errorf("not a List type")
	}
	return l, nil
}

func setUnion(a, b []any) []any {
	seen := make(map[string]bool, len(a)+len(b))
	result := make([]any, 0, len(a)+len(b))
	for _, v := range a {
		key, _ := json.Marshal(v)
		s := string(key)
		if !seen[s] {
			seen[s] = true
			result = append(result, v)
		}
	}
	for _, v := range b {
		key, _ := json.Marshal(v)
		s := string(key)
		if !seen[s] {
			seen[s] = true
			result = append(result, v)
		}
	}
	return result
}

func setDifference(a, b []any) []any {
	remove := make(map[string]bool, len(b))
	for _, v := range b {
		key, _ := json.Marshal(v)
		remove[string(key)] = true
	}
	result := make([]any, 0, len(a))
	for _, v := range a {
		key, _ := json.Marshal(v)
		if !remove[string(key)] {
			result = append(result, v)
		}
	}
	return result
}

// parseKeyConditionExpression extracts the hash key name, its equality value,
// and an optional sort key condition from a KeyConditionExpression.
// The hash key condition must be an equality; the sort key condition supports
// =, <, <=, >, >=, BETWEEN, and begins_with.
func parseKeyConditionExpression(
	expr string,
	attrNames map[string]string,
	attrValues map[string]any,
) (string, any, *SortKeyCondition, error) {
	parts := strings.SplitN(strings.TrimSpace(expr), " AND ", 2)

	// Parse hash key equality condition
	tokens := strings.Fields(strings.TrimSpace(parts[0]))
	if len(tokens) != 3 || tokens[1] != "=" {
		return "", nil, nil, fmt.Errorf("unsupported KeyConditionExpression: %q", expr)
	}
	name, err := resolveAttrName(tokens[0], attrNames)
	if err != nil {
		return "", nil, nil, err
	}
	val, ok := attrValues[tokens[2]]
	if !ok {
		return "", nil, nil, fmt.Errorf("ExpressionAttributeValues missing %q", tokens[2])
	}

	// Parse optional sort key condition
	var skCond *SortKeyCondition
	if len(parts) == 2 {
		var err error
		skCond, err = parseSortKeyCondition(strings.TrimSpace(parts[1]), attrNames, attrValues)
		if err != nil {
			return "", nil, nil, err
		}
	}

	return name, val, skCond, nil
}

// parseSortKeyCondition parses a single sort key condition expression.
// Supported forms: attr OP :val (OP = =/</<=/>/>=),
// attr BETWEEN :lo AND :hi, begins_with(attr, :prefix).
func parseSortKeyCondition(
	expr string,
	attrNames map[string]string,
	attrValues map[string]any,
) (*SortKeyCondition, error) {
	resolveValue := func(ref string) (any, error) {
		v, ok := attrValues[ref]
		if !ok {
			return nil, fmt.Errorf("ExpressionAttributeValues missing %q", ref)
		}
		return v, nil
	}

	if strings.HasPrefix(expr, "begins_with(") {
		inner := strings.TrimSuffix(strings.TrimPrefix(expr, "begins_with("), ")")
		argParts := strings.SplitN(inner, ",", 2)
		if len(argParts) != 2 {
			return nil, fmt.Errorf("invalid begins_with: %q", expr)
		}
		skName, err := resolveAttrName(strings.TrimSpace(argParts[0]), attrNames)
		if err != nil {
			return nil, err
		}
		skVal, err := resolveValue(strings.TrimSpace(argParts[1]))
		if err != nil {
			return nil, err
		}
		return &SortKeyCondition{Name: skName, Operator: OpBeginsWith, Value: skVal}, nil
	}

	tokens := strings.Fields(expr)

	if len(tokens) == 5 &&
		strings.ToUpper(tokens[1]) == OpBETWEEN &&
		strings.ToUpper(tokens[3]) == "AND" {
		skName, err := resolveAttrName(tokens[0], attrNames)
		if err != nil {
			return nil, err
		}
		lo, err := resolveValue(tokens[2])
		if err != nil {
			return nil, err
		}
		hi, err := resolveValue(tokens[4])
		if err != nil {
			return nil, err
		}
		return &SortKeyCondition{Name: skName, Operator: OpBETWEEN, Value: lo, Value2: hi}, nil
	}

	if len(tokens) == 3 {
		skName, err := resolveAttrName(tokens[0], attrNames)
		if err != nil {
			return nil, err
		}
		op := tokens[1]
		switch op {
		case OpEQ, OpLT, OpLTE, OpGT, OpGTE:
		default:
			return nil, fmt.Errorf("unsupported sort key operator: %q", op)
		}
		skVal, err := resolveValue(tokens[2])
		if err != nil {
			return nil, err
		}
		return &SortKeyCondition{Name: skName, Operator: op, Value: skVal}, nil
	}

	return nil, fmt.Errorf("unsupported sort key condition: %q", expr)
}
