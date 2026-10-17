package cognito

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Messages verified against real AWS (issue #625).
func TestDecodeInput_SerializationErrors(t *testing.T) {
	const pool = `"UserPoolId":"us-east-1_abc"`
	tests := []struct {
		name string
		op   string
		body string
		want string
	}{
		{"malformed", "DescribeUserPool", `{`, ""},
		{"empty", "DescribeUserPool", ``, ""},
		{"trailing data", "DescribeUserPool", `{} x`, ""},
		{"null body", "DescribeUserPool", `null`, ""},
		{"string body", "DescribeUserPool", `"x"`, ""},
		{"array body", "DescribeUserPool", `[]`, msgUnexpectedList},

		{"number as string", "DescribeUserPool", `{"UserPoolId":123}`,
			"NUMBER_VALUE can not be converted to a String"},
		{"true as string", "DescribeUserPool", `{"UserPoolId":true}`,
			"TRUE_VALUE can not be converted to a String"},
		{"false as string", "DescribeUserPool", `{"UserPoolId":false}`,
			"FALSE_VALUE can not be converted to a String"},
		{"object as string", "DescribeUserPool", `{"UserPoolId":{"a":1}}`, msgUnexpectedStructure},
		{"array as string", "AdminGetUser", `{` + pool + `,"Username":["x"]}`, msgUnexpectedList},

		{"string as integer", "ListUsers", `{` + pool + `,"Limit":"5"}`,
			"STRING_VALUE can not be converted to an Integer"},
		{"boolean as integer", "ListUsers", `{` + pool + `,"Limit":true}`,
			"TRUE_VALUE can not be converted to an Integer"},

		{
			"number as boolean",
			"AdminCreateUser",
			`{` + pool + `,"Username":"u","ForceAliasCreation":1}`,
			"NUMBER_VALUE can not be converted to a Boolean",
		},
		{"other string as boolean", "AdminCreateUser",
			`{` + pool + `,"Username":"u","ForceAliasCreation":"on"}`,
			"STRING_VALUE can not be converted to an Boolean"},
		{"object as boolean", "AdminCreateUser",
			`{` + pool + `,"Username":"u","ForceAliasCreation":{}}`, msgUnexpectedStructure},

		{"scalar as structure", "UpdateUserPool", `{` + pool + `,"Policies":"x"}`, "Expected null"},
		{"array as structure", "UpdateUserPool", `{` + pool + `,"Policies":[]}`, msgUnexpectedList},
		{
			"scalar as map",
			"UpdateUserPool",
			`{` + pool + `,"UserPoolTags":1}`,
			"Expected map or null",
		},
		{"array as map", "UpdateUserPool", `{` + pool + `,"UserPoolTags":[]}`, msgUnexpectedList},
		{"scalar as list", "UpdateUserPool", `{` + pool + `,"AutoVerifiedAttributes":true}`,
			"Expected list or null"},
		{
			"object as list",
			"AdminUpdateUserAttributes",
			`{` + pool + `,"Username":"u","UserAttributes":{"Name":"email"}}`,
			msgUnexpectedStructure,
		},

		{"inside a list", "UpdateUserPool", `{` + pool + `,"AutoVerifiedAttributes":[1]}`,
			"NUMBER_VALUE can not be converted to a String"},
		{"inside a map", "UpdateUserPool", `{` + pool + `,"UserPoolTags":{"k":1}}`,
			"NUMBER_VALUE can not be converted to a String"},
		{"inside a nested structure", "UpdateUserPool",
			`{` + pool + `,"Policies":{"PasswordPolicy":{"RequireNumbers":1}}}`,
			"NUMBER_VALUE can not be converted to a Boolean"},
		{"first of duplicate members", "DescribeUserPool",
			`{"UserPoolId":1,"UserPoolId":"us-east-1_abc"}`,
			"NUMBER_VALUE can not be converted to a String"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, serr := decodeInput(inputRules[tt.op], []byte(tt.body))
			require.NotNil(t, serr)
			assert.Equal(t, tt.want, serr.message)
		})
	}
}

func TestDecodeInput_Reads(t *testing.T) {
	tests := []struct {
		name string
		op   string
		body string
		want map[string]any
	}{
		{
			name: "member name in another case is dropped",
			op:   "DescribeUserPool",
			body: `{"userpoolid":"us-east-1_a"}`,
			want: map[string]any{},
		},
		{
			name: "undeclared member is dropped whatever its type",
			op:   "ListUserPools",
			body: `{"MaxResults":1,"Other":{"a":[1]}}`,
			want: map[string]any{"MaxResults": int64(1)},
		},
		{
			name: "last of duplicate members wins",
			op:   "DescribeUserPool",
			body: `{"UserPoolId":"us-east-1_a","UserPoolId":"us-east-1_b"}`,
			want: map[string]any{"UserPoolId": "us-east-1_b"},
		},
		{
			name: "null member is absent",
			op:   "DescribeUserPool",
			body: `{"UserPoolId":"us-east-1_a","UserPoolId":null}`,
			want: map[string]any{},
		},
		{
			name: "nested members",
			op:   "AdminUpdateUserAttributes",
			body: `{"UserPoolId":"us-east-1_a","Username":"u",` +
				`"UserAttributes":[{"Name":"email","value":"x"},null],"ClientMetadata":{"k":null}}`,
			want: map[string]any{
				"UserPoolId":     "us-east-1_a",
				"Username":       "u",
				"UserAttributes": []any{map[string]any{"Name": "email"}, nil},
				"ClientMetadata": map[string]any{"k": nil},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input, serr := decodeInput(inputRules[tt.op], []byte(tt.body))
			require.Nil(t, serr)
			assert.Equal(t, tt.want, input)
		})
	}
}

func TestDecodeInput_IntegerCoercion(t *testing.T) {
	tests := []struct {
		raw  string
		want int64
	}{
		{"5", 5},
		{"5.5", 5},
		{"-5.5", -5},
		{"1e1", 10},
		{"99999999999", 2147483647},
		{"-99999999999", -2147483648},
		{"1e400", 2147483647},
	}
	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			input, serr := decodeInput(
				inputRules["ListUserPools"], []byte(`{"MaxResults":`+tt.raw+`}`),
			)
			require.Nil(t, serr)
			assert.Equal(t, tt.want, input["MaxResults"])
		})
	}
}

func TestDecodeInput_BooleanCoercion(t *testing.T) {
	tests := []struct {
		raw  string
		want bool
	}{
		{`true`, true}, {`false`, false},
		{`"true"`, true}, {`"TRUE"`, true}, {`"t"`, true}, {`"Yes"`, true}, {`"y"`, true},
		{`"1"`, true},
		{`"FaLsE"`, false}, {`"f"`, false}, {`"no"`, false}, {`"N"`, false}, {`"0"`, false},
	}
	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			input, serr := decodeInput(inputRules["AdminCreateUser"], []byte(
				`{"UserPoolId":"us-east-1_a","Username":"u","ForceAliasCreation":`+tt.raw+`}`,
			))
			require.Nil(t, serr)
			assert.Equal(t, tt.want, input["ForceAliasCreation"])
		})
	}
}

func TestRouter_SerializationException(t *testing.T) {
	ro := newTestRouter(t)
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			"with message",
			`{"UserPoolId":123}`,
			`{"__type":"SerializationException","Message":"NUMBER_VALUE can not be converted to a String"}`,
		},
		{"without message", `not-json`, `{"__type":"SerializationException"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := doOp(t, ro, "DescribeUserPool", tt.body)
			assert.Equal(t, http.StatusBadRequest, w.Code)
			assert.JSONEq(t, tt.want, w.Body.String())
		})
	}
}

func TestRouter_HandlersReceiveDecodedBody(t *testing.T) {
	ro := newTestRouter(t)
	poolID := createPool(t, ro, "pool")

	w := doOp(t, ro, "ListUserPools", `{"MaxResults":"x"}`)
	assertErrType(t, w, ErrTypeSerializationException)

	w = doOp(t, ro, "ListUserPools", `{"MaxResults":1.9,"Unknown":true}`)
	require.Equal(t, http.StatusOK, w.Code)
	var resp struct{ UserPools []json.RawMessage }
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Len(t, resp.UserPools, 1)

	w = doOp(t, ro, "DescribeUserPool", fmt.Sprintf(`{"UserPoolId":%q,"Extra":"<&>"}`, poolID))
	assert.Equal(t, http.StatusOK, w.Code)
}

// Every handler still guards its own decoding, though the router only passes bodies that
// decodeInput produced.
func TestRouter_DispatchRejectsUndecodableBody(t *testing.T) {
	ro := newTestRouter(t)
	for op := range inputRules {
		t.Run(op, func(t *testing.T) {
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(""))
			ro.dispatch(w, r, op, []byte("not-json"))
			assert.Equal(t, http.StatusBadRequest, w.Code)
			assertErrType(t, w, ErrTypeInvalidParameterException)
		})
	}
}
