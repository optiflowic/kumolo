package s3

import (
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBucketNameFromARN(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"arn:aws:s3:::my-bucket", "my-bucket"},
		{"arn:aws-cn:s3:::my-bucket", "my-bucket"},
		{"arn:aws:s3:::bucket-with-dashes", "bucket-with-dashes"},
		{"my-bucket", "my-bucket"}, // plain name fallback
		{"arn:aws:s3:::colons:in:name", "colons:in:name"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			assert.Equal(t, tt.want, bucketNameFromARN(tt.input))
		})
	}
}

func TestRuleKeyPrefix(t *testing.T) {
	tests := []struct {
		name string
		rule replicationRule
		want string
	}{
		{
			name: "deprecated top-level prefix",
			rule: replicationRule{Prefix: "logs/"},
			want: "logs/",
		},
		{
			name: "filter prefix",
			rule: replicationRule{Filter: &replicationFilter{Prefix: "images/"}},
			want: "images/",
		},
		{
			name: "filter and prefix",
			rule: replicationRule{
				Filter: &replicationFilter{And: &replicationFilterAnd{Prefix: "data/"}},
			},
			want: "data/",
		},
		{
			name: "empty filter matches all",
			rule: replicationRule{Filter: &replicationFilter{}},
			want: "",
		},
		{
			name: "no filter matches all",
			rule: replicationRule{},
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, ruleKeyPrefix(tt.rule))
		})
	}
}

// cfgXML builds a minimal ReplicationConfiguration for a single rule.
func buildReplicationCfg(destARN, prefix, status string) string {
	return fmt.Sprintf(
		`<ReplicationConfiguration xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><Role>arn:aws:iam::000000000000:role/replication-role</Role><Rule><Status>%s</Status><Filter><Prefix>%s</Prefix></Filter><Destination><Bucket>%s</Bucket></Destination></Rule></ReplicationConfiguration>`,
		status,
		prefix,
		destARN,
	)
}

// buildReplicationCfgWithDMR builds a ReplicationConfiguration that includes
// a DeleteMarkerReplication element.
func buildReplicationCfgWithDMR(destARN, prefix, ruleStatus, dmrStatus string) string {
	return fmt.Sprintf(
		`<ReplicationConfiguration xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><Role>arn:aws:iam::000000000000:role/replication-role</Role><Rule><Status>%s</Status><Filter><Prefix>%s</Prefix></Filter><Destination><Bucket>%s</Bucket></Destination><DeleteMarkerReplication><Status>%s</Status></DeleteMarkerReplication></Rule></ReplicationConfiguration>`,
		ruleStatus,
		prefix,
		destARN,
		dmrStatus,
	)
}

func TestRuleHasTagFilter(t *testing.T) {
	tests := []struct {
		name string
		rule replicationRule
		want bool
	}{
		{
			name: "no filter",
			rule: replicationRule{},
			want: false,
		},
		{
			name: "filter prefix only",
			rule: replicationRule{Filter: &replicationFilter{Prefix: "logs/"}},
			want: false,
		},
		{
			name: "filter single tag",
			rule: replicationRule{
				Filter: &replicationFilter{Tag: &xmlTag{Key: "env", Value: "prod"}},
			},
			want: true,
		},
		{
			name: "filter and with no tags",
			rule: replicationRule{
				Filter: &replicationFilter{And: &replicationFilterAnd{Prefix: "p/"}},
			},
			want: false,
		},
		{
			name: "filter and with tags",
			rule: replicationRule{Filter: &replicationFilter{And: &replicationFilterAnd{
				Tags: []xmlTag{{Key: "k", Value: "v"}},
			}}},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, ruleHasTagFilter(tt.rule))
		})
	}
}

func TestTagSetContains(t *testing.T) {
	tags := []Tag{{Key: "env", Value: "prod"}, {Key: "team", Value: "platform"}}
	tests := []struct {
		name string
		want xmlTag
		ok   bool
	}{
		{"present", xmlTag{Key: "env", Value: "prod"}, true},
		{"present second", xmlTag{Key: "team", Value: "platform"}, true},
		{"wrong value", xmlTag{Key: "env", Value: "staging"}, false},
		{"absent key", xmlTag{Key: "missing", Value: "x"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.ok, tagSetContains(tags, tt.want))
		})
	}
	t.Run("empty tag set", func(t *testing.T) {
		assert.False(t, tagSetContains(nil, xmlTag{Key: "k", Value: "v"}))
	})
}

func TestRuleMatchesTags(t *testing.T) {
	objTags := []Tag{{Key: "env", Value: "prod"}, {Key: "team", Value: "platform"}}

	tests := []struct {
		name    string
		rule    replicationRule
		objTags []Tag
		want    bool
	}{
		{
			name: "no filter always matches",
			rule: replicationRule{},
			want: true,
		},
		{
			name: "filter prefix only always matches",
			rule: replicationRule{Filter: &replicationFilter{Prefix: "logs/"}},
			want: true,
		},
		{
			name: "filter tag matches",
			rule: replicationRule{
				Filter: &replicationFilter{Tag: &xmlTag{Key: "env", Value: "prod"}},
			},
			objTags: objTags,
			want:    true,
		},
		{
			name: "filter tag wrong value",
			rule: replicationRule{
				Filter: &replicationFilter{Tag: &xmlTag{Key: "env", Value: "staging"}},
			},
			objTags: objTags,
			want:    false,
		},
		{
			name: "filter tag absent key",
			rule: replicationRule{
				Filter: &replicationFilter{Tag: &xmlTag{Key: "missing", Value: "x"}},
			},
			objTags: objTags,
			want:    false,
		},
		{
			name: "filter and tags all match",
			rule: replicationRule{Filter: &replicationFilter{And: &replicationFilterAnd{
				Tags: []xmlTag{{Key: "env", Value: "prod"}, {Key: "team", Value: "platform"}},
			}}},
			objTags: objTags,
			want:    true,
		},
		{
			name: "filter and tags partial match",
			rule: replicationRule{Filter: &replicationFilter{And: &replicationFilterAnd{
				Tags: []xmlTag{{Key: "env", Value: "prod"}, {Key: "team", Value: "other"}},
			}}},
			objTags: objTags,
			want:    false,
		},
		{
			name: "filter and no tags matches",
			rule: replicationRule{
				Filter: &replicationFilter{And: &replicationFilterAnd{Prefix: "p/"}},
			},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tags := tt.objTags
			if tags == nil {
				tags = objTags
			}
			assert.Equal(t, tt.want, ruleMatchesTags(tt.rule, tags))
		})
	}
}

func buildReplicationCfgWithFilterTag(destARN, tagKey, tagValue string) string {
	return fmt.Sprintf(
		`<ReplicationConfiguration xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><Role>arn:aws:iam::000000000000:role/r</Role><Rule><Status>Enabled</Status><Filter><Tag><Key>%s</Key><Value>%s</Value></Tag></Filter><Destination><Bucket>%s</Bucket></Destination></Rule></ReplicationConfiguration>`,
		tagKey,
		tagValue,
		destARN,
	)
}

func buildReplicationCfgWithAndTags(destARN, prefix string, tags []xmlTag) string {
	var tagXML strings.Builder
	for _, tag := range tags {
		fmt.Fprintf(&tagXML, "<Tag><Key>%s</Key><Value>%s</Value></Tag>", tag.Key, tag.Value)
	}
	return fmt.Sprintf(
		`<ReplicationConfiguration xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><Role>arn:aws:iam::000000000000:role/r</Role><Rule><Status>Enabled</Status><Filter><And><Prefix>%s</Prefix>%s</And></Filter><Destination><Bucket>%s</Bucket></Destination></Rule></ReplicationConfiguration>`,
		prefix,
		tagXML.String(),
		destARN,
	)
}

func TestReplicateObject(t *testing.T) {
	t.Run("object is copied to destination bucket", func(t *testing.T) {
		ro := newTestRouter(t)
		require.NoError(t, ro.storage.CreateBucket("src", "us-east-1", false))
		require.NoError(t, ro.storage.CreateBucket("dst", "us-east-1", false))
		require.NoError(t, ro.storage.PutBucketReplication("src",
			buildReplicationCfg("arn:aws:s3:::dst", "", "Enabled")))

		req := httptest.NewRequest(http.MethodPut, "/src/hello.txt",
			strings.NewReader("hello"))
		req.Header.Set("Content-Type", "text/plain")
		rr := httptest.NewRecorder()
		ro.ServeHTTP(rr, req)
		require.Equal(t, http.StatusOK, rr.Code)

		// Object should exist in destination bucket
		f, meta, err := ro.storage.GetObject("dst", "hello.txt")
		require.NoError(t, err)
		defer func() { _ = f.Close() }()
		body, _ := io.ReadAll(f)
		assert.Equal(t, "hello", string(body))
		assert.Equal(t, "REPLICA", meta.ReplicationStatus)
	})

	t.Run("source object gets COMPLETED status", func(t *testing.T) {
		ro := newTestRouter(t)
		require.NoError(t, ro.storage.CreateBucket("src", "us-east-1", false))
		require.NoError(t, ro.storage.CreateBucket("dst", "us-east-1", false))
		require.NoError(t, ro.storage.PutBucketReplication("src",
			buildReplicationCfg("arn:aws:s3:::dst", "", "Enabled")))

		req := httptest.NewRequest(http.MethodPut, "/src/doc.txt",
			strings.NewReader("content"))
		req.Header.Set("Content-Type", "text/plain")
		rr := httptest.NewRecorder()
		ro.ServeHTTP(rr, req)
		require.Equal(t, http.StatusOK, rr.Code)

		srcMeta, err := ro.storage.HeadObject("src", "doc.txt")
		require.NoError(t, err)
		assert.Equal(t, "COMPLETED", srcMeta.ReplicationStatus)
	})

	t.Run("prefix filter: matching key is replicated", func(t *testing.T) {
		ro := newTestRouter(t)
		require.NoError(t, ro.storage.CreateBucket("src", "us-east-1", false))
		require.NoError(t, ro.storage.CreateBucket("dst", "us-east-1", false))
		require.NoError(t, ro.storage.PutBucketReplication("src",
			buildReplicationCfg("arn:aws:s3:::dst", "logs/", "Enabled")))

		req := httptest.NewRequest(http.MethodPut, "/src/logs/2026.log",
			strings.NewReader("log"))
		req.Header.Set("Content-Type", "text/plain")
		rr := httptest.NewRecorder()
		ro.ServeHTTP(rr, req)
		require.Equal(t, http.StatusOK, rr.Code)

		_, _, err := ro.storage.GetObject("dst", "logs/2026.log")
		assert.NoError(t, err)
	})

	t.Run("prefix filter: non-matching key is not replicated", func(t *testing.T) {
		ro := newTestRouter(t)
		require.NoError(t, ro.storage.CreateBucket("src", "us-east-1", false))
		require.NoError(t, ro.storage.CreateBucket("dst", "us-east-1", false))
		require.NoError(t, ro.storage.PutBucketReplication("src",
			buildReplicationCfg("arn:aws:s3:::dst", "logs/", "Enabled")))

		req := httptest.NewRequest(http.MethodPut, "/src/data/file.txt",
			strings.NewReader("data"))
		req.Header.Set("Content-Type", "text/plain")
		rr := httptest.NewRecorder()
		ro.ServeHTTP(rr, req)
		require.Equal(t, http.StatusOK, rr.Code)

		_, _, err := ro.storage.GetObject("dst", "data/file.txt")
		assert.ErrorIs(t, err, ErrObjectNotFound)
	})

	t.Run("disabled rule is not executed", func(t *testing.T) {
		ro := newTestRouter(t)
		require.NoError(t, ro.storage.CreateBucket("src", "us-east-1", false))
		require.NoError(t, ro.storage.CreateBucket("dst", "us-east-1", false))
		require.NoError(t, ro.storage.PutBucketReplication("src",
			buildReplicationCfg("arn:aws:s3:::dst", "", "Disabled")))

		req := httptest.NewRequest(http.MethodPut, "/src/obj.txt",
			strings.NewReader("body"))
		req.Header.Set("Content-Type", "text/plain")
		rr := httptest.NewRecorder()
		ro.ServeHTTP(rr, req)
		require.Equal(t, http.StatusOK, rr.Code)

		_, _, err := ro.storage.GetObject("dst", "obj.txt")
		assert.ErrorIs(t, err, ErrObjectNotFound)
	})

	t.Run("REPLICA objects are not re-replicated", func(t *testing.T) {
		ro := newTestRouter(t)
		require.NoError(t, ro.storage.CreateBucket("src", "us-east-1", false))
		require.NoError(t, ro.storage.CreateBucket("dst", "us-east-1", false))
		require.NoError(t, ro.storage.PutBucketReplication("src",
			buildReplicationCfg("arn:aws:s3:::dst", "", "Enabled")))

		// Write an object marked as a REPLICA via the storage layer, then check
		// that replicateObject does nothing when the src meta has REPLICA status.
		_, err := ro.storage.PutObject("src", "replica.txt", strings.NewReader("x"),
			"text/plain", nil, "", "", false, "", nil, nil, "")
		require.NoError(t, err)
		require.NoError(
			t,
			ro.storage.SetObjectReplicationStatus("src", "replica.txt", ReplicationStatusReplica),
		)

		srcMeta, err := ro.storage.HeadObject("src", "replica.txt")
		require.NoError(t, err)
		ro.replicateObject("src", "replica.txt", srcMeta)

		_, _, err = ro.storage.GetObject("dst", "replica.txt")
		assert.ErrorIs(t, err, ErrObjectNotFound)
	})

	t.Run("replication-status header on HeadObject", func(t *testing.T) {
		ro := newTestRouter(t)
		require.NoError(t, ro.storage.CreateBucket("src", "us-east-1", false))
		require.NoError(t, ro.storage.CreateBucket("dst", "us-east-1", false))
		require.NoError(t, ro.storage.PutBucketReplication("src",
			buildReplicationCfg("arn:aws:s3:::dst", "", "Enabled")))

		req := httptest.NewRequest(http.MethodPut, "/src/item.txt",
			strings.NewReader("val"))
		req.Header.Set("Content-Type", "text/plain")
		rr := httptest.NewRecorder()
		ro.ServeHTTP(rr, req)
		require.Equal(t, http.StatusOK, rr.Code)

		head := httptest.NewRequest(http.MethodHead, "/src/item.txt", nil)
		hr := httptest.NewRecorder()
		ro.ServeHTTP(hr, head)
		require.Equal(t, http.StatusOK, hr.Code)
		assert.Equal(t, "COMPLETED", hr.Header().Get("X-Amz-Replication-Status"))

		// replica side
		head2 := httptest.NewRequest(http.MethodHead, "/dst/item.txt", nil)
		hr2 := httptest.NewRecorder()
		ro.ServeHTTP(hr2, head2)
		require.Equal(t, http.StatusOK, hr2.Code)
		assert.Equal(t, "REPLICA", hr2.Header().Get("X-Amz-Replication-Status"))
	})

	t.Run("replication via CopyObject", func(t *testing.T) {
		ro := newTestRouter(t)
		require.NoError(t, ro.storage.CreateBucket("src", "us-east-1", false))
		require.NoError(t, ro.storage.CreateBucket("dst", "us-east-1", false))
		require.NoError(t, ro.storage.CreateBucket("replica", "us-east-1", false))
		require.NoError(t, ro.storage.PutBucketReplication("dst",
			buildReplicationCfg("arn:aws:s3:::replica", "", "Enabled")))

		_, err := ro.storage.PutObject("src", "orig.txt", strings.NewReader("data"),
			"text/plain", nil, "", "", false, "", nil, nil, "")
		require.NoError(t, err)

		req := httptest.NewRequest(http.MethodPut, "/dst/orig.txt", nil)
		req.Header.Set("X-Amz-Copy-Source", "/src/orig.txt")
		rr := httptest.NewRecorder()
		ro.ServeHTTP(rr, req)
		require.Equal(t, http.StatusOK, rr.Code)

		_, _, err = ro.storage.GetObject("replica", "orig.txt")
		assert.NoError(t, err)
	})

	t.Run("replication via CompleteMultipartUpload", func(t *testing.T) {
		ro := newTestRouter(t)
		require.NoError(t, ro.storage.CreateBucket("src", "us-east-1", false))
		require.NoError(t, ro.storage.CreateBucket("dst", "us-east-1", false))
		require.NoError(t, ro.storage.PutBucketReplication("src",
			buildReplicationCfg("arn:aws:s3:::dst", "", "Enabled")))

		// Create MPU
		createReq := httptest.NewRequest(http.MethodPost, "/src/mpu.bin?uploads", nil)
		createRR := httptest.NewRecorder()
		ro.ServeHTTP(createRR, createReq)
		require.Equal(t, http.StatusOK, createRR.Code)

		var initResp initiateMultipartUploadResult
		require.NoError(t, xml.Unmarshal(createRR.Body.Bytes(), &initResp))
		uploadID := initResp.UploadID

		// Upload one part (≥5 MB to satisfy min-part-size; last part is exempt)
		body := strings.Repeat("x", 5*1024*1024+1)
		partReq := httptest.NewRequest(http.MethodPut,
			"/src/mpu.bin?partNumber=1&uploadId="+uploadID,
			strings.NewReader(body))
		partRR := httptest.NewRecorder()
		ro.ServeHTTP(partRR, partReq)
		require.Equal(t, http.StatusOK, partRR.Code)
		etag := partRR.Header().Get("ETag")

		// Complete
		completeBody := `<CompleteMultipartUpload>` +
			`<Part><PartNumber>1</PartNumber><ETag>` + etag + `</ETag></Part>` +
			`</CompleteMultipartUpload>`
		completeReq := httptest.NewRequest(http.MethodPost,
			"/src/mpu.bin?uploadId="+uploadID,
			strings.NewReader(completeBody))
		completeRR := httptest.NewRecorder()
		ro.ServeHTTP(completeRR, completeReq)
		require.Equal(t, http.StatusOK, completeRR.Code)

		_, _, err := ro.storage.GetObject("dst", "mpu.bin")
		assert.NoError(t, err)
	})

	t.Run("malformed replication config is silently skipped", func(t *testing.T) {
		ro := newTestRouter(t)
		require.NoError(t, ro.storage.CreateBucket("src", "us-east-1", false))
		// Inject malformed XML directly (bypasses handler validation)
		require.NoError(t, ro.storage.PutBucketReplication("src", "not-valid-xml"))

		req := httptest.NewRequest(http.MethodPut, "/src/obj.txt", strings.NewReader("body"))
		req.Header.Set("Content-Type", "text/plain")
		rr := httptest.NewRecorder()
		ro.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusOK, rr.Code)
	})

	t.Run("empty destination ARN is skipped", func(t *testing.T) {
		ro := newTestRouter(t)
		require.NoError(t, ro.storage.CreateBucket("src", "us-east-1", false))
		// Config with empty Bucket element resolves to an empty bucket name
		require.NoError(t, ro.storage.PutBucketReplication("src",
			buildReplicationCfg("", "", "Enabled")))

		req := httptest.NewRequest(http.MethodPut, "/src/obj.txt", strings.NewReader("body"))
		req.Header.Set("Content-Type", "text/plain")
		rr := httptest.NewRecorder()
		ro.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusOK, rr.Code)
	})

	t.Run("copy failure is silently logged", func(t *testing.T) {
		ro := newTestRouter(t)
		require.NoError(t, ro.storage.CreateBucket("src", "us-east-1", false))
		// Destination bucket does not exist → CopyObject returns ErrBucketNotFound
		require.NoError(t, ro.storage.PutBucketReplication("src",
			buildReplicationCfg("arn:aws:s3:::nonexistent-bucket", "", "Enabled")))

		req := httptest.NewRequest(http.MethodPut, "/src/obj.txt", strings.NewReader("body"))
		req.Header.Set("Content-Type", "text/plain")
		rr := httptest.NewRecorder()
		ro.ServeHTTP(rr, req)
		require.Equal(t, http.StatusOK, rr.Code)

		// Source object should exist but have no replication status (copy failed)
		srcMeta, err := ro.storage.HeadObject("src", "obj.txt")
		require.NoError(t, err)
		assert.Empty(t, srcMeta.ReplicationStatus)
	})

	t.Run("object tags are copied to destination", func(t *testing.T) {
		ro := newTestRouter(t)
		require.NoError(t, ro.storage.CreateBucket("src", "us-east-1", false))
		require.NoError(t, ro.storage.CreateBucket("dst", "us-east-1", false))
		require.NoError(t, ro.storage.PutBucketReplication("src",
			buildReplicationCfg("arn:aws:s3:::dst", "", "Enabled")))

		_, err := ro.storage.PutObject("src", "tagged.txt", strings.NewReader("data"),
			"text/plain", nil, "", "", false, "", nil, nil, "")
		require.NoError(t, err)
		require.NoError(t, ro.storage.PutObjectTagging("src", "tagged.txt", []Tag{
			{Key: "env", Value: "prod"},
			{Key: "team", Value: "platform"},
		}))

		srcMeta, err := ro.storage.HeadObject("src", "tagged.txt")
		require.NoError(t, err)
		ro.replicateObject("src", "tagged.txt", srcMeta)

		tags, err := ro.storage.GetObjectTagging("dst", "tagged.txt")
		require.NoError(t, err)
		assert.Equal(t, []Tag{{Key: "env", Value: "prod"}, {Key: "team", Value: "platform"}}, tags)
	})

	t.Run("Filter.Tag: matching tag causes replication", func(t *testing.T) {
		ro := newTestRouter(t)
		require.NoError(t, ro.storage.CreateBucket("src", "us-east-1", false))
		require.NoError(t, ro.storage.CreateBucket("dst", "us-east-1", false))
		require.NoError(t, ro.storage.PutBucketReplication("src",
			buildReplicationCfgWithFilterTag("arn:aws:s3:::dst", "env", "prod")))

		_, err := ro.storage.PutObject("src", "obj.txt", strings.NewReader("data"),
			"text/plain", nil, "", "", false, "", nil, nil, "")
		require.NoError(t, err)
		require.NoError(t, ro.storage.PutObjectTagging("src", "obj.txt", []Tag{
			{Key: "env", Value: "prod"},
		}))

		srcMeta, err := ro.storage.HeadObject("src", "obj.txt")
		require.NoError(t, err)
		ro.replicateObject("src", "obj.txt", srcMeta)

		_, _, err = ro.storage.GetObject("dst", "obj.txt")
		assert.NoError(t, err)
	})

	t.Run("Filter.Tag: non-matching tag suppresses replication", func(t *testing.T) {
		ro := newTestRouter(t)
		require.NoError(t, ro.storage.CreateBucket("src", "us-east-1", false))
		require.NoError(t, ro.storage.CreateBucket("dst", "us-east-1", false))
		require.NoError(t, ro.storage.PutBucketReplication("src",
			buildReplicationCfgWithFilterTag("arn:aws:s3:::dst", "env", "prod")))

		_, err := ro.storage.PutObject("src", "obj.txt", strings.NewReader("data"),
			"text/plain", nil, "", "", false, "", nil, nil, "")
		require.NoError(t, err)
		require.NoError(t, ro.storage.PutObjectTagging("src", "obj.txt", []Tag{
			{Key: "env", Value: "staging"},
		}))

		srcMeta, err := ro.storage.HeadObject("src", "obj.txt")
		require.NoError(t, err)
		ro.replicateObject("src", "obj.txt", srcMeta)

		_, _, err = ro.storage.GetObject("dst", "obj.txt")
		assert.ErrorIs(t, err, ErrObjectNotFound)
	})

	t.Run("Filter.Tag: no tags on object suppresses replication", func(t *testing.T) {
		ro := newTestRouter(t)
		require.NoError(t, ro.storage.CreateBucket("src", "us-east-1", false))
		require.NoError(t, ro.storage.CreateBucket("dst", "us-east-1", false))
		require.NoError(t, ro.storage.PutBucketReplication("src",
			buildReplicationCfgWithFilterTag("arn:aws:s3:::dst", "env", "prod")))

		_, err := ro.storage.PutObject("src", "untagged.txt", strings.NewReader("data"),
			"text/plain", nil, "", "", false, "", nil, nil, "")
		require.NoError(t, err)

		srcMeta, err := ro.storage.HeadObject("src", "untagged.txt")
		require.NoError(t, err)
		ro.replicateObject("src", "untagged.txt", srcMeta)

		_, _, err = ro.storage.GetObject("dst", "untagged.txt")
		assert.ErrorIs(t, err, ErrObjectNotFound)
	})

	t.Run("Filter.And.Tags: all tags match causes replication", func(t *testing.T) {
		ro := newTestRouter(t)
		require.NoError(t, ro.storage.CreateBucket("src", "us-east-1", false))
		require.NoError(t, ro.storage.CreateBucket("dst", "us-east-1", false))
		require.NoError(t, ro.storage.PutBucketReplication("src",
			buildReplicationCfgWithAndTags("arn:aws:s3:::dst", "", []xmlTag{
				{Key: "env", Value: "prod"},
				{Key: "team", Value: "platform"},
			})))

		_, err := ro.storage.PutObject("src", "obj.txt", strings.NewReader("data"),
			"text/plain", nil, "", "", false, "", nil, nil, "")
		require.NoError(t, err)
		require.NoError(t, ro.storage.PutObjectTagging("src", "obj.txt", []Tag{
			{Key: "env", Value: "prod"},
			{Key: "team", Value: "platform"},
		}))

		srcMeta, err := ro.storage.HeadObject("src", "obj.txt")
		require.NoError(t, err)
		ro.replicateObject("src", "obj.txt", srcMeta)

		_, _, err = ro.storage.GetObject("dst", "obj.txt")
		assert.NoError(t, err)
	})

	t.Run("Filter.And.Tags: partial tag match suppresses replication", func(t *testing.T) {
		ro := newTestRouter(t)
		require.NoError(t, ro.storage.CreateBucket("src", "us-east-1", false))
		require.NoError(t, ro.storage.CreateBucket("dst", "us-east-1", false))
		require.NoError(t, ro.storage.PutBucketReplication("src",
			buildReplicationCfgWithAndTags("arn:aws:s3:::dst", "", []xmlTag{
				{Key: "env", Value: "prod"},
				{Key: "team", Value: "platform"},
			})))

		_, err := ro.storage.PutObject("src", "obj.txt", strings.NewReader("data"),
			"text/plain", nil, "", "", false, "", nil, nil, "")
		require.NoError(t, err)
		require.NoError(t, ro.storage.PutObjectTagging("src", "obj.txt", []Tag{
			{Key: "env", Value: "prod"},
			// "team" tag is absent
		}))

		srcMeta, err := ro.storage.HeadObject("src", "obj.txt")
		require.NoError(t, err)
		ro.replicateObject("src", "obj.txt", srcMeta)

		_, _, err = ro.storage.GetObject("dst", "obj.txt")
		assert.ErrorIs(t, err, ErrObjectNotFound)
	})

	t.Run("Filter.And prefix+tag: both match causes replication", func(t *testing.T) {
		ro := newTestRouter(t)
		require.NoError(t, ro.storage.CreateBucket("src", "us-east-1", false))
		require.NoError(t, ro.storage.CreateBucket("dst", "us-east-1", false))
		require.NoError(t, ro.storage.PutBucketReplication("src",
			buildReplicationCfgWithAndTags("arn:aws:s3:::dst", "logs/", []xmlTag{
				{Key: "env", Value: "prod"},
			})))

		_, err := ro.storage.PutObject("src", "logs/2026.log", strings.NewReader("log"),
			"text/plain", nil, "", "", false, "", nil, nil, "")
		require.NoError(t, err)
		require.NoError(t, ro.storage.PutObjectTagging("src", "logs/2026.log", []Tag{
			{Key: "env", Value: "prod"},
		}))

		srcMeta, err := ro.storage.HeadObject("src", "logs/2026.log")
		require.NoError(t, err)
		ro.replicateObject("src", "logs/2026.log", srcMeta)

		_, _, err = ro.storage.GetObject("dst", "logs/2026.log")
		assert.NoError(t, err)
	})

	t.Run(
		"Filter.And prefix+tag: prefix matches but tag absent suppresses replication",
		func(t *testing.T) {
			ro := newTestRouter(t)
			require.NoError(t, ro.storage.CreateBucket("src", "us-east-1", false))
			require.NoError(t, ro.storage.CreateBucket("dst", "us-east-1", false))
			require.NoError(t, ro.storage.PutBucketReplication("src",
				buildReplicationCfgWithAndTags("arn:aws:s3:::dst", "logs/", []xmlTag{
					{Key: "env", Value: "prod"},
				})))

			_, err := ro.storage.PutObject("src", "logs/2026.log", strings.NewReader("log"),
				"text/plain", nil, "", "", false, "", nil, nil, "")
			require.NoError(t, err)
			// object has no tags

			srcMeta, err := ro.storage.HeadObject("src", "logs/2026.log")
			require.NoError(t, err)
			ro.replicateObject("src", "logs/2026.log", srcMeta)

			_, _, err = ro.storage.GetObject("dst", "logs/2026.log")
			assert.ErrorIs(t, err, ErrObjectNotFound)
		},
	)

	t.Run("replication-status header on GetObject", func(t *testing.T) {
		ro := newTestRouter(t)
		require.NoError(t, ro.storage.CreateBucket("src", "us-east-1", false))
		require.NoError(t, ro.storage.CreateBucket("dst", "us-east-1", false))
		require.NoError(t, ro.storage.PutBucketReplication("src",
			buildReplicationCfg("arn:aws:s3:::dst", "", "Enabled")))

		req := httptest.NewRequest(http.MethodPut, "/src/item.txt", strings.NewReader("val"))
		req.Header.Set("Content-Type", "text/plain")
		rr := httptest.NewRecorder()
		ro.ServeHTTP(rr, req)
		require.Equal(t, http.StatusOK, rr.Code)

		get := httptest.NewRequest(http.MethodGet, "/src/item.txt", nil)
		gr := httptest.NewRecorder()
		ro.ServeHTTP(gr, get)
		require.Equal(t, http.StatusOK, gr.Code)
		assert.Equal(t, "COMPLETED", gr.Header().Get("X-Amz-Replication-Status"))

		get2 := httptest.NewRequest(http.MethodGet, "/dst/item.txt", nil)
		gr2 := httptest.NewRecorder()
		ro.ServeHTTP(gr2, get2)
		require.Equal(t, http.StatusOK, gr2.Code)
		assert.Equal(t, "REPLICA", gr2.Header().Get("X-Amz-Replication-Status"))
	})
}

func TestReplicateDeleteMarker(t *testing.T) {
	// putObject creates an object and returns its version ID.
	putObject := func(t *testing.T, ro *Router, bucket, key, body string) {
		t.Helper()
		req := httptest.NewRequest(http.MethodPut, "/"+bucket+"/"+key, strings.NewReader(body))
		req.Header.Set("Content-Type", "text/plain")
		rr := httptest.NewRecorder()
		ro.ServeHTTP(rr, req)
		require.Equal(t, http.StatusOK, rr.Code)
	}

	// deleteObject performs a DELETE and returns (versionID, isDeleteMarker).
	deleteObject := func(t *testing.T, ro *Router, bucket, key string) (string, bool) {
		t.Helper()
		req := httptest.NewRequest(http.MethodDelete, "/"+bucket+"/"+key, nil)
		rr := httptest.NewRecorder()
		ro.ServeHTTP(rr, req)
		require.Equal(t, http.StatusNoContent, rr.Code)
		return rr.Header().Get("x-amz-version-id"), rr.Header().Get("x-amz-delete-marker") == "true"
	}

	t.Run("delete marker is replicated when DMR enabled", func(t *testing.T) {
		ro := newTestRouter(t)
		require.NoError(t, ro.storage.CreateBucket("src", "us-east-1", false))
		require.NoError(t, ro.storage.CreateBucket("dst", "us-east-1", false))
		enableVersioning(t, ro, "src")
		enableVersioning(t, ro, "dst")
		require.NoError(t, ro.storage.PutBucketReplication("src",
			buildReplicationCfgWithDMR("arn:aws:s3:::dst", "", "Enabled", "Enabled")))

		putObject(t, ro, "src", "obj.txt", "hello")
		_, isMarker := deleteObject(t, ro, "src", "obj.txt")
		require.True(t, isMarker)

		// destination should have a delete marker
		_, dstMarkers, err := ro.storage.ListObjectVersions("dst")
		require.NoError(t, err)
		assert.Len(t, dstMarkers, 1)
		assert.Equal(t, "obj.txt", dstMarkers[0].Key)

		// GET on destination should return 404 with x-amz-delete-marker: true
		// (unversioned access to a delete marker returns 404 per S3 spec)
		get := httptest.NewRequest(http.MethodGet, "/dst/obj.txt", nil)
		gr := httptest.NewRecorder()
		ro.ServeHTTP(gr, get)
		assert.Equal(t, http.StatusNotFound, gr.Code)
		assert.Equal(t, "true", gr.Header().Get("x-amz-delete-marker"))
	})

	t.Run("delete marker not replicated when DMR disabled", func(t *testing.T) {
		ro := newTestRouter(t)
		require.NoError(t, ro.storage.CreateBucket("src", "us-east-1", false))
		require.NoError(t, ro.storage.CreateBucket("dst", "us-east-1", false))
		enableVersioning(t, ro, "src")
		enableVersioning(t, ro, "dst")
		require.NoError(t, ro.storage.PutBucketReplication("src",
			buildReplicationCfgWithDMR("arn:aws:s3:::dst", "", "Enabled", "Disabled")))

		putObject(t, ro, "src", "obj.txt", "hello")
		_, isMarker := deleteObject(t, ro, "src", "obj.txt")
		require.True(t, isMarker)

		_, dstMarkers, err := ro.storage.ListObjectVersions("dst")
		require.NoError(t, err)
		assert.Empty(t, dstMarkers)
	})

	t.Run("delete marker not replicated when DMR element absent", func(t *testing.T) {
		ro := newTestRouter(t)
		require.NoError(t, ro.storage.CreateBucket("src", "us-east-1", false))
		require.NoError(t, ro.storage.CreateBucket("dst", "us-east-1", false))
		enableVersioning(t, ro, "src")
		enableVersioning(t, ro, "dst")
		// Config without DeleteMarkerReplication element
		require.NoError(t, ro.storage.PutBucketReplication("src",
			buildReplicationCfg("arn:aws:s3:::dst", "", "Enabled")))

		putObject(t, ro, "src", "obj.txt", "hello")
		_, isMarker := deleteObject(t, ro, "src", "obj.txt")
		require.True(t, isMarker)

		_, dstMarkers, err := ro.storage.ListObjectVersions("dst")
		require.NoError(t, err)
		assert.Empty(t, dstMarkers)
	})

	t.Run("prefix filter is respected", func(t *testing.T) {
		ro := newTestRouter(t)
		require.NoError(t, ro.storage.CreateBucket("src", "us-east-1", false))
		require.NoError(t, ro.storage.CreateBucket("dst", "us-east-1", false))
		enableVersioning(t, ro, "src")
		enableVersioning(t, ro, "dst")
		require.NoError(t, ro.storage.PutBucketReplication("src",
			buildReplicationCfgWithDMR("arn:aws:s3:::dst", "logs/", "Enabled", "Enabled")))

		putObject(t, ro, "src", "logs/2026.log", "log")
		_, isMarker := deleteObject(t, ro, "src", "logs/2026.log")
		require.True(t, isMarker)

		_, dstMarkers, err := ro.storage.ListObjectVersions("dst")
		require.NoError(t, err)
		assert.Len(t, dstMarkers, 1)

		// key outside prefix is not replicated
		putObject(t, ro, "src", "data/file.txt", "data")
		_, isMarker2 := deleteObject(t, ro, "src", "data/file.txt")
		require.True(t, isMarker2)

		_, dstMarkers2, err := ro.storage.ListObjectVersions("dst")
		require.NoError(t, err)
		assert.Len(t, dstMarkers2, 1) // still 1, data/file.txt not replicated
	})

	t.Run("disabled rule is skipped", func(t *testing.T) {
		ro := newTestRouter(t)
		require.NoError(t, ro.storage.CreateBucket("src", "us-east-1", false))
		require.NoError(t, ro.storage.CreateBucket("dst", "us-east-1", false))
		enableVersioning(t, ro, "src")
		enableVersioning(t, ro, "dst")
		require.NoError(t, ro.storage.PutBucketReplication("src",
			buildReplicationCfgWithDMR("arn:aws:s3:::dst", "", "Disabled", "Enabled")))

		putObject(t, ro, "src", "obj.txt", "hello")
		_, isMarker := deleteObject(t, ro, "src", "obj.txt")
		require.True(t, isMarker)

		_, dstMarkers, err := ro.storage.ListObjectVersions("dst")
		require.NoError(t, err)
		assert.Empty(t, dstMarkers)
	})

	t.Run("non-versioned delete does not trigger DMR", func(t *testing.T) {
		ro := newTestRouter(t)
		require.NoError(t, ro.storage.CreateBucket("src", "us-east-1", false))
		require.NoError(t, ro.storage.CreateBucket("dst", "us-east-1", false))
		// versioning NOT enabled on src — DeleteObjectVersioned produces no marker
		require.NoError(t, ro.storage.PutBucketReplication("src",
			buildReplicationCfgWithDMR("arn:aws:s3:::dst", "", "Enabled", "Enabled")))

		putObject(t, ro, "src", "obj.txt", "hello")
		_, isMarker := deleteObject(t, ro, "src", "obj.txt")
		require.False(t, isMarker)

		_, dstMarkers, err := ro.storage.ListObjectVersions("dst")
		require.NoError(t, err)
		assert.Empty(t, dstMarkers)
	})

	t.Run("failure is silently logged", func(t *testing.T) {
		ro := newTestRouter(t)
		require.NoError(t, ro.storage.CreateBucket("src", "us-east-1", false))
		enableVersioning(t, ro, "src")
		// destination bucket does not exist → DeleteObjectVersioned returns ErrBucketNotFound
		require.NoError(t, ro.storage.PutBucketReplication("src",
			buildReplicationCfgWithDMR("arn:aws:s3:::nonexistent", "", "Enabled", "Enabled")))

		putObject(t, ro, "src", "obj.txt", "hello")
		_, isMarker := deleteObject(t, ro, "src", "obj.txt")
		require.True(t, isMarker)
		// test passes as long as no panic and response was 204
	})

	t.Run("malformed replication config is silently skipped", func(t *testing.T) {
		ro := newTestRouter(t)
		require.NoError(t, ro.storage.CreateBucket("src", "us-east-1", false))
		enableVersioning(t, ro, "src")
		require.NoError(t, ro.storage.PutBucketReplication("src", "not-valid-xml"))

		putObject(t, ro, "src", "obj.txt", "hello")
		_, isMarker := deleteObject(t, ro, "src", "obj.txt")
		require.True(t, isMarker)
		// test passes as long as no panic and response was 204
	})

	t.Run("empty destination ARN is skipped", func(t *testing.T) {
		ro := newTestRouter(t)
		require.NoError(t, ro.storage.CreateBucket("src", "us-east-1", false))
		enableVersioning(t, ro, "src")
		require.NoError(t, ro.storage.PutBucketReplication("src",
			buildReplicationCfgWithDMR("", "", "Enabled", "Enabled")))

		putObject(t, ro, "src", "obj.txt", "hello")
		_, isMarker := deleteObject(t, ro, "src", "obj.txt")
		require.True(t, isMarker)
		// test passes as long as no panic and response was 204
	})

	t.Run("delete marker replicated via DeleteObjects batch", func(t *testing.T) {
		ro := newTestRouter(t)
		require.NoError(t, ro.storage.CreateBucket("src", "us-east-1", false))
		require.NoError(t, ro.storage.CreateBucket("dst", "us-east-1", false))
		enableVersioning(t, ro, "src")
		enableVersioning(t, ro, "dst")
		require.NoError(t, ro.storage.PutBucketReplication("src",
			buildReplicationCfgWithDMR("arn:aws:s3:::dst", "", "Enabled", "Enabled")))

		putObject(t, ro, "src", "a.txt", "a")
		putObject(t, ro, "src", "b.txt", "b")

		body := `<Delete><Object><Key>a.txt</Key></Object><Object><Key>b.txt</Key></Object></Delete>`
		req := httptest.NewRequest(http.MethodPost, "/src?delete", strings.NewReader(body))
		rr := httptest.NewRecorder()
		ro.ServeHTTP(rr, req)
		require.Equal(t, http.StatusOK, rr.Code)

		_, dstMarkers, err := ro.storage.ListObjectVersions("dst")
		require.NoError(t, err)
		assert.Len(t, dstMarkers, 2)
	})

	t.Run("explicit-version delete does not trigger DMR", func(t *testing.T) {
		ro := newTestRouter(t)
		require.NoError(t, ro.storage.CreateBucket("src", "us-east-1", false))
		require.NoError(t, ro.storage.CreateBucket("dst", "us-east-1", false))
		enableVersioning(t, ro, "src")
		enableVersioning(t, ro, "dst")
		require.NoError(t, ro.storage.PutBucketReplication("src",
			buildReplicationCfgWithDMR("arn:aws:s3:::dst", "", "Enabled", "Enabled")))

		putReq := httptest.NewRequest(http.MethodPut, "/src/obj.txt", strings.NewReader("hello"))
		putReq.Header.Set("Content-Type", "text/plain")
		putRR := httptest.NewRecorder()
		ro.ServeHTTP(putRR, putReq)
		require.Equal(t, http.StatusOK, putRR.Code)
		versionID := putRR.Header().Get("x-amz-version-id")
		require.NotEmpty(t, versionID)

		delReq := httptest.NewRequest(http.MethodDelete, "/src/obj.txt?versionId="+versionID, nil)
		delRR := httptest.NewRecorder()
		ro.ServeHTTP(delRR, delReq)
		require.Equal(t, http.StatusNoContent, delRR.Code)

		_, dstMarkers, err := ro.storage.ListObjectVersions("dst")
		require.NoError(t, err)
		assert.Empty(t, dstMarkers)
	})

	t.Run("explicit-version batch delete does not trigger DMR", func(t *testing.T) {
		ro := newTestRouter(t)
		require.NoError(t, ro.storage.CreateBucket("src", "us-east-1", false))
		require.NoError(t, ro.storage.CreateBucket("dst", "us-east-1", false))
		enableVersioning(t, ro, "src")
		enableVersioning(t, ro, "dst")
		require.NoError(t, ro.storage.PutBucketReplication("src",
			buildReplicationCfgWithDMR("arn:aws:s3:::dst", "", "Enabled", "Enabled")))

		putAndGetVersionID := func(key, body string) string {
			t.Helper()
			req := httptest.NewRequest(http.MethodPut, "/src/"+key, strings.NewReader(body))
			req.Header.Set("Content-Type", "text/plain")
			rr := httptest.NewRecorder()
			ro.ServeHTTP(rr, req)
			require.Equal(t, http.StatusOK, rr.Code)
			id := rr.Header().Get("x-amz-version-id")
			require.NotEmpty(t, id)
			return id
		}

		vidA := putAndGetVersionID("a.txt", "a")
		vidB := putAndGetVersionID("b.txt", "b")

		body := fmt.Sprintf(
			`<Delete><Object><Key>a.txt</Key><VersionId>%s</VersionId></Object><Object><Key>b.txt</Key><VersionId>%s</VersionId></Object></Delete>`,
			vidA,
			vidB,
		)
		req := httptest.NewRequest(http.MethodPost, "/src?delete", strings.NewReader(body))
		rr := httptest.NewRecorder()
		ro.ServeHTTP(rr, req)
		require.Equal(t, http.StatusOK, rr.Code)

		_, dstMarkers, err := ro.storage.ListObjectVersions("dst")
		require.NoError(t, err)
		assert.Empty(t, dstMarkers)
	})
}

func TestSetObjectReplicationStatus(t *testing.T) {
	t.Run("bucket not found", func(t *testing.T) {
		s := newTestStorage(t)
		err := s.SetObjectReplicationStatus("nonexistent", "key.txt", ReplicationStatusCompleted)
		assert.ErrorIs(t, err, ErrBucketNotFound)
	})
	t.Run("object not found", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateBucket("bucket", "us-east-1", false))
		err := s.SetObjectReplicationStatus("bucket", "missing.txt", ReplicationStatusCompleted)
		assert.ErrorIs(t, err, ErrObjectNotFound)
	})
	t.Run("invalid status is rejected", func(t *testing.T) {
		s := newTestStorage(t)
		require.NoError(t, s.CreateBucket("bucket", "us-east-1", false))
		err := s.SetObjectReplicationStatus("bucket", "key.txt", "INVALID")
		assert.Error(t, err)
	})
}
