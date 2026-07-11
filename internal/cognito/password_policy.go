package cognito

import (
	"encoding/json"
	"unicode"
	"unicode/utf8"
)

// passwordPolicy mirrors the subset of PasswordPolicyType that kumolo enforces.
type passwordPolicy struct {
	MinimumLength                 int  `json:"MinimumLength"`
	RequireUppercase              bool `json:"RequireUppercase"`
	RequireLowercase              bool `json:"RequireLowercase"`
	RequireNumbers                bool `json:"RequireNumbers"`
	RequireSymbols                bool `json:"RequireSymbols"`
	TemporaryPasswordValidityDays int  `json:"TemporaryPasswordValidityDays"`
}

type policiesType struct {
	PasswordPolicy passwordPolicy `json:"PasswordPolicy"`
}

// passwordPolicyOverlay mirrors passwordPolicy with pointer fields so that
// fields absent from a stored Policies blob can be distinguished from fields
// explicitly set to their zero value, allowing omitted fields to inherit
// AWS's default policy instead of being zeroed out.
type passwordPolicyOverlay struct {
	MinimumLength                 *int  `json:"MinimumLength"`
	RequireUppercase              *bool `json:"RequireUppercase"`
	RequireLowercase              *bool `json:"RequireLowercase"`
	RequireNumbers                *bool `json:"RequireNumbers"`
	RequireSymbols                *bool `json:"RequireSymbols"`
	TemporaryPasswordValidityDays *int  `json:"TemporaryPasswordValidityDays"`
}

type policiesOverlayType struct {
	PasswordPolicy passwordPolicyOverlay `json:"PasswordPolicy"`
}

// defaultPasswordPolicy matches AWS's default PasswordPolicy for a new user pool.
// This is the single source of truth for both DescribeUserPool's echoed Policies
// (see defaultPolicies in handler_user_pool.go) and password validation.
func defaultPasswordPolicy() passwordPolicy {
	return passwordPolicy{
		MinimumLength:                 minPasswordLen,
		RequireUppercase:              true,
		RequireLowercase:              true,
		RequireNumbers:                true,
		RequireSymbols:                true,
		TemporaryPasswordValidityDays: 7,
	}
}

// passwordPolicyFromPool extracts the pool's PasswordPolicy from its stored
// Policies blob, falling back to AWS's default policy when absent or malformed.
// Fields omitted from the stored blob inherit the default policy's value
// rather than being zeroed out.
func passwordPolicyFromPool(meta *UserPoolMetadata) passwordPolicy {
	policy := defaultPasswordPolicy()
	if meta == nil || meta.Policies == nil {
		return policy
	}
	var p policiesOverlayType
	if err := json.Unmarshal(meta.Policies, &p); err != nil {
		return policy
	}
	overlay := p.PasswordPolicy
	if overlay.MinimumLength != nil && *overlay.MinimumLength > 0 {
		policy.MinimumLength = *overlay.MinimumLength
	}
	if overlay.RequireUppercase != nil {
		policy.RequireUppercase = *overlay.RequireUppercase
	}
	if overlay.RequireLowercase != nil {
		policy.RequireLowercase = *overlay.RequireLowercase
	}
	if overlay.RequireNumbers != nil {
		policy.RequireNumbers = *overlay.RequireNumbers
	}
	if overlay.RequireSymbols != nil {
		policy.RequireSymbols = *overlay.RequireSymbols
	}
	if overlay.TemporaryPasswordValidityDays != nil {
		policy.TemporaryPasswordValidityDays = *overlay.TemporaryPasswordValidityDays
	}
	return policy
}

// validatePassword checks password against policy, returning the
// AWS-formatted InvalidPasswordException message and ok=false on the first
// violated rule, or ok=true when the password satisfies every rule.
func validatePassword(policy passwordPolicy, password string) (message string, ok bool) {
	minLen := policy.MinimumLength
	if minLen <= 0 {
		minLen = minPasswordLen
	}
	switch {
	case utf8.RuneCountInString(password) < minLen:
		return "Password did not conform with policy: Password not long enough", false
	case utf8.RuneCountInString(password) > maxPasswordLen:
		return "Password did not conform with policy: Password too long", false
	case policy.RequireUppercase && !containsRune(password, unicode.IsUpper):
		return "Password did not conform with policy: Password must have uppercase characters", false
	case policy.RequireLowercase && !containsRune(password, unicode.IsLower):
		return "Password did not conform with policy: Password must have lowercase characters", false
	case policy.RequireNumbers && !containsRune(password, unicode.IsNumber):
		return "Password did not conform with policy: Password must have numeric characters", false
	case policy.RequireSymbols && !containsRune(password, isSymbolRune):
		return "Password did not conform with policy: Password must have symbol characters", false
	}
	return "", true
}

func containsRune(s string, pred func(rune) bool) bool {
	for _, r := range s {
		if pred(r) {
			return true
		}
	}
	return false
}

func isSymbolRune(r rune) bool {
	return !unicode.IsLetter(r) && !unicode.IsNumber(r) && !unicode.IsSpace(r)
}
