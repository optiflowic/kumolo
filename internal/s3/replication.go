package s3

import (
	"encoding/xml"
	"log/slog"
	"strings"
)

// replicationConfig is the parsed form of the ReplicationConfiguration XML stored
// by PutBucketReplication.
type replicationConfig struct {
	XMLName xml.Name          `xml:"ReplicationConfiguration"`
	Rules   []replicationRule `xml:"Rule"`
}

type replicationRule struct {
	ID                      string                   `xml:"ID"`
	Status                  string                   `xml:"Status"`
	Prefix                  string                   `xml:"Prefix"` // deprecated top-level prefix
	Filter                  *replicationFilter       `xml:"Filter"`
	Destination             replicationDest          `xml:"Destination"`
	DeleteMarkerReplication *deleteMarkerReplication `xml:"DeleteMarkerReplication"`
}

type deleteMarkerReplication struct {
	Status string `xml:"Status"`
}

type replicationFilter struct {
	Prefix string                `xml:"Prefix"`
	Tag    *xmlTag               `xml:"Tag"`
	And    *replicationFilterAnd `xml:"And"`
}

type replicationFilterAnd struct {
	Prefix string   `xml:"Prefix"`
	Tags   []xmlTag `xml:"Tag"`
}

type replicationDest struct {
	Bucket       string `xml:"Bucket"`
	StorageClass string `xml:"StorageClass"`
}

// replicateObject copies the object at bucket/key to each enabled replication destination
// whose prefix filter matches key. Objects already marked as REPLICA are not re-replicated
// to prevent cascading. Errors are logged and never propagated to the caller.
func (ro *Router) replicateObject(bucket, key string, srcMeta ObjectMetadata) {
	if srcMeta.ReplicationStatus == ReplicationStatusReplica {
		return
	}

	cfgXML, err := ro.storage.GetBucketReplication(bucket)
	if err != nil || cfgXML == "" {
		return
	}

	var cfg replicationConfig
	// cfgXML is from a prior authenticated request, not direct external input.
	if err := xml.Unmarshal([]byte(cfgXML), &cfg); err != nil { //nolint:gosec // G709
		slog.Warn("replication: failed to parse config", "bucket", bucket, "err", err)
		return
	}

	var (
		objTagsLoaded bool
		objTags       []Tag
	)

	for _, rule := range cfg.Rules {
		if rule.Status != "Enabled" {
			continue
		}
		if prefix := ruleKeyPrefix(rule); !strings.HasPrefix(key, prefix) {
			continue
		}
		if ruleHasTagFilter(rule) {
			if !objTagsLoaded {
				objTagsLoaded = true
				objTags, _ = ro.storage.GetObjectTagging(bucket, key)
			}
			if !ruleMatchesTags(rule, objTags) {
				continue
			}
		}
		destBucket := bucketNameFromARN(rule.Destination.Bucket)
		if destBucket == "" {
			slog.Warn( // #nosec G706 -- bucket/key come from URL path; log injection risk accepted for a local dev emulator
				"replication: invalid destination ARN",
				"bucket",
				bucket,
				"arn",
				rule.Destination.Bucket,
			)
			continue
		}

		_, copyErr := ro.storage.CopyObject(
			bucket, key, srcMeta.VersionID,
			destBucket, key,
			srcMeta.ContentType,
			srcMeta.UserMetadata,
			srcMeta.SSEAlgorithm, srcMeta.SSEKMSKeyID, srcMeta.SSEBucketKeyEnabled, "",
			srcMeta.Retention, srcMeta.LegalHold,
			rule.Destination.StorageClass,
			nil, // COPY: replicate source tags
		)
		if copyErr != nil {
			slog.Warn( // #nosec G706 -- bucket/key come from URL path; log injection risk accepted for a local dev emulator
				"replication: copy failed",
				"src_bucket",
				bucket,
				"key",
				key,
				"dest_bucket",
				destBucket,
				"err",
				copyErr,
			)
			continue
		}

		if err := ro.storage.SetObjectReplicationStatus(destBucket, key, ReplicationStatusReplica); err != nil {
			// untestable: storage write failure after a successful copy cannot be injected via current test helpers
			slog.Warn( // #nosec G706 -- bucket/key come from URL path; log injection risk accepted for a local dev emulator
				"replication: failed to set REPLICA status",
				"dest_bucket",
				destBucket,
				"key",
				key,
				"err",
				err,
			)
		}
		if err := ro.storage.SetObjectReplicationStatus(bucket, key, ReplicationStatusCompleted); err != nil {
			// untestable: storage write failure after a successful copy cannot be injected via current test helpers
			slog.Warn( // #nosec G706 -- bucket/key come from URL path; log injection risk accepted for a local dev emulator
				"replication: failed to set COMPLETED status",
				"bucket",
				bucket,
				"key",
				key,
				"err",
				err,
			)
		}
		slog.Info( // #nosec G706 -- bucket/key come from URL path; log injection risk accepted for a local dev emulator
			"replication: object replicated",
			"src_bucket",
			bucket,
			"dest_bucket",
			destBucket,
			"key",
			key,
		)
	}
}

// ruleKeyPrefix returns the key prefix for a replication rule.
// Priority: Filter.And.Prefix > Filter.Prefix > deprecated top-level Prefix.
func ruleKeyPrefix(rule replicationRule) string {
	if rule.Filter != nil {
		if rule.Filter.And != nil {
			return rule.Filter.And.Prefix
		}
		return rule.Filter.Prefix
	}
	return rule.Prefix
}

// ruleHasTagFilter reports whether a rule has a tag-based filter (Filter.Tag or Filter.And.Tags).
func ruleHasTagFilter(rule replicationRule) bool {
	if rule.Filter == nil {
		return false
	}
	if rule.Filter.Tag != nil {
		return true
	}
	return rule.Filter.And != nil && len(rule.Filter.And.Tags) > 0
}

// ruleMatchesTags reports whether the object tags satisfy the tag filter of the rule.
// Returns true if the rule has no tag filter.
func ruleMatchesTags(rule replicationRule, objTags []Tag) bool {
	if rule.Filter == nil {
		return true
	}
	if rule.Filter.Tag != nil {
		return tagSetContains(objTags, *rule.Filter.Tag)
	}
	if rule.Filter.And != nil {
		for _, want := range rule.Filter.And.Tags {
			if !tagSetContains(objTags, want) {
				return false
			}
		}
	}
	return true
}

// tagSetContains reports whether tags contains an entry matching want.
func tagSetContains(tags []Tag, want xmlTag) bool {
	for _, t := range tags {
		if t.Key == want.Key && t.Value == want.Value {
			return true
		}
	}
	return false
}

// replicateDeleteMarker propagates a delete marker from bucket/key to each enabled
// replication destination whose rule has DeleteMarkerReplication.Status=Enabled.
// Errors are logged and never propagated to the caller.
func (ro *Router) replicateDeleteMarker(bucket, key string) {
	cfgXML, err := ro.storage.GetBucketReplication(bucket)
	if err != nil || cfgXML == "" {
		return
	}

	var cfg replicationConfig
	// cfgXML is from a prior authenticated request, not direct external input.
	if err := xml.Unmarshal([]byte(cfgXML), &cfg); err != nil { //nolint:gosec // G709
		slog.Warn("replication: failed to parse config", "bucket", bucket, "err", err)
		return
	}

	for _, rule := range cfg.Rules {
		if rule.Status != "Enabled" {
			continue
		}
		if rule.DeleteMarkerReplication == nil || rule.DeleteMarkerReplication.Status != "Enabled" {
			continue
		}
		if prefix := ruleKeyPrefix(rule); !strings.HasPrefix(key, prefix) {
			continue
		}
		destBucket := bucketNameFromARN(rule.Destination.Bucket)
		if destBucket == "" {
			slog.Warn( // #nosec G706 -- bucket/key come from URL path; log injection risk accepted for a local dev emulator
				"replication: invalid destination ARN",
				"bucket",
				bucket,
				"arn",
				rule.Destination.Bucket,
			)
			continue
		}

		if _, _, err := ro.storage.DeleteObjectVersioned(destBucket, key, false); err != nil {
			slog.Warn( // #nosec G706 -- bucket/key come from URL path; log injection risk accepted for a local dev emulator
				"replication: delete marker replication failed",
				"src_bucket",
				bucket,
				"key",
				key,
				"dest_bucket",
				destBucket,
				"err",
				err,
			)
			continue
		}
		slog.Info( // #nosec G706 -- bucket/key come from URL path; log injection risk accepted for a local dev emulator
			"replication: delete marker replicated",
			"src_bucket",
			bucket,
			"dest_bucket",
			destBucket,
			"key",
			key,
		)
	}
}

// bucketNameFromARN extracts the bucket name from an S3 ARN
// (e.g. "arn:aws:s3:::my-bucket" → "my-bucket").
// Falls back to treating the input as a plain bucket name.
func bucketNameFromARN(arn string) string {
	// S3 bucket ARN format: arn:aws:s3:::bucket-name (5 colons total, 6 parts)
	parts := strings.SplitN(arn, ":", 6)
	if len(parts) == 6 {
		return parts[5]
	}
	return arn
}
