//go:build ignore

// gencases generates input validation cases for one service from the botocore model
// bundled with awscli2. Each case breaks one top-level input member (missing required,
// length, pattern, enum or range) and expects the service's validation error.
//
// Usage:
//
//	go run tools/gencases/main.go -model <service-2.json> -router internal/cognito/router.go \
//	  -error InvalidParameterException -out tests/integration/testdata/cognito_validation.json
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
)

type model struct {
	Metadata struct {
		APIVersion   string `json:"apiVersion"`
		JSONVersion  string `json:"jsonVersion"`
		TargetPrefix string `json:"targetPrefix"`
	} `json:"metadata"`
	Operations map[string]struct {
		Input *struct {
			Shape string `json:"shape"`
		} `json:"input"`
	} `json:"operations"`
	Shapes map[string]shape `json:"shapes"`
}

type shape struct {
	Type     string            `json:"type"`
	Required []string          `json:"required"`
	Members  map[string]member `json:"members"`
	Member   *member           `json:"member"`
	Key      *member           `json:"key"`
	Value    *member           `json:"value"`
	Enum     []string          `json:"enum"`
	Min      *float64          `json:"min"`
	Max      *float64          `json:"max"`
	Pattern  string            `json:"pattern"`
}

type member struct {
	Shape string `json:"shape"`
}

type testCase struct {
	Name      string         `json:"name"`
	Operation string         `json:"operation"`
	Body      map[string]any `json:"body"`
	// Expect is the error type the case must return. Empty for the base request,
	// which must pass validation.
	Expect string `json:"expect,omitempty"`
}

type output struct {
	Service     string     `json:"service"`
	APIVersion  string     `json:"api_version"`
	JSONVersion string     `json:"json_version"`
	Target      string     `json:"target_prefix"`
	Cases       []testCase `json:"cases"`
	Skipped     []string   `json:"skipped"`
}

// samples are values for shapes whose pattern alone does not give a realistic value,
// or where kumolo is stricter than the model (client IDs are 26 lowercase characters).
var samples = map[string]string{
	"UserPoolIdType": "us-east-1_abc123XYZ",
	"ClientIdType":   "abcdefghijklmnopqrstuvwxyz",
	"ArnType":        "arn:aws:cognito-idp:us-east-1:123456789012:userpool/us-east-1_abc123XYZ",
	"TokenModelType": "abc.def.ghi",
	"PasswordType":   "Passw0rd!",
	// kumolo supports only email and phone_number for verification flows.
	"AttributeNameType": "email",
}

// baseExtras adds members that are optional in the model but required in practice
// (one of AccessToken or Session must be present).
var baseExtras = map[string]map[string]any{
	"AssociateSoftwareToken": {"AccessToken": "abc.def.ghi"},
	"RespondToAuthChallenge": {"Session": "abcdefghijklmnopqrstuvwxyz"},
	"VerifySoftwareToken":    {"AccessToken": "abc.def.ghi"},
}

var candidates = []string{"abc123", "user1", "123456", "a"}

var invalidCandidates = []string{"!!!", " ", "a b", "@@@", "\t"}

const (
	// maxOverflowLen caps the length limits a "too long" case is generated for,
	// so request bodies stay small.
	maxOverflowLen = 10000
	maxNestDepth   = 3
)

func main() {
	modelPath := flag.String("model", "", "path to botocore service-2.json")
	routerPath := flag.String("router", "", "router.go with case \"<Operation>\": dispatch")
	errType := flag.String("error", "", "validation error type of the service")
	outPath := flag.String("out", "", "output JSON path")
	service := flag.String("service", "", "service name recorded in the output")
	flag.Parse()
	if *modelPath == "" || *routerPath == "" || *errType == "" || *outPath == "" {
		flag.Usage()
		os.Exit(2)
	}

	if err := run(*modelPath, *routerPath, *errType, *outPath, *service); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(modelPath, routerPath, errType, outPath, service string) error {
	raw, err := os.ReadFile(modelPath)
	if err != nil {
		return err
	}
	var m model
	if err := json.Unmarshal(raw, &m); err != nil {
		return fmt.Errorf("parse model: %w", err)
	}
	ops, err := implementedOps(routerPath)
	if err != nil {
		return err
	}

	out := output{
		Service:     service,
		APIVersion:  m.Metadata.APIVersion,
		JSONVersion: m.Metadata.JSONVersion,
		Target:      m.Metadata.TargetPrefix,
		Cases:       []testCase{},
		Skipped:     []string{},
	}
	for _, op := range ops {
		cases, skipped := casesFor(&m, op, errType)
		out.Cases = append(out.Cases, cases...)
		out.Skipped = append(out.Skipped, skipped...)
	}

	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(outPath, append(data, '\n'), 0o644); err != nil {
		return err
	}
	fmt.Printf("%d cases, %d skipped\n", len(out.Cases), len(out.Skipped))
	return nil
}

func implementedOps(routerPath string) ([]string, error) {
	src, err := os.ReadFile(routerPath)
	if err != nil {
		return nil, err
	}
	var ops []string
	for _, m := range regexp.MustCompile(`case "(\w+)":`).FindAllStringSubmatch(string(src), -1) {
		ops = append(ops, m[1])
	}
	sort.Strings(ops)
	return ops, nil
}

func casesFor(m *model, op, errType string) ([]testCase, []string) {
	o, ok := m.Operations[op]
	if !ok {
		return nil, []string{op + ": not in model"}
	}
	if o.Input == nil {
		return nil, []string{op + ": no input"}
	}
	in := m.Shapes[o.Input.Shape]

	base := map[string]any{}
	for _, name := range in.Required {
		v, err := validValue(m, in.Members[name].Shape, 0)
		if err != nil {
			return nil, []string{fmt.Sprintf("%s: base value for %s: %v", op, name, err)}
		}
		base[name] = v
	}
	for k, v := range baseExtras[op] {
		base[k] = v
	}

	cases := []testCase{{Name: op + "/base", Operation: op, Body: base}}
	var skipped []string
	add := func(desc, name string, v any) {
		body := clone(base)
		body[name] = v
		cases = append(cases, testCase{
			Name: op + "/" + name + " " + desc, Operation: op, Body: body, Expect: errType,
		})
	}

	for _, name := range in.Required {
		body := clone(base)
		delete(body, name)
		cases = append(cases, testCase{
			Name: op + "/" + name + " missing", Operation: op, Body: body, Expect: errType,
		})
	}

	names := make([]string, 0, len(in.Members))
	for name := range in.Members {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		s := m.Shapes[in.Members[name].Shape]
		switch s.Type {
		case "string":
			if len(s.Enum) > 0 {
				add("invalid enum", name, "INVALID_ENUM_VALUE")
				continue
			}
			valid, err := validValue(m, in.Members[name].Shape, 0)
			if err != nil {
				skipped = append(skipped, fmt.Sprintf("%s: %s: %v", op, name, err))
				continue
			}
			str := valid.(string)
			skip := func(desc string) {
				skipped = append(skipped, fmt.Sprintf("%s: %s: no %s value", op, name, desc))
			}
			if s.Max != nil && *s.Max < maxOverflowLen {
				if v, ok := resize(s, str, int(*s.Max)+1); ok {
					add("too long", name, v)
				} else {
					skip("too long")
				}
			}
			if s.Min != nil && *s.Min > 1 {
				if v, ok := resize(s, str, int(*s.Min)-1); ok {
					add("too short", name, v)
				} else {
					skip("too short")
				}
			}
			if s.Pattern != "" {
				if v, ok := invalidPattern(s); ok {
					add("invalid pattern", name, v)
				} else {
					skip("invalid pattern")
				}
			}
		case "integer", "long":
			if s.Min != nil {
				add("below min", name, int(*s.Min)-1)
			}
			if s.Max != nil {
				add("above max", name, int(*s.Max)+1)
			}
		}
	}
	return cases, skipped
}

// validValue builds a value that satisfies the shape's constraints.
func validValue(m *model, name string, depth int) (any, error) {
	if depth > maxNestDepth {
		return nil, fmt.Errorf("shape %s nests too deep", name)
	}
	s := m.Shapes[name]
	switch s.Type {
	case "string":
		if len(s.Enum) > 0 {
			return s.Enum[0], nil
		}
		if v, ok := samples[name]; ok {
			return v, nil
		}
		for _, c := range candidates {
			n := len([]rune(c))
			if s.Min != nil && float64(n) < *s.Min {
				n = int(*s.Min)
			}
			if !inRange(s, n) {
				continue
			}
			if v, ok := resize(s, c, n); ok {
				return v, nil
			}
		}
		return nil, fmt.Errorf("no sample for %s (pattern %q)", name, s.Pattern)
	case "integer", "long":
		if s.Min != nil {
			return int(*s.Min), nil
		}
		return 1, nil
	case "boolean":
		return false, nil
	case "structure":
		obj := map[string]any{}
		for _, r := range s.Required {
			v, err := validValue(m, s.Members[r].Shape, depth+1)
			if err != nil {
				return nil, err
			}
			obj[r] = v
		}
		return obj, nil
	case "list":
		v, err := validValue(m, s.Member.Shape, depth+1)
		if err != nil {
			return nil, err
		}
		return []any{v}, nil
	case "map":
		k, err := validValue(m, s.Key.Shape, depth+1)
		if err != nil {
			return nil, err
		}
		v, err := validValue(m, s.Value.Shape, depth+1)
		if err != nil {
			return nil, err
		}
		return map[string]any{k.(string): v}, nil
	}
	return nil, fmt.Errorf("unsupported type %s for %s", s.Type, name)
}

// resize pads or truncates v to n characters and reports whether the result still
// matches the pattern.
func resize(s shape, v string, n int) (string, bool) {
	if n < 0 {
		return "", false
	}
	r := []rune(v)
	if len(r) == 0 {
		r = []rune("a")
	}
	for len(r) < n {
		r = append(r, r[len(r)-1])
	}
	out := string(r[:n])
	return out, n == 0 || matches(s, out)
}

func inRange(s shape, n int) bool {
	if s.Min != nil && float64(n) < *s.Min {
		return false
	}
	if s.Max != nil && float64(n) > *s.Max {
		return false
	}
	return true
}

func invalidPattern(s shape) (string, bool) {
	if s.Pattern == "" {
		return "", false
	}
	if _, err := compile(s.Pattern); err != nil {
		return "", false
	}
	for _, c := range invalidCandidates {
		n := len([]rune(c))
		if s.Min != nil && *s.Min > float64(n) {
			n = int(*s.Min)
		}
		v := strings.Repeat(string([]rune(c)[0]), n)
		if inRange(s, n) && !matches(s, v) {
			return v, true
		}
	}
	return "", false
}

func matches(s shape, v string) bool {
	if s.Pattern == "" {
		return true
	}
	re, err := compile(s.Pattern)
	if err != nil {
		return false
	}
	return re.MatchString(v)
}

var compiled = map[string]*regexp.Regexp{}

func compile(p string) (*regexp.Regexp, error) {
	if re, ok := compiled[p]; ok {
		return re, nil
	}
	re, err := regexp.Compile(`^(?:` + p + `)$`)
	if err != nil {
		return nil, err
	}
	compiled[p] = re
	return re, nil
}

func clone(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
