package cognito

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidatePassword(t *testing.T) {
	fullPolicy := defaultPasswordPolicy()

	tests := []struct {
		name     string
		policy   passwordPolicy
		password string
		wantOK   bool
		wantMsg  string
	}{
		{
			name:     "satisfies full policy",
			policy:   fullPolicy,
			password: "Valid1Pass!",
			wantOK:   true,
		},
		{
			name:     "too short",
			policy:   fullPolicy,
			password: "Sh0rt!",
			wantOK:   false,
			wantMsg:  "Password did not conform with policy: Password not long enough",
		},
		{
			name:     "missing uppercase",
			policy:   fullPolicy,
			password: "lowercase1!",
			wantOK:   false,
			wantMsg:  "Password did not conform with policy: Password must have uppercase characters",
		},
		{
			name:     "missing lowercase",
			policy:   fullPolicy,
			password: "UPPERCASE1!",
			wantOK:   false,
			wantMsg:  "Password did not conform with policy: Password must have lowercase characters",
		},
		{
			name:     "missing number",
			policy:   fullPolicy,
			password: "NoNumbersHere!",
			wantOK:   false,
			wantMsg:  "Password did not conform with policy: Password must have numeric characters",
		},
		{
			name:     "missing symbol",
			policy:   fullPolicy,
			password: "NoSymbolsHere1",
			wantOK:   false,
			wantMsg:  "Password did not conform with policy: Password must have symbol characters",
		},
		{
			name: "relaxed policy allows simple password",
			policy: passwordPolicy{
				MinimumLength: 4,
			},
			password: "abcd",
			wantOK:   true,
		},
		{
			name: "zero minimum length falls back to default",
			policy: passwordPolicy{
				MinimumLength: 0,
			},
			password: "short",
			wantOK:   false,
			wantMsg:  "Password did not conform with policy: Password not long enough",
		},
		{
			name:   "minimum length counted by runes not bytes",
			policy: fullPolicy,
			// 5 runes / 15 UTF-8 bytes: below the 8-rune minimum despite exceeding 8 bytes.
			password: "パスワード",
			wantOK:   false,
			wantMsg:  "Password did not conform with policy: Password not long enough",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg, ok := validatePassword(tt.policy, tt.password)
			assert.Equal(t, tt.wantOK, ok)
			if !tt.wantOK {
				assert.Equal(t, tt.wantMsg, msg)
			}
		})
	}
}

func TestPasswordPolicyFromPool(t *testing.T) {
	t.Run("nil pool falls back to default", func(t *testing.T) {
		assert.Equal(t, defaultPasswordPolicy(), passwordPolicyFromPool(nil))
	})

	t.Run("nil Policies falls back to default", func(t *testing.T) {
		meta := &UserPoolMetadata{}
		assert.Equal(t, defaultPasswordPolicy(), passwordPolicyFromPool(meta))
	})

	t.Run("malformed Policies falls back to default", func(t *testing.T) {
		meta := &UserPoolMetadata{Policies: json.RawMessage(`not-json`)}
		assert.Equal(t, defaultPasswordPolicy(), passwordPolicyFromPool(meta))
	})

	t.Run("custom policy is honored", func(t *testing.T) {
		meta := &UserPoolMetadata{
			Policies: json.RawMessage(
				`{"PasswordPolicy":{"MinimumLength":12,"RequireUppercase":false,` +
					`"RequireLowercase":true,"RequireNumbers":false,"RequireSymbols":false}}`,
			),
		}
		want := passwordPolicy{
			MinimumLength:                 12,
			RequireUppercase:              false,
			RequireLowercase:              true,
			RequireNumbers:                false,
			RequireSymbols:                false,
			TemporaryPasswordValidityDays: 7, // omitted from Policies, inherits default
		}
		assert.Equal(t, want, passwordPolicyFromPool(meta))
	})

	t.Run("non-positive MinimumLength falls back to default minimum", func(t *testing.T) {
		meta := &UserPoolMetadata{
			Policies: json.RawMessage(
				`{"PasswordPolicy":{"MinimumLength":0,"RequireSymbols":true}}`,
			),
		}
		want := defaultPasswordPolicy()
		assert.Equal(t, want, passwordPolicyFromPool(meta))
	})

	t.Run("Policies present without PasswordPolicy key falls back to default", func(t *testing.T) {
		meta := &UserPoolMetadata{Policies: json.RawMessage(`{}`)}
		assert.Equal(t, defaultPasswordPolicy(), passwordPolicyFromPool(meta))
	})

	t.Run("Policies present with unrelated fields only falls back to default", func(t *testing.T) {
		meta := &UserPoolMetadata{Policies: json.RawMessage(`{"PasswordHistorySize":4}`)}
		assert.Equal(t, defaultPasswordPolicy(), passwordPolicyFromPool(meta))
	})

	t.Run("partial PasswordPolicy inherits default for omitted fields", func(t *testing.T) {
		meta := &UserPoolMetadata{
			Policies: json.RawMessage(`{"PasswordPolicy":{"RequireSymbols":false}}`),
		}
		want := defaultPasswordPolicy()
		want.RequireSymbols = false
		assert.Equal(t, want, passwordPolicyFromPool(meta))
	})
}
