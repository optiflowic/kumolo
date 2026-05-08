package sts

import (
	"encoding/xml"
	"log/slog"
	"net/http"
)

const (
	xmlns     = "https://sts.amazonaws.com/doc/2011-06-15/"
	requestID = "00000000-0000-0000-0000-000000000000"

	fixedAccount      = "000000000000"
	fixedUserID       = "AKIAIOSFODNN7EXAMPLE" // #nosec G101 -- well-known AWS docs example key, not a real credential
	fixedARN          = "arn:aws:iam::000000000000:root"
	fixedAccessKeyID  = "AKIAIOSFODNN7EXAMPLE"                     // #nosec G101 -- well-known AWS docs example key, not a real credential
	fixedSecretKey    = "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY" // #nosec G101 -- well-known AWS docs example key, not a real credential
	fixedSessionToken = "AQoDYXdzEJr"                              // #nosec G101 -- fixed placeholder token for local emulator
	fixedExpiration   = "2099-01-01T00:00:00Z"
	fixedRoleARN      = "arn:aws:sts::000000000000:assumed-role/kumolo-role/session"
	fixedRoleID       = "AROAIOSFODNN7EXAMPLE:session"
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

// Router handles STS API requests dispatched via the Action query parameter.
type Router struct{}

func NewRouter() *Router { return &Router{} }

func (ro *Router) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	action := r.URL.Query().Get("Action")
	switch action {
	case "GetCallerIdentity":
		ro.handleGetCallerIdentity(w)
	case "AssumeRole":
		ro.handleAssumeRole(w)
	case "GetSessionToken":
		ro.handleGetSessionToken(w)
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

func (ro *Router) handleAssumeRole(w http.ResponseWriter) {
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
				AssumedRoleID: fixedRoleID,
				Arn:           fixedRoleARN,
			},
		},
		ResponseMetadata: responseMetadata{RequestID: requestID},
	})
}

func (ro *Router) handleGetSessionToken(w http.ResponseWriter) {
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
	w.Header().Set("Content-Type", "text/xml")
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
