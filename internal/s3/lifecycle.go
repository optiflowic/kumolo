package s3

import (
	"context"
	"encoding/xml"
	"log/slog"
	"strings"
	"time"
)

// lifecycleStore is the subset of Storage required by LifecycleEnforcer.
type lifecycleStore interface {
	ListBuckets() ([]BucketInfo, error)
	GetBucketLifecycle(bucket string) (string, error)
	GetBucketVersioning(bucket string) (string, error)
	ListObjects(bucket string) ([]ObjectInfo, error)
	DeleteObject(bucket, key string, bypassGovernance bool) error
	DeleteObjectVersioned(
		bucket, key string,
		bypassGovernance bool,
	) (versionID string, isDeleteMarker bool, err error)
	ListObjectVersions(bucket string) ([]VersionInfo, []DeleteMarkerInfo, error)
	DeleteObjectVersion(bucket, key, versionID string, bypassGovernance bool) (bool, error)
	ListMultipartUploads(bucket string) ([]MultipartUploadInfo, error)
	AbortMultipartUpload(uploadID string) error
}

// LifecycleEnforcer periodically evaluates lifecycle rules against stored objects.
type LifecycleEnforcer struct {
	storage  lifecycleStore
	interval time.Duration
	now      func() time.Time
}

// NewLifecycleEnforcer returns a new LifecycleEnforcer that runs every interval.
func NewLifecycleEnforcer(storage lifecycleStore, interval time.Duration) *LifecycleEnforcer {
	return &LifecycleEnforcer{storage: storage, interval: interval, now: time.Now}
}

// Start runs lifecycle enforcement in a background goroutine until ctx is cancelled.
// An initial evaluation runs immediately on startup.
func (e *LifecycleEnforcer) Start(ctx context.Context) {
	go func() {
		e.runOnce()
		ticker := time.NewTicker(e.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C: // untestable without real-time coupling; tested via TestStart_TickerFiresRunOnce
				e.runOnce()
			case <-ctx.Done():
				return
			}
		}
	}()
}

func (e *LifecycleEnforcer) runOnce() {
	buckets, err := e.storage.ListBuckets()
	if err != nil {
		slog.Error("lifecycle: list buckets", "err", err)
		return
	}
	for _, b := range buckets {
		e.enforceBucket(b.Name)
	}
}

func (e *LifecycleEnforcer) enforceBucket(bucket string) {
	xmlBody, err := e.storage.GetBucketLifecycle(bucket)
	if err != nil {
		slog.Error("lifecycle: get lifecycle config", "bucket", bucket, "err", err)
		return
	}
	if xmlBody == "" {
		return
	}

	var cfg lifecycleConfiguration
	if err := xml.Unmarshal([]byte(xmlBody), &cfg); err != nil {
		slog.Error("lifecycle: parse config", "bucket", bucket, "err", err)
		return
	}

	versioning, err := e.storage.GetBucketVersioning(bucket)
	if err != nil {
		slog.Warn(
			"lifecycle: get versioning status, treating as unversioned",
			"bucket",
			bucket,
			"err",
			err,
		)
	}
	versioned := versioning == "Enabled"
	now := e.now()

	for _, rule := range cfg.Rules {
		if rule.Status != "Enabled" {
			continue
		}
		prefix := rule.effectivePrefix()

		if rule.Expiration != nil {
			if rule.Expiration.Days > 0 {
				e.enforceExpiration(
					bucket,
					prefix,
					now.AddDate(0, 0, -rule.Expiration.Days),
					versioned,
				)
			}
			if !rule.Expiration.Date.IsZero() && !now.Before(rule.Expiration.Date) {
				e.enforceExpiration(bucket, prefix, now, versioned)
			}
			if rule.Expiration.ExpiredObjectDeleteMarker {
				e.enforceExpiredObjectDeleteMarker(bucket, prefix)
			}
		}
		if rule.NoncurrentVersionExpiration != nil &&
			rule.NoncurrentVersionExpiration.NoncurrentDays > 0 {
			e.enforceNoncurrentExpiration(
				bucket,
				prefix,
				rule.NoncurrentVersionExpiration.NoncurrentDays,
				now,
			)
		}
		if rule.AbortIncompleteMultipartUpload != nil &&
			rule.AbortIncompleteMultipartUpload.DaysAfterInitiation > 0 {
			e.enforceAbortIncomplete(
				bucket,
				prefix,
				rule.AbortIncompleteMultipartUpload.DaysAfterInitiation,
				now,
			)
		}
	}
}

func (e *LifecycleEnforcer) enforceExpiration(
	bucket, prefix string,
	cutoff time.Time,
	versioned bool,
) {
	if !versioned {
		objects, err := e.storage.ListObjects(bucket)
		if err != nil {
			slog.Error("lifecycle: list objects", "bucket", bucket, "err", err)
			return
		}
		for _, obj := range objects {
			if !matchPrefix(obj.Key, prefix) || !obj.Metadata.LastModified.Before(cutoff) {
				continue
			}
			if err := e.storage.DeleteObject(bucket, obj.Key, false); err != nil {
				slog.Error("lifecycle: delete object", "bucket", bucket, "key", obj.Key, "err", err)
				continue
			}
			slog.Info("lifecycle: expired object", "bucket", bucket, "key", obj.Key)
		}
		return
	}

	// Versioned bucket: place a delete marker on the current version.
	versions, _, err := e.storage.ListObjectVersions(bucket)
	if err != nil {
		slog.Error("lifecycle: list versions", "bucket", bucket, "err", err)
		return
	}
	for _, v := range versions {
		if !v.IsLatest || !matchPrefix(v.Key, prefix) || !v.LastModified.Before(cutoff) {
			continue
		}
		if _, _, err := e.storage.DeleteObjectVersioned(bucket, v.Key, false); err != nil {
			slog.Error(
				"lifecycle: expire versioned object",
				"bucket",
				bucket,
				"key",
				v.Key,
				"err",
				err,
			)
			continue
		}
		slog.Info("lifecycle: expired object (versioned)", "bucket", bucket, "key", v.Key)
	}
}

func (e *LifecycleEnforcer) enforceNoncurrentExpiration(
	bucket, prefix string,
	noncurrentDays int,
	now time.Time,
) {
	cutoff := now.AddDate(0, 0, -noncurrentDays)

	versions, markers, err := e.storage.ListObjectVersions(bucket)
	if err != nil {
		slog.Error("lifecycle: list versions", "bucket", bucket, "err", err)
		return
	}

	for _, v := range versions {
		if v.IsLatest || !matchPrefix(v.Key, prefix) ||
			!noncurrentBefore(v.NoncurrentSince, v.LastModified, cutoff) {
			continue
		}
		if _, err := e.storage.DeleteObjectVersion(bucket, v.Key, v.VersionID, false); err != nil {
			slog.Error(
				"lifecycle: delete noncurrent version",
				"bucket",
				bucket,
				"key",
				v.Key,
				"versionId",
				v.VersionID,
				"err",
				err,
			)
			continue
		}
		slog.Info(
			"lifecycle: expired noncurrent version",
			"bucket",
			bucket,
			"key",
			v.Key,
			"versionId",
			v.VersionID,
		)
	}

	for _, m := range markers {
		if m.IsLatest || !matchPrefix(m.Key, prefix) ||
			!noncurrentBefore(m.NoncurrentSince, m.LastModified, cutoff) {
			continue
		}
		if _, err := e.storage.DeleteObjectVersion(bucket, m.Key, m.VersionID, false); err != nil {
			slog.Error(
				"lifecycle: delete noncurrent delete-marker",
				"bucket",
				bucket,
				"key",
				m.Key,
				"versionId",
				m.VersionID,
				"err",
				err,
			)
			continue
		}
		slog.Info(
			"lifecycle: expired noncurrent delete-marker",
			"bucket",
			bucket,
			"key",
			m.Key,
			"versionId",
			m.VersionID,
		)
	}
}

func (e *LifecycleEnforcer) enforceAbortIncomplete(
	bucket, prefix string,
	daysAfterInitiation int,
	now time.Time,
) {
	cutoff := now.AddDate(0, 0, -daysAfterInitiation)

	uploads, err := e.storage.ListMultipartUploads(bucket)
	if err != nil {
		slog.Error("lifecycle: list multipart uploads", "bucket", bucket, "err", err)
		return
	}

	for _, u := range uploads {
		if !matchPrefix(u.Key, prefix) || !u.Initiated.Before(cutoff) {
			continue
		}
		if err := e.storage.AbortMultipartUpload(u.UploadID); err != nil {
			slog.Error(
				"lifecycle: abort multipart upload",
				"bucket",
				bucket,
				"key",
				u.Key,
				"uploadId",
				u.UploadID,
				"err",
				err,
			)
			continue
		}
		slog.Info(
			"lifecycle: aborted incomplete multipart upload",
			"bucket",
			bucket,
			"key",
			u.Key,
			"uploadId",
			u.UploadID,
		)
	}
}

// enforceExpiredObjectDeleteMarker removes lone delete markers (IsLatest with no remaining
// non-current non-marker versions for the same key) when ExpiredObjectDeleteMarker is true.
func (e *LifecycleEnforcer) enforceExpiredObjectDeleteMarker(bucket, prefix string) {
	versions, markers, err := e.storage.ListObjectVersions(bucket)
	if err != nil {
		slog.Error(
			"lifecycle: list versions for delete-marker cleanup",
			"bucket",
			bucket,
			"err",
			err,
		)
		return
	}

	// Build a set of keys that still have at least one non-marker version.
	keysWithVersions := make(map[string]struct{})
	for _, v := range versions {
		keysWithVersions[v.Key] = struct{}{}
	}

	for _, m := range markers {
		if !m.IsLatest || !matchPrefix(m.Key, prefix) {
			continue
		}
		if _, hasVersion := keysWithVersions[m.Key]; hasVersion {
			continue
		}
		if _, err := e.storage.DeleteObjectVersion(bucket, m.Key, m.VersionID, false); err != nil {
			slog.Error(
				"lifecycle: delete expired object delete-marker",
				"bucket", bucket,
				"key", m.Key,
				"versionId", m.VersionID,
				"err", err,
			)
			continue
		}
		slog.Info(
			"lifecycle: removed expired object delete-marker",
			"bucket", bucket,
			"key", m.Key,
			"versionId", m.VersionID,
		)
	}
}

// effectivePrefix returns the prefix from Filter (V2) or the top-level Prefix (V1).
func (r lifecycleRule) effectivePrefix() string {
	if r.Filter != nil {
		return r.Filter.Prefix
	}
	return r.Prefix
}

// noncurrentBefore uses NoncurrentSince; falls back to LastModified for pre-existing versions.
func noncurrentBefore(noncurrentSince, lastModified, cutoff time.Time) bool {
	if !noncurrentSince.IsZero() {
		return noncurrentSince.Before(cutoff)
	}
	return lastModified.Before(cutoff)
}

func matchPrefix(key, prefix string) bool {
	if prefix == "" {
		return true
	}
	return strings.HasPrefix(key, prefix)
}
