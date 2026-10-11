package cognito

import (
	"encoding/json"
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
)

type inputPattern struct {
	src string
	re  *regexp.Regexp
}

func mustPattern(src string) *inputPattern {
	return &inputPattern{src: src, re: regexp.MustCompile(`^(?:` + src + `)$`)}
}

// inputShape holds the constraints the AWS model puts on a scalar shape.
type inputShape struct {
	kind      shapeKind
	sensitive bool
	min, max  *int64 // length for strings, value for integers
	pattern   *inputPattern
	enum      []string
}

// memberRule is one top-level input member. A nil shape means only presence is checked.
type memberRule struct {
	name     string
	required bool
	shape    *inputShape
}

// validateInput checks the request against the AWS model constraints in inputRules.
// It runs before any handler, so a constraint violation is reported ahead of resource
// lookups and token verification, as on AWS. A body that is not a JSON object passes;
// the handler reports it.
func validateInput(op string, body []byte) (message string, ok bool) {
	var members map[string]json.RawMessage
	if err := json.Unmarshal(body, &members); err != nil {
		return "", true
	}

	var violations []string
	for _, rule := range inputRules[op] {
		raw, present := members[rule.name]
		if !present || string(raw) == "null" {
			if rule.required {
				violations = append(
					violations,
					violation(rule.name, "null", "Member must not be null"),
				)
			}
			continue
		}
		if rule.shape != nil {
			violations = append(violations, rule.shape.check(rule.name, raw)...)
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

// check returns one violation per broken constraint. A value of the wrong JSON type
// returns none; the handler's own decoding reports it.
func (s *inputShape) check(name string, raw json.RawMessage) []string {
	var (
		shown    string
		size     int64
		sizeUnit = "value"
		out      []string
	)
	switch s.kind {
	case kindString:
		var v string
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil
		}
		shown, size, sizeUnit = "'"+v+"'", int64(utf8.RuneCountInString(v)), "length"
		if s.pattern != nil && !s.pattern.re.MatchString(v) {
			out = append(out, "Member must satisfy regular expression pattern: "+s.pattern.src)
		}
		if s.enum != nil && !slices.Contains(s.enum, v) {
			out = append(out,
				"Member must satisfy enum value set: ["+strings.Join(s.enum, ", ")+"]")
		}
	case kindInteger:
		var v int64
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil
		}
		shown, size = "'"+strconv.FormatInt(v, 10)+"'", v
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
