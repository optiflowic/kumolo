package cognito

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
)

const (
	poolIDLen   = 9
	poolIDChars = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
)

var rePoolName = regexp.MustCompile(`^[\w\s+=,.@-]{1,128}$`)

func generatePoolID() (string, error) {
	const n = len(poolIDChars)
	const limit = byte((256 / n) * n) // reject values ≥ limit to eliminate modular bias
	b := make([]byte, poolIDLen)
	for i := range b {
		for {
			if _, err := rand.Read(b[i : i+1]); err != nil {
				// untestable: crypto/rand.Read only fails on OS-level entropy source errors
				return "", fmt.Errorf("read entropy: %w", err)
			}
			if b[i] < limit {
				b[i] = poolIDChars[b[i]%byte(n)]
				break
			}
		}
	}
	return poolRegion + "_" + string(b), nil
}

type schemaAttr struct {
	AttributeDataType          string          `json:"AttributeDataType"`
	DeveloperOnlyAttribute     bool            `json:"DeveloperOnlyAttribute"`
	Mutable                    bool            `json:"Mutable"`
	Name                       string          `json:"Name"`
	Required                   bool            `json:"Required"`
	StringAttributeConstraints *strConstraints `json:"StringAttributeConstraints,omitempty"`
	NumberAttributeConstraints *numConstraints `json:"NumberAttributeConstraints,omitempty"`
}

type strConstraints struct {
	MaxLength string `json:"MaxLength"`
	MinLength string `json:"MinLength"`
}

type numConstraints struct {
	MaxValue string `json:"MaxValue,omitempty"`
	MinValue string `json:"MinValue,omitempty"`
}

func standardSchemaAttrs() []schemaAttr {
	str := func(name string, req, mut bool, minLen, maxLen string) schemaAttr {
		return schemaAttr{
			AttributeDataType:          "String",
			Name:                       name,
			Required:                   req,
			Mutable:                    mut,
			StringAttributeConstraints: &strConstraints{MinLength: minLen, MaxLength: maxLen},
		}
	}
	return []schemaAttr{
		{
			AttributeDataType:          "String",
			Name:                       "sub",
			Required:                   true,
			Mutable:                    false,
			StringAttributeConstraints: &strConstraints{MinLength: "1", MaxLength: "2048"},
		},
		str("name", false, true, "0", "2048"),
		str("given_name", false, true, "0", "2048"),
		str("family_name", false, true, "0", "2048"),
		str("middle_name", false, true, "0", "2048"),
		str("nickname", false, true, "0", "2048"),
		str("preferred_username", false, true, "0", "2048"),
		str("profile", false, true, "0", "2048"),
		str("picture", false, true, "0", "2048"),
		str("website", false, true, "0", "2048"),
		str("email", false, true, "0", "2048"),
		{AttributeDataType: "Boolean", Name: "email_verified", Required: false, Mutable: true},
		str("gender", false, true, "0", "2048"),
		str("birthdate", false, true, "10", "10"),
		str("zoneinfo", false, true, "0", "2048"),
		str("locale", false, true, "0", "2048"),
		str("phone_number", false, true, "0", "2048"),
		{
			AttributeDataType: "Boolean",
			Name:              "phone_number_verified",
			Required:          false,
			Mutable:           true,
		},
		str("address", false, true, "0", "2048"),
		{
			AttributeDataType:          "Number",
			Name:                       "updated_at",
			Required:                   false,
			Mutable:                    true,
			NumberAttributeConstraints: &numConstraints{MinValue: "0"},
		},
	}
}

func buildSchemaAttributes(customSchema json.RawMessage) (json.RawMessage, error) {
	attrs := standardSchemaAttrs()
	if len(customSchema) > 0 {
		var custom []schemaAttr
		if err := json.Unmarshal(customSchema, &custom); err != nil {
			return nil, errors.New("invalid schema: value must be an array")
		}
		attrs = append(attrs, custom...)
	}
	data, err := json.Marshal(attrs)
	if err != nil {
		// unreachable: schemaAttr contains only plain types with no unencodable fields
		return nil, fmt.Errorf("encode schema: %w", err)
	}
	return json.RawMessage(data), nil
}

func defaultStr(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

func defaultPolicies() json.RawMessage {
	return json.RawMessage(
		`{"PasswordPolicy":{"MinimumLength":8,"RequireUppercase":true,"RequireLowercase":true,"RequireNumbers":true,"RequireSymbols":true,"TemporaryPasswordValidityDays":7}}`,
	)
}

func defaultAdminCreateUserConfig() json.RawMessage {
	return json.RawMessage(`{"AllowAdminCreateUserOnly":false,"UnusedAccountValidityDays":7}`)
}

type createUserPoolRequest struct {
	PoolName                    string            `json:"PoolName"`
	MfaConfiguration            string            `json:"MfaConfiguration"`
	DeletionProtection          string            `json:"DeletionProtection"`
	Policies                    json.RawMessage   `json:"Policies"`
	Schema                      json.RawMessage   `json:"Schema"`
	AliasAttributes             []string          `json:"AliasAttributes"`
	AutoVerifiedAttributes      []string          `json:"AutoVerifiedAttributes"`
	UsernameAttributes          []string          `json:"UsernameAttributes"`
	UsernameConfiguration       json.RawMessage   `json:"UsernameConfiguration"`
	LambdaConfig                json.RawMessage   `json:"LambdaConfig"`
	EmailConfiguration          json.RawMessage   `json:"EmailConfiguration"`
	SmsConfiguration            json.RawMessage   `json:"SmsConfiguration"`
	DeviceConfiguration         json.RawMessage   `json:"DeviceConfiguration"`
	AdminCreateUserConfig       json.RawMessage   `json:"AdminCreateUserConfig"`
	AccountRecoverySetting      json.RawMessage   `json:"AccountRecoverySetting"`
	UserAttributeUpdateSettings json.RawMessage   `json:"UserAttributeUpdateSettings"`
	UserPoolAddOns              json.RawMessage   `json:"UserPoolAddOns"`
	VerificationMessageTemplate json.RawMessage   `json:"VerificationMessageTemplate"`
	UserPoolTags                map[string]string `json:"UserPoolTags"`
	UserPoolTier                string            `json:"UserPoolTier"`
	EmailVerificationMessage    string            `json:"EmailVerificationMessage"`
	EmailVerificationSubject    string            `json:"EmailVerificationSubject"`
	SmsAuthenticationMessage    string            `json:"SmsAuthenticationMessage"`
	SmsVerificationMessage      string            `json:"SmsVerificationMessage"`
}

type updateUserPoolRequest struct {
	UserPoolId                  string            `json:"UserPoolId"`
	PoolName                    string            `json:"PoolName"`
	MfaConfiguration            string            `json:"MfaConfiguration"`
	DeletionProtection          string            `json:"DeletionProtection"`
	Policies                    json.RawMessage   `json:"Policies"`
	AutoVerifiedAttributes      []string          `json:"AutoVerifiedAttributes"`
	LambdaConfig                json.RawMessage   `json:"LambdaConfig"`
	EmailConfiguration          json.RawMessage   `json:"EmailConfiguration"`
	SmsConfiguration            json.RawMessage   `json:"SmsConfiguration"`
	DeviceConfiguration         json.RawMessage   `json:"DeviceConfiguration"`
	AdminCreateUserConfig       json.RawMessage   `json:"AdminCreateUserConfig"`
	AccountRecoverySetting      json.RawMessage   `json:"AccountRecoverySetting"`
	UserAttributeUpdateSettings json.RawMessage   `json:"UserAttributeUpdateSettings"`
	UserPoolAddOns              json.RawMessage   `json:"UserPoolAddOns"`
	VerificationMessageTemplate json.RawMessage   `json:"VerificationMessageTemplate"`
	UserPoolTags                map[string]string `json:"UserPoolTags"`
	UserPoolTier                string            `json:"UserPoolTier"`
	EmailVerificationMessage    string            `json:"EmailVerificationMessage"`
	EmailVerificationSubject    string            `json:"EmailVerificationSubject"`
	SmsAuthenticationMessage    string            `json:"SmsAuthenticationMessage"`
	SmsVerificationMessage      string            `json:"SmsVerificationMessage"`
}

func (ro *Router) handleCreateUserPool(w http.ResponseWriter, body []byte) {
	var req createUserPoolRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(
			w,
			http.StatusBadRequest,
			ErrTypeInvalidParameterException,
			"invalid request body",
		)
		return
	}
	if req.PoolName == "" {
		writeError(
			w,
			http.StatusBadRequest,
			ErrTypeInvalidParameterException,
			"PoolName is required",
		)
		return
	}
	if !rePoolName.MatchString(req.PoolName) {
		writeError(
			w,
			http.StatusBadRequest,
			ErrTypeInvalidParameterException,
			"PoolName must be 1-128 characters and match pattern [\\w\\s+=,.@-]+",
		)
		return
	}

	poolID, err := generatePoolID()
	if err != nil {
		// untestable: crypto/rand.Read only fails on OS-level entropy source errors
		writeError(
			w,
			http.StatusInternalServerError,
			ErrTypeInternalErrorException,
			"failed to generate pool ID",
		)
		return
	}

	schemaAttrs, err := buildSchemaAttributes(req.Schema)
	if err != nil {
		writeError(w, http.StatusBadRequest, ErrTypeInvalidParameterException, err.Error())
		return
	}

	policies := req.Policies
	if policies == nil {
		policies = defaultPolicies()
	}
	adminCreateUserConfig := req.AdminCreateUserConfig
	if adminCreateUserConfig == nil {
		adminCreateUserConfig = defaultAdminCreateUserConfig()
	}

	ts := nowUnix()
	meta := &UserPoolMetadata{
		ID:                          poolID,
		Name:                        req.PoolName,
		Arn:                         poolARN(poolID),
		Status:                      "Active",
		CreationDate:                ts,
		LastModifiedDate:            ts,
		MfaConfiguration:            defaultStr(req.MfaConfiguration, "OFF"),
		DeletionProtection:          req.DeletionProtection,
		Policies:                    policies,
		SchemaAttributes:            schemaAttrs,
		AliasAttributes:             req.AliasAttributes,
		AutoVerifiedAttributes:      req.AutoVerifiedAttributes,
		UsernameAttributes:          req.UsernameAttributes,
		UsernameConfiguration:       req.UsernameConfiguration,
		LambdaConfig:                req.LambdaConfig,
		EmailConfiguration:          req.EmailConfiguration,
		SmsConfiguration:            req.SmsConfiguration,
		DeviceConfiguration:         req.DeviceConfiguration,
		AdminCreateUserConfig:       adminCreateUserConfig,
		AccountRecoverySetting:      req.AccountRecoverySetting,
		UserAttributeUpdateSettings: req.UserAttributeUpdateSettings,
		UserPoolAddOns:              req.UserPoolAddOns,
		VerificationMessageTemplate: req.VerificationMessageTemplate,
		UserPoolTags:                req.UserPoolTags,
		UserPoolTier:                defaultStr(req.UserPoolTier, "ESSENTIALS"),
		EmailVerificationMessage:    req.EmailVerificationMessage,
		EmailVerificationSubject:    req.EmailVerificationSubject,
		SmsAuthenticationMessage:    req.SmsAuthenticationMessage,
		SmsVerificationMessage:      req.SmsVerificationMessage,
	}

	if err := ro.storage.CreateUserPool(meta); err != nil {
		writeError(
			w,
			http.StatusInternalServerError,
			ErrTypeInternalErrorException,
			"failed to create user pool",
		)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"UserPool": meta})
}

func (ro *Router) handleDescribeUserPool(w http.ResponseWriter, body []byte) {
	var req struct {
		UserPoolId string `json:"UserPoolId"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(
			w,
			http.StatusBadRequest,
			ErrTypeInvalidParameterException,
			"invalid request body",
		)
		return
	}
	if req.UserPoolId == "" {
		writeError(
			w,
			http.StatusBadRequest,
			ErrTypeInvalidParameterException,
			"UserPoolId is required",
		)
		return
	}

	meta, err := ro.storage.GetUserPool(req.UserPoolId)
	if err != nil {
		if errors.Is(err, errUserPoolNotFound) {
			writeError(
				w,
				http.StatusBadRequest,
				ErrTypeResourceNotFoundException,
				"User pool not found.",
			)
			return
		}
		writeError(
			w,
			http.StatusInternalServerError,
			ErrTypeInternalErrorException,
			"failed to get user pool",
		)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"UserPool": meta})
}

func (ro *Router) handleUpdateUserPool(w http.ResponseWriter, body []byte) {
	var req updateUserPoolRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(
			w,
			http.StatusBadRequest,
			ErrTypeInvalidParameterException,
			"invalid request body",
		)
		return
	}
	if req.UserPoolId == "" {
		writeError(
			w,
			http.StatusBadRequest,
			ErrTypeInvalidParameterException,
			"UserPoolId is required",
		)
		return
	}

	err := ro.storage.UpdateUserPool(req.UserPoolId, func(meta *UserPoolMetadata) error {
		applyPoolUpdate(meta, &req)
		return nil
	})
	if err != nil {
		if errors.Is(err, errUserPoolNotFound) {
			writeError(
				w,
				http.StatusBadRequest,
				ErrTypeResourceNotFoundException,
				"User pool not found.",
			)
			return
		}
		writeError(
			w,
			http.StatusInternalServerError,
			ErrTypeInternalErrorException,
			"failed to update user pool",
		)
		return
	}
	writeEmpty(w)
}

func applyPoolUpdate(meta *UserPoolMetadata, req *updateUserPoolRequest) {
	if req.PoolName != "" {
		meta.Name = req.PoolName
	}
	if req.MfaConfiguration != "" {
		meta.MfaConfiguration = req.MfaConfiguration
	}
	if req.DeletionProtection != "" {
		meta.DeletionProtection = req.DeletionProtection
	}
	if req.Policies != nil {
		meta.Policies = req.Policies
	}
	if req.AutoVerifiedAttributes != nil {
		meta.AutoVerifiedAttributes = req.AutoVerifiedAttributes
	}
	if req.LambdaConfig != nil {
		meta.LambdaConfig = req.LambdaConfig
	}
	if req.EmailConfiguration != nil {
		meta.EmailConfiguration = req.EmailConfiguration
	}
	if req.SmsConfiguration != nil {
		meta.SmsConfiguration = req.SmsConfiguration
	}
	if req.DeviceConfiguration != nil {
		meta.DeviceConfiguration = req.DeviceConfiguration
	}
	if req.AdminCreateUserConfig != nil {
		meta.AdminCreateUserConfig = req.AdminCreateUserConfig
	}
	if req.AccountRecoverySetting != nil {
		meta.AccountRecoverySetting = req.AccountRecoverySetting
	}
	if req.UserAttributeUpdateSettings != nil {
		meta.UserAttributeUpdateSettings = req.UserAttributeUpdateSettings
	}
	if req.UserPoolAddOns != nil {
		meta.UserPoolAddOns = req.UserPoolAddOns
	}
	if req.VerificationMessageTemplate != nil {
		meta.VerificationMessageTemplate = req.VerificationMessageTemplate
	}
	if req.UserPoolTags != nil {
		meta.UserPoolTags = req.UserPoolTags
	}
	if req.UserPoolTier != "" {
		meta.UserPoolTier = req.UserPoolTier
	}
	if req.EmailVerificationMessage != "" {
		meta.EmailVerificationMessage = req.EmailVerificationMessage
	}
	if req.EmailVerificationSubject != "" {
		meta.EmailVerificationSubject = req.EmailVerificationSubject
	}
	if req.SmsAuthenticationMessage != "" {
		meta.SmsAuthenticationMessage = req.SmsAuthenticationMessage
	}
	if req.SmsVerificationMessage != "" {
		meta.SmsVerificationMessage = req.SmsVerificationMessage
	}
}

func (ro *Router) handleGetUserPoolMfaConfig(w http.ResponseWriter, body []byte) {
	var req struct {
		UserPoolId string `json:"UserPoolId"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(
			w,
			http.StatusBadRequest,
			ErrTypeInvalidParameterException,
			"invalid request body",
		)
		return
	}
	if req.UserPoolId == "" {
		writeError(
			w,
			http.StatusBadRequest,
			ErrTypeInvalidParameterException,
			"UserPoolId is required",
		)
		return
	}

	meta, err := ro.storage.GetUserPool(req.UserPoolId)
	if err != nil {
		if errors.Is(err, errUserPoolNotFound) {
			writeError(
				w,
				http.StatusBadRequest,
				ErrTypeResourceNotFoundException,
				"User pool not found.",
			)
			return
		}
		writeError(
			w,
			http.StatusInternalServerError,
			ErrTypeInternalErrorException,
			"failed to get user pool",
		)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"MfaConfiguration":              meta.MfaConfiguration,
		"SoftwareTokenMfaConfiguration": map[string]any{"Enabled": false},
	})
}

func (ro *Router) handleDeleteUserPool(w http.ResponseWriter, body []byte) {
	var req struct {
		UserPoolId string `json:"UserPoolId"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(
			w,
			http.StatusBadRequest,
			ErrTypeInvalidParameterException,
			"invalid request body",
		)
		return
	}
	if req.UserPoolId == "" {
		writeError(
			w,
			http.StatusBadRequest,
			ErrTypeInvalidParameterException,
			"UserPoolId is required",
		)
		return
	}

	if err := ro.storage.DeleteUserPool(req.UserPoolId); err != nil {
		if errors.Is(err, errUserPoolNotFound) {
			writeError(
				w,
				http.StatusBadRequest,
				ErrTypeResourceNotFoundException,
				"User pool not found.",
			)
			return
		}
		writeError(
			w,
			http.StatusInternalServerError,
			ErrTypeInternalErrorException,
			"failed to delete user pool",
		)
		return
	}
	writeEmpty(w)
}

type userPoolDescription struct {
	ID               string          `json:"Id"`
	Name             string          `json:"Name"`
	CreationDate     float64         `json:"CreationDate"`
	LastModifiedDate float64         `json:"LastModifiedDate"`
	LambdaConfig     json.RawMessage `json:"LambdaConfig,omitempty"`
	Status           string          `json:"Status"`
}

func (ro *Router) handleListUserPools(w http.ResponseWriter, body []byte) {
	var req struct {
		MaxResults *int   `json:"MaxResults"`
		NextToken  string `json:"NextToken"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(
			w,
			http.StatusBadRequest,
			ErrTypeInvalidParameterException,
			"invalid request body",
		)
		return
	}
	if req.MaxResults == nil {
		writeError(
			w,
			http.StatusBadRequest,
			ErrTypeInvalidParameterException,
			"MaxResults is required",
		)
		return
	}
	if *req.MaxResults < 1 || *req.MaxResults > 60 {
		writeError(w, http.StatusBadRequest, ErrTypeInvalidParameterException,
			"MaxResults must be between 1 and 60")
		return
	}

	pools, nextToken, err := ro.storage.ListUserPools(*req.MaxResults, req.NextToken)
	if err != nil {
		if errors.Is(err, errInvalidNextToken) {
			writeError(
				w,
				http.StatusBadRequest,
				ErrTypeInvalidParameterException,
				"Invalid pagination token.",
			)
			return
		}
		writeError(
			w,
			http.StatusInternalServerError,
			ErrTypeInternalErrorException,
			"failed to list user pools",
		)
		return
	}

	descs := make([]userPoolDescription, 0, len(pools))
	for _, p := range pools {
		descs = append(descs, userPoolDescription{
			ID:               p.ID,
			Name:             p.Name,
			CreationDate:     p.CreationDate,
			LastModifiedDate: p.LastModifiedDate,
			LambdaConfig:     p.LambdaConfig,
			Status:           p.Status,
		})
	}

	resp := map[string]any{"UserPools": descs}
	if nextToken != "" {
		resp["NextToken"] = nextToken
	}
	writeJSON(w, http.StatusOK, resp)
}
