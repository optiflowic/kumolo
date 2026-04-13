package s3

import (
	"encoding/xml"
	"time"
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

// ObjectMetadata is stored as a sidecar .meta.json file alongside each object.
type ObjectMetadata struct {
	ContentType    string            `json:"contentType"`
	ETag           string            `json:"etag"`
	LastModified   time.Time         `json:"lastModified"`
	Size           int64             `json:"size"`
	UserMetadata   map[string]string `json:"userMetadata,omitempty"`
	VersionID      string            `json:"versionId,omitempty"`
	IsDeleteMarker bool              `json:"isDeleteMarker,omitempty"`
}

// VersionInfo represents a non-delete-marker version of an object in a versioned bucket.
type VersionInfo struct {
	Key          string
	VersionID    string
	IsLatest     bool
	LastModified time.Time
	ETag         string
	Size         int64
}

// DeleteMarkerInfo represents a delete marker version in a versioned bucket.
type DeleteMarkerInfo struct {
	Key          string
	VersionID    string
	IsLatest     bool
	LastModified time.Time
}

// Tag is a key-value pair attached to an S3 object.
type Tag struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type BucketInfo struct {
	Name         string
	CreationDate time.Time
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
	UploadID  string
	Key       string
	Initiated time.Time
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
	XMLName     xml.Name           `xml:"ListBucketResult"`
	Name        string             `xml:"Name"`
	Prefix      string             `xml:"Prefix"`
	KeyCount    int                `xml:"KeyCount"`
	MaxKeys     int                `xml:"MaxKeys"`
	IsTruncated bool               `xml:"IsTruncated"`
	Contents    []xmlObjectContent `xml:"Contents"`
}

type xmlObjectContent struct {
	Key          string    `xml:"Key"`
	LastModified time.Time `xml:"LastModified"`
	ETag         string    `xml:"ETag"`
	Size         int64     `xml:"Size"`
	StorageClass string    `xml:"StorageClass"`
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
	XMLName     xml.Name             `xml:"ListMultipartUploadsResult"`
	Bucket      string               `xml:"Bucket"`
	MaxUploads  int                  `xml:"MaxUploads"`
	IsTruncated bool                 `xml:"IsTruncated"`
	Uploads     []xmlMultipartUpload `xml:"Upload"`
}

type xmlMultipartUpload struct {
	Key          string    `xml:"Key"`
	UploadID     string    `xml:"UploadId"`
	StorageClass string    `xml:"StorageClass"`
	Initiated    time.Time `xml:"Initiated"`
}

type listPartsResult struct {
	XMLName      xml.Name  `xml:"ListPartsResult"`
	Bucket       string    `xml:"Bucket"`
	Key          string    `xml:"Key"`
	UploadID     string    `xml:"UploadId"`
	StorageClass string    `xml:"StorageClass"`
	MaxParts     int       `xml:"MaxParts"`
	IsTruncated  bool      `xml:"IsTruncated"`
	Parts        []xmlPart `xml:"Part"`
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
	Key string `xml:"Key"`
}

type deleteObjectsResult struct {
	XMLName xml.Name           `xml:"DeleteResult"`
	Deleted []xmlDeletedObject `xml:"Deleted"`
	Errors  []xmlDeleteError   `xml:"Error"`
}

type xmlDeletedObject struct {
	Key string `xml:"Key"`
}

type xmlDeleteError struct {
	Key     string `xml:"Key"`
	Code    string `xml:"Code"`
	Message string `xml:"Message"`
}

type xmlVersioningConfiguration struct {
	XMLName xml.Name `xml:"VersioningConfiguration"`
	Status  string   `xml:"Status,omitempty"`
}

type xmlListVersionsResult struct {
	XMLName         xml.Name           `xml:"ListVersionsResult"`
	Xmlns           string             `xml:"xmlns,attr"`
	Name            string             `xml:"Name"`
	Prefix          string             `xml:"Prefix"`
	KeyMarker       string             `xml:"KeyMarker"`
	VersionIdMarker string             `xml:"VersionIdMarker"`
	MaxKeys         int                `xml:"MaxKeys"`
	IsTruncated     bool               `xml:"IsTruncated"`
	Versions        []xmlObjectVersion `xml:"Version"`
	DeleteMarkers   []xmlDeleteMarker  `xml:"DeleteMarker"`
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
