package cognito

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateInput_Violations(t *testing.T) {
	tests := []struct {
		name string
		op   string
		body string
		want string
	}{
		{
			// Message verified against real AWS (issue #609).
			name: "pattern",
			op:   "DescribeUserPool",
			body: `{"UserPoolId":"!!!"}`,
			want: `1 validation error detected: Value '!!!' at 'userPoolId' failed to satisfy ` +
				`constraint: Member must satisfy regular expression pattern: [\w-]+_[0-9a-zA-Z]+`,
		},
		{
			// Message verified against real AWS (issue #609).
			name: "sensitive member hides the value",
			op:   "GetUser",
			body: `{"AccessToken":"!!!"}`,
			want: `1 validation error detected: Value at 'accessToken' failed to satisfy ` +
				`constraint: Member must satisfy regular expression pattern: [A-Za-z0-9-_=.]+`,
		},
		{
			name: "required member missing",
			op:   "DescribeUserPool",
			body: `{}`,
			want: `1 validation error detected: Value null at 'userPoolId' failed to satisfy ` +
				`constraint: Member must not be null`,
		},
		{
			name: "required member null",
			op:   "DescribeUserPool",
			body: `{"UserPoolId":null}`,
			want: `1 validation error detected: Value null at 'userPoolId' failed to satisfy ` +
				`constraint: Member must not be null`,
		},
		{
			name: "required member without scalar constraints",
			op:   "TagResource",
			body: `{"ResourceArn":"arn:aws:cognito-idp:us-east-1:000000000000:userpool/us-east-1_a"}`,
			want: `1 validation error detected: Value null at 'tags' failed to satisfy ` +
				`constraint: Member must not be null`,
		},
		{
			name: "too long",
			op:   "DescribeUserPool",
			body: fmt.Sprintf(`{"UserPoolId":"us-east-1_%s"}`, strings.Repeat("a", 46)),
			want: `1 validation error detected: Value 'us-east-1_` + strings.Repeat("a", 46) +
				`' at 'userPoolId' failed to satisfy constraint: ` +
				`Member must have length less than or equal to 55`,
		},
		{
			name: "empty string breaks pattern and minimum length",
			op:   "DescribeUserPool",
			body: `{"UserPoolId":""}`,
			want: `2 validation errors detected: Value '' at 'userPoolId' failed to satisfy ` +
				`constraint: Member must satisfy regular expression pattern: [\w-]+_[0-9a-zA-Z]+; ` +
				`Value '' at 'userPoolId' failed to satisfy constraint: ` +
				`Member must have length greater than or equal to 1`,
		},
		{
			name: "length counts characters, not bytes",
			op:   "CreateUserPool",
			body: fmt.Sprintf(
				`{"PoolName":"pool","EmailVerificationSubject":%q}`,
				strings.Repeat("あ", 141),
			),
			want: `1 validation error detected: Value '` + strings.Repeat("あ", 141) +
				`' at 'emailVerificationSubject' failed to satisfy constraint: ` +
				`Member must have length less than or equal to 140`,
		},
		{
			name: "enum",
			op:   "SetUserPoolMfaConfig",
			body: `{"UserPoolId":"us-east-1_abc","MfaConfiguration":"MAYBE"}`,
			want: `1 validation error detected: Value 'MAYBE' at 'mfaConfiguration' failed to ` +
				`satisfy constraint: Member must satisfy enum value set: [OFF, ON, OPTIONAL]`,
		},
		{
			name: "integer above maximum",
			op:   "ListUserPools",
			body: `{"MaxResults":61}`,
			want: `1 validation error detected: Value '61' at 'maxResults' failed to satisfy ` +
				`constraint: Member must have value less than or equal to 60`,
		},
		{
			name: "integer below minimum",
			op:   "ListUserPools",
			body: `{"MaxResults":0}`,
			want: `1 validation error detected: Value '0' at 'maxResults' failed to satisfy ` +
				`constraint: Member must have value greater than or equal to 1`,
		},
		{
			name: "member name in another case",
			op:   "ListUserPools",
			body: `{"maxresults":0}`,
			want: `1 validation error detected: Value '0' at 'maxResults' failed to satisfy ` +
				`constraint: Member must have value greater than or equal to 1`,
		},
		{
			name: "duplicate member in another case",
			op:   "ListUserPools",
			body: `{"MaxResults":1,"maxResults":0}`,
			want: `1 validation error detected: Value '0' at 'maxResults' failed to satisfy ` +
				`constraint: Member must have value greater than or equal to 1`,
		},
		{
			name: "invalid value followed by a null duplicate",
			op:   "ListUserPoolClients",
			body: `{"UserPoolId":"us-east-1_abc","MaxResults":0,"MaxResults":null}`,
			want: `1 validation error detected: Value '0' at 'maxResults' failed to satisfy ` +
				`constraint: Member must have value greater than or equal to 1`,
		},
		{
			name: "required member whose last occurrence is null",
			op:   "DescribeUserPool",
			body: `{"UserPoolId":"us-east-1_abc","UserPoolId":null}`,
			want: `1 validation error detected: Value null at 'userPoolId' failed to satisfy ` +
				`constraint: Member must not be null`,
		},
		{
			name: "one entry per violating member",
			op:   "AdminGetUser",
			body: `{"UserPoolId":"!!!"}`,
			want: `2 validation errors detected: Value '!!!' at 'userPoolId' failed to satisfy ` +
				`constraint: Member must satisfy regular expression pattern: [\w-]+_[0-9a-zA-Z]+; ` +
				`Value null at 'username' failed to satisfy constraint: Member must not be null`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg, ok := validateInput(tt.op, []byte(tt.body))
			assert.False(t, ok)
			assert.Equal(t, tt.want, msg)
		})
	}
}

func TestValidateInput_Passes(t *testing.T) {
	tests := []struct {
		name string
		op   string
		body string
	}{
		{name: "valid", op: "DescribeUserPool", body: `{"UserPoolId":"us-east-1_abc123XYZ"}`},
		{name: "optional member omitted", op: "ListUserPools", body: `{"MaxResults":60}`},
		{
			name: "optional member null",
			op:   "ListUserPools",
			body: `{"MaxResults":1,"NextToken":null}`,
		},
		{name: "length at the limit", op: "CreateUserPool", body: fmt.Sprintf(
			`{"PoolName":"pool","EmailVerificationSubject":%q}`, strings.Repeat("あ", 140),
		)},
		{
			name: "null followed by a valid duplicate",
			op:   "DescribeUserPool",
			body: `{"UserPoolId":null,"userPoolId":"us-east-1_abc123XYZ"}`,
		},
		{
			name: "member the model does not constrain",
			op:   "ListUserPools",
			body: `{"MaxResults":1,"Other":{"a":[1]}}`,
		},
		{name: "unknown operation", op: "NoSuchOperation", body: `{"UserPoolId":"!!!"}`},
		{name: "body is not JSON", op: "DescribeUserPool", body: `not-json`},
		{name: "body is not an object", op: "DescribeUserPool", body: `[]`},
		{name: "string member of another type", op: "DescribeUserPool", body: `{"UserPoolId":42}`},
		{name: "integer member of another type", op: "ListUserPools", body: `{"MaxResults":"x"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg, ok := validateInput(tt.op, []byte(tt.body))
			assert.True(t, ok)
			assert.Empty(t, msg)
		})
	}
}

// Members of the wrong JSON type pass validateInput and are rejected by the handler.
func TestRouter_WrongMemberTypeIsRejectedByHandler(t *testing.T) {
	ro := newTestRouter(t)
	for op, body := range map[string]string{
		"DescribeUserPool": `{"UserPoolId":42}`,
		"ListUserPools":    `{"MaxResults":"x"}`,
	} {
		w := doOp(t, ro, op, body)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assertErrType(t, w, ErrTypeInvalidParameterException)
	}
}

func TestRouter_ValidatesInputBeforeResourceLookup(t *testing.T) {
	ro := newTestRouter(t)
	tests := []struct {
		name string
		op   string
		body string
	}{
		{
			name: "pool does not exist",
			op:   "AdminGetUser",
			body: `{"UserPoolId":"us-east-1_NOTEXIST","Username":"a b"}`,
		},
		{
			name: "client does not exist",
			op:   "SignUp",
			body: `{"ClientId":"abcdefghijklmnopqrstuvwxyz","Username":"a b","Password":"Passw0rd!"}`,
		},
		{
			name: "token is not a JWT",
			op:   "ChangePassword",
			body: `{"AccessToken":"abc.def.ghi","ProposedPassword":"a b"}`,
		},
		{
			name: "session is not a JWT",
			op:   "VerifySoftwareToken",
			body: `{"Session":"invalid-session-token","UserCode":"12345"}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := doOp(t, ro, tt.op, tt.body)
			assert.Equal(t, http.StatusBadRequest, w.Code)

			var resp errResponse
			require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
			assert.Equal(t, ErrTypeInvalidParameterException, resp.Type)
			assert.Contains(t, resp.Message, "validation error detected")
		})
	}
}

// Handlers decode member names case-insensitively, so a differently-cased duplicate must
// not carry an unvalidated value past validateInput.
func TestRouter_CaseVariantMemberCannotBypassValidation(t *testing.T) {
	ro := newTestRouter(t)
	poolID := createPool(t, ro, "pool")
	for op, body := range map[string]string{
		"ListUserPools":       `{"MaxResults":1,"maxResults":0}`,
		"ListUserPoolClients": fmt.Sprintf(`{"UserPoolId":%q,"maxResults":0}`, poolID),
		"DescribeUserPoolClient": fmt.Sprintf(
			`{"UserPoolId":%q,"ClientId":"abcdefghijklmnopqrstuvwxyz","userPoolId":"../x"}`, poolID,
		),
	} {
		t.Run(op, func(t *testing.T) {
			w := doOp(t, ro, op, body)
			assert.Equal(t, http.StatusBadRequest, w.Code)
			assertErrType(t, w, ErrTypeInvalidParameterException)
		})
	}
}

// A new operation needs `make genvalidation`; without it the router would dispatch
// the operation with no input validation at all.
func TestInputRules_CoverEveryDispatchedOperation(t *testing.T) {
	src, err := os.ReadFile("router.go")
	require.NoError(t, err)
	ops := regexp.MustCompile(`case "(\w+)":`).FindAllSubmatch(src, -1)
	require.NotEmpty(t, ops)
	for _, m := range ops {
		assert.Contains(t, inputRules, string(m[1]))
	}
	assert.Len(t, inputRules, len(ops))
}

// The SRP session carries the client's A and the sealed server secret, which makes it
// the longest session kumolo issues. It must stay within the model's Session limit or
// validateInput would reject kumolo's own challenge response.
func TestSessionType_FitsLongestIssuedSession(t *testing.T) {
	ro := newTestRouter(t)
	_, clientID := setupPool(t, ro)
	username := strings.Repeat("u", int(*shapeUsernameType.max))
	signUpUser(t, ro, clientID, username, "Password123!")
	confirmUser(t, ro, clientID, username)

	_, resp := initSRPChallenge(t, ro, clientID, username)
	assert.LessOrEqual(t, int64(len(resp.Session)), *shapeSessionType.max)
}
