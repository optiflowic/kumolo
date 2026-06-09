package s3

import (
	"encoding/xml"
	"time"
)

const (
	ReplicationStatusCompleted = "COMPLETED"
	ReplicationStatusReplica   = "REPLICA"
	ReplicationStatusPending   = "PENDING"
	ReplicationStatusFailed    = "FAILED"
)

// CORSRule represents a single CORS rule stored in bucket metadata.
type CORSRule struct {
	ID             string   `json:"id,omitempty"`
	AllowedOrigins []string `json:"allowedOrigins"`
	AllowedMethods []string `json:"allowedMethods"`
	AllowedHeaders []string `json:"allowedHeaders,omitempty"`
	ExposeHeaders  []string `json:"exposeHeaders,omitempty"`
	MaxAgeSeconds  int      `json:"maxAgeSeconds,omitempty"`
}

// ObjectRetention holds WORM retention settings for an object version.
type ObjectRetention struct {
	Mode            string    `json:"mode"` // GOVERNANCE or COMPLIANCE
	RetainUntilDate time.Time `json:"retainUntilDate"`
}

// ObjectLegalHold holds legal hold status for an object version.
type ObjectLegalHold struct {
	Status string `json:"status"` // ON or OFF
}

// ObjectMetadata is stored as a sidecar .meta.json file alongside each object.
type ObjectMetadata struct {
	ContentType         string            `json:"contentType"`
	ETag                string            `json:"etag"`
	LastModified        time.Time         `json:"lastModified"`
	Size                int64             `json:"size"`
	UserMetadata        map[string]string `json:"userMetadata,omitempty"`
	VersionID           string            `json:"versionId,omitempty"`
	IsDeleteMarker      bool              `json:"isDeleteMarker,omitempty"`
	SSEAlgorithm        string            `json:"sseAlgorithm,omitempty"`
	SSEKMSKeyID         string            `json:"sseKmsKeyId,omitempty"`
	SSEBucketKeyEnabled bool              `json:"sseBucketKeyEnabled,omitempty"`
	SSECKeyMD5          string            `json:"ssecKeyMd5,omitempty"`
	StorageClass        string            `json:"storageClass,omitempty"`
	RestoreInitiated    bool              `json:"restoreInitiated,omitempty"`
	Retention           *ObjectRetention  `json:"retention,omitempty"`
	LegalHold           *ObjectLegalHold  `json:"legalHold,omitempty"`
	// Set when superseded; zero for versions predating lifecycle enforcement.
	NoncurrentSince   time.Time `json:"noncurrentSince,omitempty"`
	ReplicationStatus string    `json:"replicationStatus,omitempty"`
}

// VersionInfo represents a non-delete-marker version of an object in a versioned bucket.
type VersionInfo struct {
	Key             string
	VersionID       string
	IsLatest        bool
	LastModified    time.Time
	ETag            string
	Size            int64
	StorageClass    string
	NoncurrentSince time.Time // zero when IsLatest is true
}

// DeleteMarkerInfo represents a delete marker version in a versioned bucket.
type DeleteMarkerInfo struct {
	Key             string
	VersionID       string
	IsLatest        bool
	LastModified    time.Time
	NoncurrentSince time.Time // zero when IsLatest is true
}

// Tag is a key-value pair attached to an S3 object.
type Tag struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type BucketInfo struct {
	Name         string
	CreationDate time.Time
	Region       string
}

type ObjectInfo struct {
	Key      string
	Metadata ObjectMetadata
}

// CompletePart identifies a part by number and ETag for CompleteMultipartUpload.
type CompletePart struct {
	PartNumber int
	ETag       string
}

// MultipartUploadInfo describes an in-progress multipart upload.
type MultipartUploadInfo struct {
	UploadID     string
	Key          string
	Initiated    time.Time
	StorageClass string
}

// PartInfo describes an uploaded part.
type PartInfo struct {
	PartNumber   int
	ETag         string
	Size         int64
	LastModified time.Time
}

// XML response types for S3 operations.

type listBucketsResult struct {
	XMLName xml.Name    `xml:"ListAllMyBucketsResult"`
	Owner   xmlOwner    `xml:"Owner"`
	Buckets []xmlBucket `xml:"Buckets>Bucket"`
}

type xmlOwner struct {
	ID          string `xml:"ID"`
	DisplayName string `xml:"DisplayName"`
}

type xmlBucket struct {
	Name         string    `xml:"Name"`
	CreationDate time.Time `xml:"CreationDate"`
	BucketRegion string    `xml:"BucketRegion,omitempty"`
}

type listObjectsResult struct {
	XMLName        xml.Name           `xml:"ListBucketResult"`
	Name           string             `xml:"Name"`
	Prefix         string             `xml:"Prefix"`
	Marker         string             `xml:"Marker"`
	NextMarker     string             `xml:"NextMarker,omitempty"`
	Delimiter      string             `xml:"Delimiter,omitempty"`
	MaxKeys        int                `xml:"MaxKeys"`
	IsTruncated    bool               `xml:"IsTruncated"`
	Contents       []xmlObjectContent `xml:"Contents"`
	CommonPrefixes []xmlCommonPrefix  `xml:"CommonPrefixes"`
}

type xmlCommonPrefix struct {
	Prefix string `xml:"Prefix"`
}

type listObjectsV2Result struct {
	XMLName               xml.Name           `xml:"ListBucketResult"`
	Name                  string             `xml:"Name"`
	Prefix                string             `xml:"Prefix"`
	Delimiter             string             `xml:"Delimiter,omitempty"`
	MaxKeys               int                `xml:"MaxKeys"`
	KeyCount              int                `xml:"KeyCount"`
	IsTruncated           bool               `xml:"IsTruncated"`
	ContinuationToken     string             `xml:"ContinuationToken,omitempty"`
	NextContinuationToken string             `xml:"NextContinuationToken,omitempty"`
	StartAfter            string             `xml:"StartAfter,omitempty"`
	Contents              []xmlObjectContent `xml:"Contents"`
	CommonPrefixes        []xmlCommonPrefix  `xml:"CommonPrefixes"`
}

type xmlObjectContent struct {
	Key          string    `xml:"Key"`
	LastModified time.Time `xml:"LastModified"`
	ETag         string    `xml:"ETag"`
	Size         int64     `xml:"Size"`
	StorageClass string    `xml:"StorageClass"`
	Owner        *xmlOwner `xml:"Owner,omitempty"`
}

// locationConstraint represents the GetBucketLocation response.
// An empty Location means us-east-1 per the S3 specification.
type locationConstraint struct {
	XMLName  xml.Name `xml:"LocationConstraint"`
	Location string   `xml:",chardata"`
}

type copyObjectResult struct {
	XMLName      xml.Name  `xml:"CopyObjectResult"`
	ETag         string    `xml:"ETag"`
	LastModified time.Time `xml:"LastModified"`
}

type copyPartResult struct {
	XMLName      xml.Name  `xml:"CopyPartResult"`
	ETag         string    `xml:"ETag"`
	LastModified time.Time `xml:"LastModified"`
}

type initiateMultipartUploadResult struct {
	XMLName  xml.Name `xml:"InitiateMultipartUploadResult"`
	Bucket   string   `xml:"Bucket"`
	Key      string   `xml:"Key"`
	UploadID string   `xml:"UploadId"`
}

type completeMultipartUploadRequest struct {
	XMLName xml.Name          `xml:"CompleteMultipartUpload"`
	Parts   []xmlCompletePart `xml:"Part"`
}

type xmlCompletePart struct {
	PartNumber int    `xml:"PartNumber"`
	ETag       string `xml:"ETag"`
}

type completeMultipartUploadResult struct {
	XMLName  xml.Name `xml:"CompleteMultipartUploadResult"`
	Location string   `xml:"Location"`
	Bucket   string   `xml:"Bucket"`
	Key      string   `xml:"Key"`
	ETag     string   `xml:"ETag"`
}

type listMultipartUploadsResult struct {
	XMLName            xml.Name             `xml:"ListMultipartUploadsResult"`
	Bucket             string               `xml:"Bucket"`
	KeyMarker          string               `xml:"KeyMarker"`
	UploadIdMarker     string               `xml:"UploadIdMarker"`
	NextKeyMarker      string               `xml:"NextKeyMarker,omitempty"`
	NextUploadIdMarker string               `xml:"NextUploadIdMarker,omitempty"`
	Prefix             string               `xml:"Prefix"`
	Delimiter          string               `xml:"Delimiter,omitempty"`
	MaxUploads         int                  `xml:"MaxUploads"`
	IsTruncated        bool                 `xml:"IsTruncated"`
	Uploads            []xmlMultipartUpload `xml:"Upload"`
	CommonPrefixes     []xmlCommonPrefix    `xml:"CommonPrefixes"`
}

type xmlMultipartUpload struct {
	Key          string    `xml:"Key"`
	UploadID     string    `xml:"UploadId"`
	StorageClass string    `xml:"StorageClass"`
	Initiated    time.Time `xml:"Initiated"`
}

type listPartsResult struct {
	XMLName              xml.Name  `xml:"ListPartsResult"`
	Bucket               string    `xml:"Bucket"`
	Key                  string    `xml:"Key"`
	UploadID             string    `xml:"UploadId"`
	StorageClass         string    `xml:"StorageClass"`
	PartNumberMarker     int       `xml:"PartNumberMarker"`
	NextPartNumberMarker int       `xml:"NextPartNumberMarker,omitempty"`
	MaxParts             int       `xml:"MaxParts"`
	IsTruncated          bool      `xml:"IsTruncated"`
	Parts                []xmlPart `xml:"Part"`
}

type xmlPart struct {
	PartNumber   int       `xml:"PartNumber"`
	ETag         string    `xml:"ETag"`
	Size         int64     `xml:"Size"`
	LastModified time.Time `xml:"LastModified"`
}

type xmlTagging struct {
	XMLName xml.Name `xml:"Tagging"`
	TagSet  []xmlTag `xml:"TagSet>Tag"`
}

type xmlTag struct {
	Key   string `xml:"Key"`
	Value string `xml:"Value"`
}

type deleteObjectsRequest struct {
	XMLName xml.Name          `xml:"Delete"`
	Quiet   bool              `xml:"Quiet"`
	Objects []xmlDeleteObject `xml:"Object"`
}

type xmlDeleteObject struct {
	Key       string `xml:"Key"`
	VersionId string `xml:"VersionId"`
}

type deleteObjectsResult struct {
	XMLName xml.Name           `xml:"DeleteResult"`
	Deleted []xmlDeletedObject `xml:"Deleted"`
	Errors  []xmlDeleteError   `xml:"Error"`
}

type xmlDeletedObject struct {
	Key                   string `xml:"Key"`
	VersionId             string `xml:"VersionId,omitempty"`
	DeleteMarker          bool   `xml:"DeleteMarker,omitempty"`
	DeleteMarkerVersionId string `xml:"DeleteMarkerVersionId,omitempty"`
}

type xmlDeleteError struct {
	Key       string `xml:"Key"`
	VersionId string `xml:"VersionId,omitempty"`
	Code      string `xml:"Code"`
	Message   string `xml:"Message"`
}

type xmlVersioningConfiguration struct {
	XMLName xml.Name `xml:"VersioningConfiguration"`
	Status  string   `xml:"Status,omitempty"`
}

type xmlListVersionsResult struct {
	XMLName             xml.Name           `xml:"ListVersionsResult"`
	Xmlns               string             `xml:"xmlns,attr"`
	Name                string             `xml:"Name"`
	Prefix              string             `xml:"Prefix"`
	KeyMarker           string             `xml:"KeyMarker"`
	VersionIdMarker     string             `xml:"VersionIdMarker"`
	NextKeyMarker       string             `xml:"NextKeyMarker,omitempty"`
	NextVersionIdMarker string             `xml:"NextVersionIdMarker,omitempty"`
	Delimiter           string             `xml:"Delimiter,omitempty"`
	MaxKeys             int                `xml:"MaxKeys"`
	IsTruncated         bool               `xml:"IsTruncated"`
	Versions            []xmlObjectVersion `xml:"Version"`
	DeleteMarkers       []xmlDeleteMarker  `xml:"DeleteMarker"`
	CommonPrefixes      []xmlCommonPrefix  `xml:"CommonPrefixes"`
}

type xmlObjectVersion struct {
	Key          string   `xml:"Key"`
	VersionId    string   `xml:"VersionId"`
	IsLatest     bool     `xml:"IsLatest"`
	LastModified string   `xml:"LastModified"`
	ETag         string   `xml:"ETag"`
	Size         int64    `xml:"Size"`
	StorageClass string   `xml:"StorageClass"`
	Owner        xmlOwner `xml:"Owner"`
}

type xmlDeleteMarker struct {
	Key          string   `xml:"Key"`
	VersionId    string   `xml:"VersionId"`
	IsLatest     bool     `xml:"IsLatest"`
	LastModified string   `xml:"LastModified"`
	Owner        xmlOwner `xml:"Owner"`
}

type xmlCORSConfiguration struct {
	XMLName   xml.Name      `xml:"CORSConfiguration"`
	CORSRules []xmlCORSRule `xml:"CORSRule"`
}

type xmlCORSRule struct {
	ID             string   `xml:"ID,omitempty"`
	AllowedOrigins []string `xml:"AllowedOrigin"`
	AllowedMethods []string `xml:"AllowedMethod"`
	AllowedHeaders []string `xml:"AllowedHeader,omitempty"`
	ExposeHeaders  []string `xml:"ExposeHeader,omitempty"`
	MaxAgeSeconds  int      `xml:"MaxAgeSeconds,omitempty"`
}

// byteRange represents an inclusive byte range [Start, End] for UploadPartCopy.
type byteRange struct {
	Start int64
	End   int64
}

// xmlObjectRetention is the XML representation for GetObjectRetention /
// PutObjectRetention request and response bodies.
type xmlObjectRetention struct {
	XMLName         xml.Name  `xml:"http://s3.amazonaws.com/doc/2006-03-01/ Retention"`
	Mode            string    `xml:"Mode"`
	RetainUntilDate time.Time `xml:"RetainUntilDate"`
}

// xmlObjectLegalHold is the XML representation for GetObjectLegalHold /
// PutObjectLegalHold request and response bodies.
type xmlObjectLegalHold struct {
	XMLName xml.Name `xml:"http://s3.amazonaws.com/doc/2006-03-01/ LegalHold"`
	Status  string   `xml:"Status"`
}

// getObjectAttributesResponse is the XML response for GetObjectAttributes.
// Field order follows the AWS S3 API documentation.
type getObjectAttributesResponse struct {
	XMLName      xml.Name        `xml:"GetObjectAttributesResponse"`
	ETag         string          `xml:"ETag,omitempty"`
	ObjectParts  *xmlObjectParts `xml:"ObjectParts,omitempty"`
	ObjectSize   *int64          `xml:"ObjectSize,omitempty"`
	StorageClass string          `xml:"StorageClass,omitempty"`
}

// xmlObjectParts holds multipart info returned by GetObjectAttributes.
type xmlObjectParts struct {
	TotalPartsCount int `xml:"TotalPartsCount"`
}

// xmlSSEConfiguration is the XML representation of ServerSideEncryptionConfiguration
// used for parsing bucket default encryption settings.
type xmlSSEConfiguration struct {
	XMLName xml.Name     `xml:"ServerSideEncryptionConfiguration"`
	Rules   []xmlSSERule `xml:"Rule"`
}

type xmlSSERule struct {
	Apply            xmlApplySSEByDefault `xml:"ApplyServerSideEncryptionByDefault"`
	BucketKeyEnabled bool                 `xml:"BucketKeyEnabled"`
}

type xmlApplySSEByDefault struct {
	SSEAlgorithm   string `xml:"SSEAlgorithm"`
	KMSMasterKeyID string `xml:"KMSMasterKeyID"`
}

// Lifecycle configuration XML types used by LifecycleEnforcer.

type lifecycleConfiguration struct {
	XMLName xml.Name        `xml:"LifecycleConfiguration"`
	Rules   []lifecycleRule `xml:"Rule"`
}

type lifecycleRule struct {
	ID     string           `xml:"ID,omitempty"`
	Status string           `xml:"Status"`
	Prefix string           `xml:"Prefix,omitempty"` // legacy V1 style; V2 uses Filter
	Filter *lifecycleFilter `xml:"Filter"`

	Expiration                     *lifecycleExpiration                     `xml:"Expiration"`
	NoncurrentVersionExpiration    *lifecycleNoncurrentVersionExpiration    `xml:"NoncurrentVersionExpiration"`
	AbortIncompleteMultipartUpload *lifecycleAbortIncompleteMultipartUpload `xml:"AbortIncompleteMultipartUpload"`
}

type lifecycleFilter struct {
	Prefix string `xml:"Prefix"`
}

type lifecycleExpiration struct {
	Days                      int       `xml:"Days"`
	Date                      time.Time `xml:"Date"`
	ExpiredObjectDeleteMarker bool      `xml:"ExpiredObjectDeleteMarker"`
}

type lifecycleNoncurrentVersionExpiration struct {
	NoncurrentDays int `xml:"NoncurrentDays"`
}

type lifecycleAbortIncompleteMultipartUpload struct {
	DaysAfterInitiation int `xml:"DaysAfterInitiation"`
}
