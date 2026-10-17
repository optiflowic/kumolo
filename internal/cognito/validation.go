package cognito

import (
	"cmp"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"
)

type shapeKind int

const (
	kindString shapeKind = iota + 1
	kindInteger
	kindBoolean
	kindStructure
	kindList
	kindMap
)

type inputPattern struct {
	src string
	re  *regexp.Regexp
}

func mustPattern(src string) *inputPattern {
	return &inputPattern{src: src, re: regexp.MustCompile(`^(?:` + src + `)$`)}
}

// inputShape is one shape of the AWS model: its type, its constraints and, for a
// structure, list or map, the shapes it contains.
type inputShape struct {
	kind       shapeKind
	name       string // structure, as AWS prints its value
	sensitive  bool
	min, max   *int64 // length for strings, lists and maps; value for integers
	pattern    *inputPattern
	enum       []string
	members    []memberRule // structure, in model declaration order
	member     *inputShape  // list
	key, value *inputShape  // map
}

type memberRule struct {
	name     string
	required bool
	shape    *inputShape
}

// memberOrderCapacity is the table size of the Java HashMap whose iteration order AWS
// reports violations in across the members of a structure (fitted to live calls, #624).
const memberOrderCapacity uint32 = 64

// validateInput checks a decoded request against the AWS model constraints, nested members
// included. It runs before any handler, so a constraint violation is reported ahead of
// resource lookups and token verification, as on AWS.
func validateInput(in *inputShape, input map[string]any) (message string, ok bool) {
	if i := slices.IndexFunc(in.members, func(m memberRule) bool {
		return m.name == "UserPoolId"
	}); i >= 0 && in.members[i].required {
		if _, present := input["UserPoolId"]; !present {
			return "UserPoolId must not be null", false
		}
	}

	violations := in.checkStructure("", input, false)
	if len(violations) == 0 {
		return "", true
	}
	noun := "errors"
	if len(violations) == 1 {
		noun = "error"
	}
	return fmt.Sprintf("%d validation %s detected: %s",
		len(violations), noun, strings.Join(violations, "; ")), false
}

func (s *inputShape) checkStructure(path string, v map[string]any, nested bool) []string {
	names := make([]string, len(s.members))
	for i, m := range s.members {
		names[i] = lowerFirst(m.name)
	}
	var out []string
	for _, name := range javaOrder(names, memberOrderCapacity) {
		m := s.members[slices.Index(names, name)]
		if path != "" {
			name = path + "." + name
		}
		val, present := v[m.name]
		if !present && nested && m.shape.kind == kindInteger {
			// AWS reads an omitted integer inside a structure as 0.
			val, present = int64(0), true
		}
		if !present {
			if m.required {
				out = append(out, violation(name, m.shape.display(nil), "Member must not be null"))
			}
			continue
		}
		out = append(out, m.shape.check(name, val)...)
	}
	return out
}

// check returns the violations of a value decoded by decodeInput.
func (s *inputShape) check(path string, v any) []string {
	switch s.kind {
	case kindStructure:
		return s.checkStructure(path, v.(map[string]any), true)
	case kindList:
		return s.checkList(path, v.([]any))
	case kindMap:
		return s.checkMap(path, v.(map[string]any))
	case kindString, kindInteger:
		var out []string
		for _, c := range s.broken(v) {
			out = append(out, violation(path, s.display(v), c))
		}
		return out
	}
	return nil
}

func (s *inputShape) checkList(path string, l []any) []string {
	out := s.sizeViolations(path, l, len(l))
	if s.member.kind == kindStructure {
		for i, e := range l {
			if e != nil {
				out = append(out, s.member.checkStructure(
					fmt.Sprintf("%s.%d.member", path, i+1), e.(map[string]any), true)...)
			}
		}
		return out
	}
	if slices.ContainsFunc(l, s.member.isBroken) {
		out = append(out, violation(path, s.display(l),
			"Member must satisfy constraint: "+s.member.constraintList()))
	}
	return out
}

func (s *inputShape) checkMap(path string, m map[string]any) []string {
	out := s.sizeViolations(path, m, len(m))
	keys := mapKeys(m)
	if slices.ContainsFunc(keys, func(k string) bool { return s.key.isBroken(k) }) {
		out = append(out, violation(path, s.display(m),
			"Map keys must satisfy constraint: "+s.key.constraintList()))
	}
	if s.value.kind == kindStructure {
		for _, k := range keys {
			if v := m[k]; v != nil {
				out = append(out, s.value.checkStructure(
					path+"."+k+".member", v.(map[string]any), true)...)
			}
		}
		return out
	}
	if slices.ContainsFunc(keys, func(k string) bool { return s.value.isBroken(m[k]) }) {
		out = append(out, violation(path, s.display(m),
			"Map value must satisfy constraint: "+s.value.constraintList()))
	}
	return out
}

func (s *inputShape) sizeViolations(path string, v any, size int) []string {
	var out []string
	if s.max != nil && int64(size) > *s.max {
		out = append(out, violation(path, s.display(v), maxConstraint("length", *s.max)))
	}
	if s.min != nil && int64(size) < *s.min {
		out = append(out, violation(path, s.display(v), minConstraint("length", *s.min)))
	}
	return out
}

func (s *inputShape) isBroken(v any) bool {
	return v != nil && len(s.broken(v)) > 0
}

// broken returns the constraints a scalar breaks, in the order AWS reports them.
func (s *inputShape) broken(v any) []string {
	var (
		size int64
		unit = "value"
		str  string
		out  []string
	)
	switch v := v.(type) {
	case string:
		str, size, unit = v, int64(utf8.RuneCountInString(v)), "length"
	case int64:
		size = v
	default: // unreachable: string and integer shapes decode to string and int64
		return nil
	}
	if s.max != nil && size > *s.max {
		out = append(out, maxConstraint(unit, *s.max))
	}
	if s.min != nil && size < *s.min {
		out = append(out, minConstraint(unit, *s.min))
	}
	if unit == "length" {
		if s.pattern != nil && !s.pattern.re.MatchString(str) {
			out = append(out, patternConstraint(s.pattern))
		}
		if s.enum != nil && !slices.Contains(s.enum, str) {
			out = append(out, enumConstraint(s.enum))
		}
	}
	return out
}

// constraintList prints every constraint of a list member, map key or map value shape,
// broken or not, as AWS does.
func (s *inputShape) constraintList() string {
	unit := "value"
	if s.kind == kindString {
		unit = "length"
	}
	var cs []string
	if s.max != nil {
		cs = append(cs, maxConstraint(unit, *s.max))
	}
	if s.min != nil {
		cs = append(cs, minConstraint(unit, *s.min))
	}
	if s.pattern != nil {
		cs = append(cs, patternConstraint(s.pattern))
	}
	if s.enum != nil {
		cs = append(cs, enumConstraint(s.enum))
	}
	return "[" + strings.Join(cs, ", ") + "]"
}

func maxConstraint(unit string, n int64) string {
	return fmt.Sprintf("Member must have %s less than or equal to %d", unit, n)
}

func minConstraint(unit string, n int64) string {
	return fmt.Sprintf("Member must have %s greater than or equal to %d", unit, n)
}

func patternConstraint(p *inputPattern) string {
	return "Member must satisfy regular expression pattern: " + p.src
}

func enumConstraint(enum []string) string {
	return "Member must satisfy enum value set: [" +
		strings.Join(javaOrder(enum, hashCapacity(len(enum))), ", ") + "]"
}

// display is the value part of a violation: quoted, "null" when absent, or empty when the
// shape is sensitive, as AWS prints it.
func (s *inputShape) display(v any) string {
	switch {
	case s.hidden():
		return ""
	case v == nil:
		return "null"
	}
	return "'" + s.render(v) + "'"
}

func (s *inputShape) hidden() bool {
	return s.sensitive ||
		s.member != nil && s.member.sensitive ||
		s.value != nil && s.value.sensitive
}

// render prints a value the way AWS's Java service does: lists as [a, b], maps as
// {k=v} in HashMap order and structures as Name(member=value, ...).
func (s *inputShape) render(v any) string {
	if v == nil {
		return "null"
	}
	switch s.kind {
	case kindString:
		return v.(string)
	case kindInteger:
		return strconv.FormatInt(v.(int64), 10)
	case kindBoolean:
		return strconv.FormatBool(v.(bool))
	case kindList:
		parts := make([]string, 0, len(v.([]any)))
		for _, e := range v.([]any) {
			parts = append(parts, s.member.render(e))
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case kindMap:
		m := v.(map[string]any)
		parts := make([]string, 0, len(m))
		for _, k := range mapKeys(m) {
			parts = append(parts, k+"="+s.value.render(m[k]))
		}
		return "{" + strings.Join(parts, ", ") + "}"
	}
	m := v.(map[string]any)
	parts := make([]string, 0, len(s.members))
	for _, mem := range s.members {
		val := m[mem.name]
		if val == nil && mem.shape.kind == kindInteger {
			val = int64(0) // as checkStructure reads it
		}
		parts = append(parts, lowerFirst(mem.name)+"="+mem.shape.render(val))
	}
	return s.name + "(" + strings.Join(parts, ", ") + ")"
}

// mapKeys returns the keys in Java HashMap iteration order. Keys in the same bucket keep
// sorted order; AWS keeps request order, which decodeInput does not retain.
func mapKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return javaOrder(keys, hashCapacity(len(keys)))
}

// javaOrder sorts keys into the iteration order of a Java HashMap or HashSet with the
// given table size. Keys that share a bucket keep their input order.
func javaOrder(keys []string, capacity uint32) []string {
	out := slices.Clone(keys)
	slices.SortStableFunc(out, func(a, b string) int {
		return cmp.Compare(javaBucket(a, capacity), javaBucket(b, capacity))
	})
	return out
}

func javaBucket(s string, capacity uint32) uint32 {
	var h uint32
	for _, c := range utf16.Encode([]rune(s)) {
		h = 31*h + uint32(c)
	}
	return (h ^ h>>16) & (capacity - 1)
}

// javaMaxCapacity is Java HashMap's MAXIMUM_CAPACITY; the table stops growing there.
const javaMaxCapacity uint32 = 1 << 30

// hashCapacity is the table size of a Java HashMap after n insertions.
func hashCapacity(n int) uint32 {
	c := uint32(16)
	for c < javaMaxCapacity && n > int(c)*3/4 {
		c *= 2
	}
	return c
}

func lowerFirst(name string) string {
	r, n := utf8.DecodeRuneInString(name)
	return string(unicode.ToLower(r)) + name[n:]
}

// violation formats one entry the way AWS does. shown is empty for sensitive members.
func violation(path, shown, constraint string) string {
	if shown != "" {
		shown += " "
	}
	return fmt.Sprintf("Value %sat '%s' failed to satisfy constraint: %s", shown, path, constraint)
}
