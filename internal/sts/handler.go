package sts

import (
	"encoding/xml"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

const (
	xmlns     = "https://sts.amazonaws.com/doc/2011-06-15/"
	requestID = "00000000-0000-0000-0000-000000000000"

	assumeRoleMinDuration      = 900
	assumeRoleMaxDuration      = 43200
	getSessionTokenMinDuration = 900
	getSessionTokenMaxDuration = 129600

	fixedAccount      = "000000000000"
	fixedUserID       = "000000000000"
	fixedARN          = "arn:aws:iam::000000000000:root"
	fixedAccessKeyID  = "AKIAIOSFODNN7EXAMPLE"                     // #nosec G101 -- well-known AWS docs example key, not a real credential
	fixedSecretKey    = "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY" // #nosec G101 -- well-known AWS docs example key, not a real credential
	fixedSessionToken = "AQoDYXdzEJr"                              // #nosec G101 -- fixed placeholder token for local emulator
	fixedExpiration   = "2099-01-01T00:00:00Z"
	fixedRoleIDPrefix = "AROAIOSFODNN7EXAMPLE"
)

type responseMetadata struct {
	RequestID string `xml:"RequestId"`
}

type credentials struct {
	AccessKeyID     string `xml:"AccessKeyId"`
	SecretAccessKey string `xml:"SecretAccessKey"`
	SessionToken    string `xml:"SessionToken"`
	Expiration      string `xml:"Expiration"`
}

type getCallerIdentityResult struct {
	Arn     string `xml:"Arn"`
	UserID  string `xml:"UserId"`
	Account string `xml:"Account"`
}

type getCallerIdentityResponse struct {
	XMLName                 xml.Name                `xml:"GetCallerIdentityResponse"`
	Xmlns                   string                  `xml:"xmlns,attr"`
	GetCallerIdentityResult getCallerIdentityResult `xml:"GetCallerIdentityResult"`
	ResponseMetadata        responseMetadata        `xml:"ResponseMetadata"`
}

type assumedRoleUser struct {
	AssumedRoleID string `xml:"AssumedRoleId"`
	Arn           string `xml:"Arn"`
}

type assumeRoleResult struct {
	Credentials     credentials     `xml:"Credentials"`
	AssumedRoleUser assumedRoleUser `xml:"AssumedRoleUser"`
}

type assumeRoleResponse struct {
	XMLName          xml.Name         `xml:"AssumeRoleResponse"`
	Xmlns            string           `xml:"xmlns,attr"`
	AssumeRoleResult assumeRoleResult `xml:"AssumeRoleResult"`
	ResponseMetadata responseMetadata `xml:"ResponseMetadata"`
}

type getSessionTokenResult struct {
	Credentials credentials `xml:"Credentials"`
}

type getSessionTokenResponse struct {
	XMLName               xml.Name              `xml:"GetSessionTokenResponse"`
	Xmlns                 string                `xml:"xmlns,attr"`
	GetSessionTokenResult getSessionTokenResult `xml:"GetSessionTokenResult"`
	ResponseMetadata      responseMetadata      `xml:"ResponseMetadata"`
}

type errorDetail struct {
	Type    string `xml:"Type"`
	Code    string `xml:"Code"`
	Message string `xml:"Message"`
}

type errorResponse struct {
	XMLName   xml.Name    `xml:"ErrorResponse"`
	Xmlns     string      `xml:"xmlns,attr"`
	Error     errorDetail `xml:"Error"`
	RequestID string      `xml:"RequestId"`
}

var validSessionNameRE = regexp.MustCompile(`^[\w+=,.@-]*$`)

// Router handles STS API requests dispatched via the Action query parameter.
type Router struct{}

func NewRouter() *Router { return &Router{} }

func (ro *Router) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := r.ParseForm(); err != nil {
		writeError(w, http.StatusBadRequest, "InvalidRequest", "failed to parse request")
		return
	}
	action := r.Form.Get("Action")
	switch action {
	case "GetCallerIdentity":
		ro.handleGetCallerIdentity(w)
	case "AssumeRole":
		ro.handleAssumeRole(w, r)
	case "GetSessionToken":
		ro.handleGetSessionToken(w, r)
	default:
		slog.Debug( // #nosec G706 -- action comes from the Action query parameter; log injection risk accepted for a local dev emulator
			"STS operation not implemented",
			"action",
			action,
		)
		writeError(w, http.StatusBadRequest, "InvalidAction",
			"Could not find operation for the given action: "+action)
	}
}

func (ro *Router) handleGetCallerIdentity(w http.ResponseWriter) {
	writeXML(w, http.StatusOK, getCallerIdentityResponse{
		Xmlns: xmlns,
		GetCallerIdentityResult: getCallerIdentityResult{
			Arn:     fixedARN,
			UserID:  fixedUserID,
			Account: fixedAccount,
		},
		ResponseMetadata: responseMetadata{RequestID: requestID},
	})
}

func (ro *Router) handleAssumeRole(w http.ResponseWriter, r *http.Request) {
	roleArn := r.Form.Get("RoleArn")
	sessionName := r.Form.Get("RoleSessionName")
	if roleArn == "" {
		slog.Debug("AssumeRole: missing required param", "param", "RoleArn")
		writeError(
			w,
			http.StatusBadRequest,
			"ValidationError",
			"1 validation error detected: Value null at 'roleArn' failed to satisfy constraint: Member must not be null",
		)
		return
	}
	if len(roleArn) < 20 {
		slog.Debug(
			"AssumeRole: RoleArn too short",
			"len",
			len(roleArn),
		) // #nosec G706 -- roleArn comes from the request form; log injection risk accepted for a local dev emulator
		writeError(
			w,
			http.StatusBadRequest,
			"ValidationError",
			fmt.Sprintf(
				"1 validation error detected: Value '%s' at 'roleArn' failed to satisfy constraint: Member must have length greater than or equal to 20",
				roleArn,
			),
		)
		return
	}
	if len(roleArn) > 2048 {
		slog.Debug(
			"AssumeRole: RoleArn too long",
			"len",
			len(roleArn),
		) // #nosec G706 -- roleArn comes from the request form; log injection risk accepted for a local dev emulator
		writeError(
			w,
			http.StatusBadRequest,
			"ValidationError",
			fmt.Sprintf(
				"1 validation error detected: Value '%s' at 'roleArn' failed to satisfy constraint: Member must have length less than or equal to 2048",
				roleArn[:64]+"...",
			),
		)
		return
	}
	if sessionName == "" {
		slog.Debug("AssumeRole: missing required param", "param", "RoleSessionName")
		writeError(
			w,
			http.StatusBadRequest,
			"ValidationError",
			"1 validation error detected: Value null at 'roleSessionName' failed to satisfy constraint: Member must not be null",
		)
		return
	}
	if len(sessionName) < 2 {
		slog.Debug(
			"AssumeRole: RoleSessionName too short",
			"len",
			len(sessionName),
		) // #nosec G706 -- sessionName comes from the request form; log injection risk accepted for a local dev emulator
		writeError(
			w,
			http.StatusBadRequest,
			"ValidationError",
			fmt.Sprintf(
				"1 validation error detected: Value '%s' at 'roleSessionName' failed to satisfy constraint: Member must have length greater than or equal to 2",
				sessionName,
			),
		)
		return
	}
	if len(sessionName) > 64 {
		slog.Debug(
			"AssumeRole: RoleSessionName too long",
			"len",
			len(sessionName),
		) // #nosec G706 -- sessionName comes from the request form; log injection risk accepted for a local dev emulator
		writeError(
			w,
			http.StatusBadRequest,
			"ValidationError",
			fmt.Sprintf(
				"1 validation error detected: Value '%s' at 'roleSessionName' failed to satisfy constraint: Member must have length less than or equal to 64",
				sessionName,
			),
		)
		return
	}
	if !validSessionNameRE.MatchString(sessionName) {
		slog.Debug(
			"AssumeRole: RoleSessionName invalid pattern",
			"sessionName",
			sessionName,
		) // #nosec G706 -- sessionName comes from the request form; log injection risk accepted for a local dev emulator
		writeError(
			w,
			http.StatusBadRequest,
			"ValidationError",
			fmt.Sprintf(
				"1 validation error detected: Value '%s' at 'roleSessionName' failed to satisfy constraint: Member must satisfy regular expression pattern: [\\w+=,.@-]*",
				sessionName,
			),
		)
		return
	}
	if durationStr := r.Form.Get("DurationSeconds"); durationStr != "" {
		d, err := strconv.Atoi(durationStr)
		if err != nil || d < assumeRoleMinDuration {
			writeError(
				w,
				http.StatusBadRequest,
				"ValidationError",
				fmt.Sprintf(
					"1 validation error detected: Value '%s' at 'durationSeconds' failed to satisfy constraint: Member must have value greater than or equal to %d",
					durationStr,
					assumeRoleMinDuration,
				),
			)
			return
		}
		if d > assumeRoleMaxDuration {
			writeError(
				w,
				http.StatusBadRequest,
				"ValidationError",
				fmt.Sprintf(
					"1 validation error detected: Value '%s' at 'durationSeconds' failed to satisfy constraint: Member must have value less than or equal to %d",
					durationStr,
					assumeRoleMaxDuration,
				),
			)
			return
		}
	}
	idx := strings.LastIndex(roleArn, "/")
	if idx == -1 || idx == len(roleArn)-1 {
		slog.Debug(
			"AssumeRole: invalid RoleArn format",
			"roleArn",
			roleArn,
		) // #nosec G706 -- roleArn comes from the request form; log injection risk accepted for a local dev emulator
		writeError(
			w,
			http.StatusBadRequest,
			"ValidationError",
			"1 validation error detected: Value at 'roleArn' failed to satisfy constraint: Invalid ARN format",
		)
		return
	}
	roleName := roleArn[idx+1:]
	responseARN := "arn:aws:sts::" + fixedAccount + ":assumed-role/" + roleName + "/" + sessionName
	responseRoleID := fixedRoleIDPrefix + ":" + sessionName
	writeXML(w, http.StatusOK, assumeRoleResponse{
		Xmlns: xmlns,
		AssumeRoleResult: assumeRoleResult{
			Credentials: credentials{
				AccessKeyID:     fixedAccessKeyID,
				SecretAccessKey: fixedSecretKey,
				SessionToken:    fixedSessionToken,
				Expiration:      fixedExpiration,
			},
			AssumedRoleUser: assumedRoleUser{
				AssumedRoleID: responseRoleID,
				Arn:           responseARN,
			},
		},
		ResponseMetadata: responseMetadata{RequestID: requestID},
	})
}

func (ro *Router) handleGetSessionToken(w http.ResponseWriter, r *http.Request) {
	if durationStr := r.Form.Get("DurationSeconds"); durationStr != "" {
		d, err := strconv.Atoi(durationStr)
		if err != nil || d < getSessionTokenMinDuration {
			writeError(
				w,
				http.StatusBadRequest,
				"ValidationError",
				fmt.Sprintf(
					"1 validation error detected: Value '%s' at 'durationSeconds' failed to satisfy constraint: Member must have value greater than or equal to %d",
					durationStr,
					getSessionTokenMinDuration,
				),
			)
			return
		}
		if d > getSessionTokenMaxDuration {
			writeError(
				w,
				http.StatusBadRequest,
				"ValidationError",
				fmt.Sprintf(
					"1 validation error detected: Value '%s' at 'durationSeconds' failed to satisfy constraint: Member must have value less than or equal to %d",
					durationStr,
					getSessionTokenMaxDuration,
				),
			)
			return
		}
	}
	writeXML(w, http.StatusOK, getSessionTokenResponse{
		Xmlns: xmlns,
		GetSessionTokenResult: getSessionTokenResult{
			Credentials: credentials{
				AccessKeyID:     fixedAccessKeyID,
				SecretAccessKey: fixedSecretKey,
				SessionToken:    fixedSessionToken,
				Expiration:      fixedExpiration,
			},
		},
		ResponseMetadata: responseMetadata{RequestID: requestID},
	})
}

func writeXML(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "text/xml;charset=UTF-8")
	w.WriteHeader(status)
	if _, err := w.Write([]byte(xml.Header)); err != nil {
		slog.Warn("failed to write STS XML header", "err", err)
		return
	}
	if err := xml.NewEncoder(w).Encode(v); err != nil {
		slog.Warn("failed to encode STS response", "err", err)
	}
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeXML(w, status, errorResponse{
		Xmlns:     xmlns,
		Error:     errorDetail{Type: "Sender", Code: code, Message: message},
		RequestID: requestID,
	})
}
