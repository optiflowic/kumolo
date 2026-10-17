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
		// Verified against real AWS (issue #624): a missing UserPoolId is reported alone,
		// before any other constraint.
		{
			name: "UserPoolId missing",
			op:   "AdminGetUser",
			body: `{"Username":""}`,
			want: `UserPoolId must not be null`,
		},
		{
			name: "UserPoolId null",
			op:   "DescribeUserPool",
			body: `{"UserPoolId":null}`,
			want: `UserPoolId must not be null`,
		},
		{
			name: "UserPoolId whose last occurrence is null",
			op:   "DescribeUserPool",
			body: `{"UserPoolId":"us-east-1_abc","UserPoolId":null}`,
			want: `UserPoolId must not be null`,
		},
		{
			name: "required member without scalar constraints",
			op:   "TagResource",
			body: `{"ResourceArn":"arn:aws:cognito-idp:us-east-1:000000000000:userpool/us-east-1_a"}`,
			want: `1 validation error detected: Value null at 'tags' failed to satisfy ` +
				`constraint: Member must not be null`,
		},
		{
			// Verified against real AWS (issue #624).
			name: "sensitive member missing",
			op:   "GetUser",
			body: `{}`,
			want: `1 validation error detected: Value at 'accessToken' failed to satisfy ` +
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
			// Verified against real AWS (issue #624): length before pattern.
			name: "empty string breaks minimum length and pattern",
			op:   "DescribeUserPool",
			body: `{"UserPoolId":""}`,
			want: `2 validation errors detected: Value '' at 'userPoolId' failed to satisfy ` +
				`constraint: Member must have length greater than or equal to 1; ` +
				`Value '' at 'userPoolId' failed to satisfy constraint: ` +
				`Member must satisfy regular expression pattern: [\w-]+_[0-9a-zA-Z]+`,
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
			// Verified against real AWS (issue #624): enum values and members in Java
			// HashSet / HashMap order.
			name: "enums across members",
			op:   "UpdateUserPool",
			body: `{"UserPoolId":"us-east-1_abc","MfaConfiguration":"X","UserPoolTier":"X",` +
				`"DeletionProtection":"X"}`,
			want: `3 validation errors detected: ` +
				`Value 'X' at 'mfaConfiguration' failed to satisfy constraint: ` +
				`Member must satisfy enum value set: [OPTIONAL, OFF, ON]; ` +
				`Value 'X' at 'deletionProtection' failed to satisfy constraint: ` +
				`Member must satisfy enum value set: [ACTIVE, INACTIVE]; ` +
				`Value 'X' at 'userPoolTier' failed to satisfy constraint: ` +
				`Member must satisfy enum value set: [ESSENTIALS, LITE, PLUS]`,
		},
		{
			// Verified against real AWS (issue #624).
			name: "members in HashMap order, not alphabetical",
			op:   "AdminCreateUser",
			body: `{"MessageAction":"BOGUS","UserPoolId":"!!!","Username":"u"}`,
			want: `2 validation errors detected: ` +
				`Value 'BOGUS' at 'messageAction' failed to satisfy constraint: ` +
				`Member must satisfy enum value set: [SUPPRESS, RESEND]; ` +
				`Value '!!!' at 'userPoolId' failed to satisfy constraint: ` +
				`Member must satisfy regular expression pattern: [\w-]+_[0-9a-zA-Z]+`,
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
			name: "last of duplicate members",
			op:   "ListUserPools",
			body: `{"MaxResults":1,"MaxResults":0}`,
			want: `1 validation error detected: Value '0' at 'maxResults' failed to satisfy ` +
				`constraint: Member must have value greater than or equal to 1`,
		},
		{
			// Verified against real AWS (issue #624).
			name: "required members around a pattern violation",
			op:   "AdminAddUserToGroup",
			body: `{"UserPoolId":"!!!"}`,
			want: `3 validation errors detected: ` +
				`Value null at 'groupName' failed to satisfy constraint: Member must not be null; ` +
				`Value '!!!' at 'userPoolId' failed to satisfy constraint: ` +
				`Member must satisfy regular expression pattern: [\w-]+_[0-9a-zA-Z]+; ` +
				`Value at 'username' failed to satisfy constraint: Member must not be null`,
		},
		{
			// Verified against real AWS (issue #624).
			name: "list of structures",
			op:   "AdminUpdateUserAttributes",
			body: `{"UserPoolId":"us-east-1_abc","Username":"u","UserAttributes":` +
				`[{"Name":"` + strings.Repeat("a", 33) + `","Value":"x"},{"Value":"y"}]}`,
			want: `2 validation errors detected: Value '` + strings.Repeat("a", 33) +
				`' at 'userAttributes.1.member.name' failed to satisfy constraint: ` +
				`Member must have length less than or equal to 32; ` +
				`Value null at 'userAttributes.2.member.name' failed to satisfy constraint: ` +
				`Member must not be null`,
		},
		{
			// Verified against real AWS (issue #624).
			name: "list enum, nested range and map value",
			op:   "UpdateUserPool",
			body: `{"UserPoolId":"us-east-1_abc","AutoVerifiedAttributes":["bogus"],` +
				`"UserPoolTags":{"k":"` + strings.Repeat("v", 257) + `"},` +
				`"Policies":{"PasswordPolicy":{"MinimumLength":100}}}`,
			want: `3 validation errors detected: ` +
				`Value '[bogus]' at 'autoVerifiedAttributes' failed to satisfy constraint: ` +
				`Member must satisfy constraint: ` +
				`[Member must satisfy enum value set: [phone_number, email]]; ` +
				`Value '100' at 'policies.passwordPolicy.minimumLength' failed to satisfy ` +
				`constraint: Member must have value less than or equal to 99; ` +
				`Value '{k=` + strings.Repeat("v", 257) + `}' at 'userPoolTags' failed to ` +
				`satisfy constraint: Map value must satisfy constraint: ` +
				`[Member must have length less than or equal to 256, ` +
				`Member must have length greater than or equal to 0]`,
		},
		{
			// Verified against real AWS (issue #624), except the pattern: AWS prints
			// [\p{L}\p{M}\p{S}\p{N}\p{P}]+, while the published model has the one below.
			name: "list of strings with every member constraint",
			op:   "ListUsers",
			body: `{"UserPoolId":"us-east-1_abc","AttributesToGet":` +
				`["` + strings.Repeat("b", 34) + `","a",""]}`,
			want: `1 validation error detected: Value '[` + strings.Repeat("b", 34) +
				`, a, ]' at 'attributesToGet' failed to satisfy constraint: ` +
				`Member must satisfy constraint: ` +
				`[Member must have length less than or equal to 32, ` +
				`Member must have length greater than or equal to 1, ` +
				`Member must satisfy regular expression pattern: ` +
				`[\p{L}\p{M}\p{S}\p{N}\p{P}\t\n\r ]+]`,
		},
		{
			// Verified against real AWS (issue #624).
			name: "map keys and values",
			op:   "UpdateUserPool",
			body: `{"UserPoolId":"us-east-1_abc","UserPoolTags":{"` + strings.Repeat("k", 129) +
				`":"` + strings.Repeat("v", 257) + `"}}`,
			want: `2 validation errors detected: Value '{` + strings.Repeat("k", 129) + `=` +
				strings.Repeat("v", 257) + `}' at 'userPoolTags' failed to satisfy constraint: ` +
				`Map keys must satisfy constraint: ` +
				`[Member must have length less than or equal to 128, ` +
				`Member must have length greater than or equal to 1]; ` +
				`Value '{` + strings.Repeat("k", 129) + `=` + strings.Repeat("v", 257) +
				`}' at 'userPoolTags' failed to satisfy constraint: ` +
				`Map value must satisfy constraint: ` +
				`[Member must have length less than or equal to 256, ` +
				`Member must have length greater than or equal to 0]`,
		},
		{
			// Verified against real AWS (issue #624), except the enum: AWS lists 12 values,
			// three of them not in the published model.
			name: "list sizes",
			op:   "UpdateUserPoolClient",
			body: `{"UserPoolId":"us-east-1_abc","ClientId":"abcdefghijklmnopqrstuvwxyz",` +
				`"ExplicitAuthFlows":["BOGUS"],"AllowedOAuthFlows":["code","code","code","code"]}`,
			want: `2 validation errors detected: ` +
				`Value '[code, code, code, code]' at 'allowedOAuthFlows' failed to satisfy ` +
				`constraint: Member must have length less than or equal to 3; ` +
				`Value '[BOGUS]' at 'explicitAuthFlows' failed to satisfy constraint: ` +
				`Member must satisfy constraint: [Member must satisfy enum value set: ` +
				`[ALLOW_CUSTOM_AUTH, ALLOW_USER_PASSWORD_AUTH, ADMIN_NO_SRP_AUTH, ` +
				`USER_PASSWORD_AUTH, ALLOW_ADMIN_USER_PASSWORD_AUTH, ALLOW_USER_AUTH, ` +
				`CUSTOM_AUTH_FLOW_ONLY, ALLOW_USER_SRP_AUTH, ALLOW_REFRESH_TOKEN_AUTH]]`,
		},
		{
			name: "list with a null element",
			op:   "UpdateUserPoolClient",
			body: `{"UserPoolId":"us-east-1_abc","ClientId":"abcdefghijklmnopqrstuvwxyz",` +
				`"AllowedOAuthFlows":["code",null,"code","code"]}`,
			want: `1 validation error detected: Value '[code, null, code, code]' at ` +
				`'allowedOAuthFlows' failed to satisfy constraint: ` +
				`Member must have length less than or equal to 3`,
		},
		{
			// Verified against real AWS (issue #624).
			name: "empty list below minimum",
			op:   "UpdateUserPool",
			body: `{"UserPoolId":"us-east-1_abc","AccountRecoverySetting":{"RecoveryMechanisms":[]}}`,
			want: `1 validation error detected: Value '[]' at ` +
				`'accountRecoverySetting.recoveryMechanisms' failed to satisfy constraint: ` +
				`Member must have length greater than or equal to 1`,
		},
		{
			// Verified against real AWS (issue #624).
			name: "omitted integer inside a structure is 0",
			op:   "UpdateUserPool",
			body: `{"UserPoolId":"us-east-1_abc","AccountRecoverySetting":` +
				`{"RecoveryMechanisms":[{"Name":"verified_email"}]}}`,
			want: `1 validation error detected: Value '0' at ` +
				`'accountRecoverySetting.recoveryMechanisms.1.member.priority' failed to ` +
				`satisfy constraint: Member must have value greater than or equal to 1`,
		},
		{
			// Verified against real AWS (issue #624).
			name: "printed structure shows an omitted integer as 0",
			op:   "UpdateUserPool",
			body: `{"UserPoolId":"us-east-1_abc","AccountRecoverySetting":{"RecoveryMechanisms":[` +
				`{"Name":"verified_email","Priority":1},{"Name":"verified_phone_number"},` +
				`{"Name":"admin_only","Priority":2}]}}`,
			want: `2 validation errors detected: Value '[` +
				`RecoveryOptionType(priority=1, name=verified_email), ` +
				`RecoveryOptionType(priority=0, name=verified_phone_number), ` +
				`RecoveryOptionType(priority=2, name=admin_only)]' at ` +
				`'accountRecoverySetting.recoveryMechanisms' failed to satisfy constraint: ` +
				`Member must have length less than or equal to 2; ` +
				`Value '0' at 'accountRecoverySetting.recoveryMechanisms.2.member.priority' ` +
				`failed to satisfy constraint: Member must have value greater than or equal to 1`,
		},
		{
			// Verified against real AWS (issue #624).
			name: "empty password policy",
			op:   "UpdateUserPool",
			body: `{"UserPoolId":"us-east-1_abc","Policies":{"PasswordPolicy":{}}}`,
			want: `1 validation error detected: Value '0' at ` +
				`'policies.passwordPolicy.minimumLength' failed to satisfy constraint: ` +
				`Member must have value greater than or equal to 6`,
		},
		{
			// Verified against real AWS (issue #624).
			name: "map of structures",
			op:   "UpdateUserPool",
			body: `{"UserPoolId":"us-east-1_abc","AcrConfiguration":` +
				`{"Level1":{"AcrValue":"bad value"},"Level2":{}}}`,
			want: `2 validation errors detected: ` +
				`Value null at 'acrConfiguration.Level2.member.acrValue' failed to satisfy ` +
				`constraint: Member must not be null; ` +
				`Value 'bad value' at 'acrConfiguration.Level1.member.acrValue' failed to ` +
				`satisfy constraint: Member must satisfy regular expression pattern: ` +
				`[\x21\x23-\x5B\x5D-\x7E]+`,
		},
		{
			// Verified against real AWS (issue #624).
			name: "map size and keys with structure values",
			op:   "UpdateUserPool",
			body: `{"UserPoolId":"us-east-1_abc","AcrConfiguration":{"Level1":{"AcrValue":"a"},` +
				`"Level2":{"AcrValue":"b"},"Level3":{"AcrValue":"c"},"Level4":{"AcrValue":"d"},` +
				`"Level5":{"AcrValue":"e"}}}`,
			want: `2 validation errors detected: Value '{` +
				`Level4=AcrLevelConfigType(acrValue=d), Level5=AcrLevelConfigType(acrValue=e), ` +
				`Level2=AcrLevelConfigType(acrValue=b), Level3=AcrLevelConfigType(acrValue=c), ` +
				`Level1=AcrLevelConfigType(acrValue=a)}' at 'acrConfiguration' failed to ` +
				`satisfy constraint: Member must have length less than or equal to 4; ` +
				`Value '{` +
				`Level4=AcrLevelConfigType(acrValue=d), Level5=AcrLevelConfigType(acrValue=e), ` +
				`Level2=AcrLevelConfigType(acrValue=b), Level3=AcrLevelConfigType(acrValue=c), ` +
				`Level1=AcrLevelConfigType(acrValue=a)}' at 'acrConfiguration' failed to ` +
				`satisfy constraint: Map keys must satisfy constraint: ` +
				`[Member must satisfy regular expression pattern: Level[1-4]]`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg, ok := validateBody(t, tt.op, tt.body)
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
			body: `{"UserPoolId":null,"UserPoolId":"us-east-1_abc123XYZ"}`,
		},
		{
			name: "invalid value followed by a null duplicate",
			op:   "ListUserPoolClients",
			body: `{"UserPoolId":"us-east-1_abc","MaxResults":0,"MaxResults":null}`,
		},
		{
			name: "member name in another case",
			op:   "ListUserPools",
			body: `{"MaxResults":1,"maxresults":0}`,
		},
		{
			name: "member the model does not declare",
			op:   "ListUserPools",
			body: `{"MaxResults":1,"Other":{"a":[1]}}`,
		},
		{name: "integer with a fraction", op: "ListUserPools", body: `{"MaxResults":60.9}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg, ok := validateBody(t, tt.op, tt.body)
			assert.True(t, ok)
			assert.Empty(t, msg)
		})
	}
}

func validateBody(t *testing.T, op, body string) (string, bool) {
	t.Helper()
	in := inputRules[op]
	input, serr := decodeInput(in, []byte(body))
	require.Nil(t, serr)
	return validateInput(in, input)
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

// Handlers decode member names case-insensitively, so the router must hand them only the
// exact-case members decodeInput kept.
func TestRouter_IgnoresMemberNameInAnotherCase(t *testing.T) {
	ro := newTestRouter(t)
	poolID := createPool(t, ro, "pool")

	w := doOp(t, ro, "ListUserPools", `{"MaxResults":1,"maxResults":0}`)
	assert.Equal(t, http.StatusOK, w.Code)

	w = doOp(t, ro, "DescribeUserPool", fmt.Sprintf(`{"userPoolId":%q}`, poolID))
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeInvalidParameterException)
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

// Verified against real AWS (issue #624): omitted integers print as 0, every other
// omitted member as null.
func TestInputShape_RenderOmittedMembers(t *testing.T) {
	got := shapeSchemaAttributeType.render(map[string]any{"Name": "a"})
	assert.Equal(t, "SchemaAttributeType(name=a, attributeDataType=null, "+
		"developerOnlyAttribute=null, mutable=null, required=null, "+
		"numberAttributeConstraints=null, stringAttributeConstraints=null)", got)

	got = shapeRecoveryOptionType.render(map[string]any{"Name": "admin_only"})
	assert.Equal(t, "RecoveryOptionType(priority=0, name=admin_only)", got)
}
