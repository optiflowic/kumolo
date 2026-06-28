package s3

import (
	"encoding/xml"
	"net/http"
)

const (
	aclOwnerID          = "owner"
	aclOwnerDisplayName = "owner"
	aclAllUsersURI      = "http://acs.amazonaws.com/groups/global/AllUsers"
	aclAuthUsersURI     = "http://acs.amazonaws.com/groups/global/AuthenticatedUsers"
	aclPermRead         = "READ"
	aclPermWrite        = "WRITE"
	aclPermFullControl  = "FULL_CONTROL"
	aclXMLNS            = "http://s3.amazonaws.com/doc/2006-03-01/"
)

const (
	aclGranteeOwner = `<Grantee xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xsi:type="CanonicalUser">` +
		`<ID>owner</ID><DisplayName>owner</DisplayName></Grantee>`
	aclGranteeAllUsers = `<Grantee xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xsi:type="Group">` +
		`<URI>http://acs.amazonaws.com/groups/global/AllUsers</URI></Grantee>`
	aclGranteeAuthUsers = `<Grantee xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xsi:type="Group">` +
		`<URI>http://acs.amazonaws.com/groups/global/AuthenticatedUsers</URI></Grantee>`
)

func aclGrant(grantee, permission string) string {
	return `<Grant>` + grantee + `<Permission>` + permission + `</Permission></Grant>`
}

func aclWrap(grants string) string {
	return `<AccessControlPolicy xmlns="` + aclXMLNS + `">` +
		`<Owner><ID>` + aclOwnerID + `</ID><DisplayName>` + aclOwnerDisplayName + `</DisplayName></Owner>` +
		`<AccessControlList>` + grants + `</AccessControlList>` +
		`</AccessControlPolicy>`
}

// defaultACLXML returns the canonical private ACL (owner FULL_CONTROL only).
func defaultACLXML() string {
	return aclWrap(aclGrant(aclGranteeOwner, aclPermFullControl))
}

// buildCannedACL returns the canonical XML for the given canned ACL name.
// Returns an error if the name is not a valid canned ACL.
func buildCannedACL(canned string) (string, error) {
	ownerFC := aclGrant(aclGranteeOwner, aclPermFullControl)
	switch canned {
	case "private":
		return aclWrap(ownerFC), nil
	case "public-read":
		return aclWrap(ownerFC + aclGrant(aclGranteeAllUsers, aclPermRead)), nil
	case "public-read-write":
		return aclWrap(ownerFC +
			aclGrant(aclGranteeAllUsers, aclPermRead) +
			aclGrant(aclGranteeAllUsers, aclPermWrite)), nil
	case "authenticated-read":
		return aclWrap(ownerFC + aclGrant(aclGranteeAuthUsers, aclPermRead)), nil
	case "bucket-owner-read", "bucket-owner-full-control", "log-delivery-write":
		// kumolo has a single effective owner, so these reduce to private.
		return aclWrap(ownerFC), nil
	default:
		return "", &aclInvalidCannedACLError{canned: canned}
	}
}

type aclInvalidCannedACLError struct{ canned string }

func (e *aclInvalidCannedACLError) Error() string {
	return "invalid canned ACL: " + e.canned
}

// aclPolicyXML is used only for parsing incoming ACL XML bodies.
type aclPolicyXML struct {
	XMLName xml.Name `xml:"AccessControlPolicy"`
	Grants  []struct {
		Grantee struct {
			Type string `xml:"http://www.w3.org/2001/XMLSchema-instance type,attr"`
			ID   string `xml:"ID"`
			URI  string `xml:"URI"`
		} `xml:"Grantee"`
		Permission string `xml:"Permission"`
	} `xml:"AccessControlList>Grant"`
}

// parseACLBody validates the XML body and returns it as-is for storage.
func parseACLBody(body []byte) (string, error) {
	var p aclPolicyXML
	if err := xml.Unmarshal(body, &p); err != nil {
		return "", err
	}
	return string(body), nil
}

// isAnonymousRequest returns true when the request carries no AWS credentials.
func isAnonymousRequest(r *http.Request) bool {
	return r.Header.Get("Authorization") == "" && !r.URL.Query().Has(amzQSignature)
}

// aclAllowsAnonymous reports whether the stored ACL XML grants the given
// permission to the AllUsers group.
// An empty aclXML means no ACL has been explicitly set: all access is allowed
// (backward-compatible behavior — ACL enforcement only applies when an ACL has
// been explicitly configured via PutBucketACL / PutObjectACL).
func aclAllowsAnonymous(aclXML, permission string) bool {
	if aclXML == "" {
		return true // no ACL configured: unrestricted access
	}
	var p aclPolicyXML
	// aclXML is kumolo-internal data validated by parseACLBody before storage; not raw user input.
	if err := xml.Unmarshal( // #nosec G709
		[]byte(aclXML),
		&p,
	); err != nil {
		return false
	}
	for _, g := range p.Grants {
		if g.Grantee.URI != aclAllUsersURI {
			continue
		}
		if g.Permission == permission || g.Permission == aclPermFullControl {
			return true
		}
	}
	return false
}
