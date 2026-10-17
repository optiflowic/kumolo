package cognito

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"
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
	sensitive  bool
	min, max   *int64 // length for strings, lists and maps; value for integers
	pattern    *inputPattern
	enum       []string
	members    []memberRule // structure
	member     *inputShape  // list
	key, value *inputShape  // map
}

type memberRule struct {
	name     string
	required bool
	shape    *inputShape
}

// validateInput checks the top-level members of a decoded request against the AWS model
// constraints. It runs before any handler, so a constraint violation is reported ahead of
// resource lookups and token verification, as on AWS.
func validateInput(in *inputShape, input map[string]any) (message string, ok bool) {
	var violations []string
	for _, rule := range in.members {
		v, present := input[rule.name]
		if present {
			violations = append(violations, rule.shape.check(rule.name, v)...)
		}
		if rule.required && !present {
			violations = append(
				violations,
				violation(rule.name, "null", "Member must not be null"),
			)
		}
	}
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

// check returns one violation per broken constraint of a scalar value decoded by
// decodeInput.
func (s *inputShape) check(name string, value any) []string {
	var (
		shown    string
		size     int64
		sizeUnit = "value"
		out      []string
	)
	switch s.kind {
	case kindString:
		v := value.(string)
		shown, size, sizeUnit = "'"+v+"'", int64(utf8.RuneCountInString(v)), "length"
		if s.pattern != nil && !s.pattern.re.MatchString(v) {
			out = append(out, "Member must satisfy regular expression pattern: "+s.pattern.src)
		}
		if s.enum != nil && !slices.Contains(s.enum, v) {
			out = append(out,
				"Member must satisfy enum value set: ["+strings.Join(s.enum, ", ")+"]")
		}
	case kindInteger:
		v := value.(int64)
		shown, size = "'"+strconv.FormatInt(v, 10)+"'", v
	default:
		return nil
	}
	if s.max != nil && size > *s.max {
		out = append(
			out,
			fmt.Sprintf("Member must have %s less than or equal to %d", sizeUnit, *s.max),
		)
	}
	if s.min != nil && size < *s.min {
		out = append(out,
			fmt.Sprintf("Member must have %s greater than or equal to %d", sizeUnit, *s.min))
	}

	if s.sensitive {
		shown = ""
	}
	for i, constraint := range out {
		out[i] = violation(name, shown, constraint)
	}
	return out
}

// violation formats one entry the way AWS does. shown is empty for sensitive members,
// whose value AWS leaves out.
func violation(name, shown, constraint string) string {
	r, n := utf8.DecodeRuneInString(name)
	member := string(unicode.ToLower(r)) + name[n:]
	if shown != "" {
		shown += " "
	}
	return fmt.Sprintf(
		"Value %sat '%s' failed to satisfy constraint: %s",
		shown,
		member,
		constraint,
	)
}
