package s3

import (
	"context"
	"encoding/xml"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var errFake = errors.New("fake error")

// errLifecycleStore wraps fakeLCStore and overrides GetBucketLifecycle to return an error.
type errLifecycleStore struct {
	*fakeLCStore
	errGetLifecycle error
}

func (s *errLifecycleStore) GetBucketLifecycle(_ string) (string, error) {
	return "", s.errGetLifecycle
}

// fakeLCStore is a minimal in-memory implementation of lifecycleStore for tests.
// Set the errXxx fields to inject errors into specific methods.
// mu protects the recorded slices so tests using Start() don't race.
type fakeLCStore struct {
	mu         sync.Mutex
	buckets    []BucketInfo
	lifecycle  map[string]string // bucket -> XML
	versioning map[string]string // bucket -> "Enabled"|""

	objects  map[string][]ObjectInfo          // bucket -> objects
	versions map[string][]VersionInfo         // bucket -> versions
	markers  map[string][]DeleteMarkerInfo    // bucket -> delete markers
	uploads  map[string][]MultipartUploadInfo // bucket -> uploads

	deletedObjects  []string // "bucket/key"
	deletedVersions []string // "bucket/key/versionId"
	abortedUploads  []string // uploadId

	// error injection
	errListBuckets           error
	errGetVersioning         error
	errListObjects           error
	errDeleteObject          error
	errDeleteObjectVersioned error
	errListObjectVersions    error
	errDeleteObjectVersion   error
	errListMultipartUploads  error
	errAbortMultipartUpload  error
}

func newFakeLCStore() *fakeLCStore {
	return &fakeLCStore{
		lifecycle:  make(map[string]string),
		versioning: make(map[string]string),
		objects:    make(map[string][]ObjectInfo),
		versions:   make(map[string][]VersionInfo),
		markers:    make(map[string][]DeleteMarkerInfo),
		uploads:    make(map[string][]MultipartUploadInfo),
	}
}

func (f *fakeLCStore) ListBuckets() ([]BucketInfo, error) {
	if f.errListBuckets != nil {
		return nil, f.errListBuckets
	}
	return f.buckets, nil
}

func (f *fakeLCStore) GetBucketLifecycle(bucket string) (string, error) {
	return f.lifecycle[bucket], nil
}

func (f *fakeLCStore) GetBucketVersioning(bucket string) (string, error) {
	if f.errGetVersioning != nil {
		return "", f.errGetVersioning
	}
	return f.versioning[bucket], nil
}

func (f *fakeLCStore) ListObjects(bucket string) ([]ObjectInfo, error) {
	if f.errListObjects != nil {
		return nil, f.errListObjects
	}
	return f.objects[bucket], nil
}

func (f *fakeLCStore) DeleteObject(bucket, key string, _ bool) error {
	if f.errDeleteObject != nil {
		return f.errDeleteObject
	}
	f.mu.Lock()
	f.deletedObjects = append(f.deletedObjects, bucket+"/"+key)
	f.mu.Unlock()
	return nil
}

func (f *fakeLCStore) DeleteObjectVersioned(bucket, key string, _ bool) (string, bool, error) {
	if f.errDeleteObjectVersioned != nil {
		return "", false, f.errDeleteObjectVersioned
	}
	f.mu.Lock()
	f.deletedObjects = append(f.deletedObjects, bucket+"/"+key)
	f.mu.Unlock()
	return "dm-1", true, nil
}

func (f *fakeLCStore) ListObjectVersions(bucket string) ([]VersionInfo, []DeleteMarkerInfo, error) {
	if f.errListObjectVersions != nil {
		return nil, nil, f.errListObjectVersions
	}
	return f.versions[bucket], f.markers[bucket], nil
}

func (f *fakeLCStore) DeleteObjectVersion(bucket, key, versionID string, _ bool) (bool, error) {
	if f.errDeleteObjectVersion != nil {
		return false, f.errDeleteObjectVersion
	}
	f.mu.Lock()
	f.deletedVersions = append(f.deletedVersions, bucket+"/"+key+"/"+versionID)
	f.mu.Unlock()
	return false, nil
}

func (f *fakeLCStore) ListMultipartUploads(bucket string) ([]MultipartUploadInfo, error) {
	if f.errListMultipartUploads != nil {
		return nil, f.errListMultipartUploads
	}
	return f.uploads[bucket], nil
}

func (f *fakeLCStore) AbortMultipartUpload(uploadID string) error {
	if f.errAbortMultipartUpload != nil {
		return f.errAbortMultipartUpload
	}
	f.mu.Lock()
	f.abortedUploads = append(f.abortedUploads, uploadID)
	f.mu.Unlock()
	return nil
}

// buildLifecycleXML is a helper that serialises a lifecycleConfiguration to XML.
func buildLifecycleXML(t *testing.T, cfg lifecycleConfiguration) string {
	t.Helper()
	b, err := xml.Marshal(cfg)
	require.NoError(t, err)
	return string(b)
}

// --- enforceExpiration (non-versioned) ---

func TestEnforceExpiration_NonVersioned(t *testing.T) {
	now := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name           string
		days           int
		prefix         string
		objects        []ObjectInfo
		wantDeleted    []string
		wantNotDeleted []string
	}{
		{
			name:   "deletes object older than expiry",
			days:   30,
			prefix: "",
			objects: []ObjectInfo{
				{Key: "old.txt", Metadata: ObjectMetadata{LastModified: now.AddDate(0, 0, -31)}},
			},
			wantDeleted: []string{"b/old.txt"},
		},
		{
			name:   "keeps object newer than expiry",
			days:   30,
			prefix: "",
			objects: []ObjectInfo{
				{Key: "new.txt", Metadata: ObjectMetadata{LastModified: now.AddDate(0, 0, -10)}},
			},
			wantNotDeleted: []string{"b/new.txt"},
		},
		{
			name:   "exactly on cutoff boundary is kept",
			days:   30,
			prefix: "",
			objects: []ObjectInfo{
				{Key: "edge.txt", Metadata: ObjectMetadata{LastModified: now.AddDate(0, 0, -30)}},
			},
			wantNotDeleted: []string{"b/edge.txt"},
		},
		{
			name:   "prefix filter restricts deletion",
			days:   30,
			prefix: "logs/",
			objects: []ObjectInfo{
				{
					Key:      "logs/old.txt",
					Metadata: ObjectMetadata{LastModified: now.AddDate(0, 0, -31)},
				},
				{
					Key:      "data/old.txt",
					Metadata: ObjectMetadata{LastModified: now.AddDate(0, 0, -31)},
				},
			},
			wantDeleted:    []string{"b/logs/old.txt"},
			wantNotDeleted: []string{"b/data/old.txt"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := newFakeLCStore()
			store.objects["b"] = tc.objects

			e := NewLifecycleEnforcer(store, time.Minute)
			e.now = func() time.Time { return now }
			e.enforceExpiration("b", tc.prefix, tc.days, false, now)

			for _, key := range tc.wantDeleted {
				assert.Contains(t, store.deletedObjects, key)
			}
			for _, key := range tc.wantNotDeleted {
				assert.NotContains(t, store.deletedObjects, key)
			}
		})
	}
}

// --- enforceExpiration (versioned) ---

func TestEnforceExpiration_Versioned(t *testing.T) {
	now := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)

	t.Run("creates delete marker for expired current version", func(t *testing.T) {
		store := newFakeLCStore()
		store.versions["b"] = []VersionInfo{
			{Key: "obj.txt", VersionID: "v1", IsLatest: true, LastModified: now.AddDate(0, 0, -31)},
		}

		e := NewLifecycleEnforcer(store, time.Minute)
		e.now = func() time.Time { return now }
		e.enforceExpiration("b", "", 30, true, now)

		assert.Contains(t, store.deletedObjects, "b/obj.txt")
	})

	t.Run("skips non-latest versions", func(t *testing.T) {
		store := newFakeLCStore()
		store.versions["b"] = []VersionInfo{
			{
				Key:          "obj.txt",
				VersionID:    "v1",
				IsLatest:     false,
				LastModified: now.AddDate(0, 0, -31),
			},
		}

		e := NewLifecycleEnforcer(store, time.Minute)
		e.now = func() time.Time { return now }
		e.enforceExpiration("b", "", 30, true, now)

		assert.Empty(t, store.deletedObjects)
	})
}

// --- enforceNoncurrentExpiration ---

func TestEnforceNoncurrentExpiration(t *testing.T) {
	now := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)

	t.Run("deletes old noncurrent versions", func(t *testing.T) {
		store := newFakeLCStore()
		store.versions["b"] = []VersionInfo{
			{Key: "obj.txt", VersionID: "v1", IsLatest: false, LastModified: now.AddDate(0, 0, -8)},
			{Key: "obj.txt", VersionID: "v2", IsLatest: true, LastModified: now.AddDate(0, 0, -1)},
		}

		e := NewLifecycleEnforcer(store, time.Minute)
		e.now = func() time.Time { return now }
		e.enforceNoncurrentExpiration("b", "", 7, now)

		assert.Contains(t, store.deletedVersions, "b/obj.txt/v1")
		assert.NotContains(t, store.deletedVersions, "b/obj.txt/v2")
	})

	t.Run("deletes old noncurrent delete markers", func(t *testing.T) {
		store := newFakeLCStore()
		store.markers["b"] = []DeleteMarkerInfo{
			{
				Key:          "obj.txt",
				VersionID:    "dm1",
				IsLatest:     false,
				LastModified: now.AddDate(0, 0, -8),
			},
		}

		e := NewLifecycleEnforcer(store, time.Minute)
		e.now = func() time.Time { return now }
		e.enforceNoncurrentExpiration("b", "", 7, now)

		assert.Contains(t, store.deletedVersions, "b/obj.txt/dm1")
	})

	t.Run("respects prefix filter", func(t *testing.T) {
		store := newFakeLCStore()
		store.versions["b"] = []VersionInfo{
			{
				Key:          "logs/old.txt",
				VersionID:    "v1",
				IsLatest:     false,
				LastModified: now.AddDate(0, 0, -8),
			},
			{
				Key:          "data/old.txt",
				VersionID:    "v2",
				IsLatest:     false,
				LastModified: now.AddDate(0, 0, -8),
			},
		}

		e := NewLifecycleEnforcer(store, time.Minute)
		e.now = func() time.Time { return now }
		e.enforceNoncurrentExpiration("b", "logs/", 7, now)

		assert.Contains(t, store.deletedVersions, "b/logs/old.txt/v1")
		assert.NotContains(t, store.deletedVersions, "b/data/old.txt/v2")
	})

	t.Run("keeps noncurrent versions within retention period", func(t *testing.T) {
		store := newFakeLCStore()
		store.versions["b"] = []VersionInfo{
			{Key: "obj.txt", VersionID: "v1", IsLatest: false, LastModified: now.AddDate(0, 0, -3)},
		}

		e := NewLifecycleEnforcer(store, time.Minute)
		e.now = func() time.Time { return now }
		e.enforceNoncurrentExpiration("b", "", 7, now)

		assert.Empty(t, store.deletedVersions)
	})
}

// --- enforceAbortIncomplete ---

func TestEnforceAbortIncomplete(t *testing.T) {
	now := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)

	t.Run("aborts old incomplete upload", func(t *testing.T) {
		store := newFakeLCStore()
		store.uploads["b"] = []MultipartUploadInfo{
			{UploadID: "up-old", Key: "big.tar", Initiated: now.AddDate(0, 0, -8)},
		}

		e := NewLifecycleEnforcer(store, time.Minute)
		e.now = func() time.Time { return now }
		e.enforceAbortIncomplete("b", "", 7, now)

		assert.Contains(t, store.abortedUploads, "up-old")
	})

	t.Run("keeps recent upload", func(t *testing.T) {
		store := newFakeLCStore()
		store.uploads["b"] = []MultipartUploadInfo{
			{UploadID: "up-new", Key: "big.tar", Initiated: now.AddDate(0, 0, -3)},
		}

		e := NewLifecycleEnforcer(store, time.Minute)
		e.now = func() time.Time { return now }
		e.enforceAbortIncomplete("b", "", 7, now)

		assert.Empty(t, store.abortedUploads)
	})

	t.Run("respects prefix filter", func(t *testing.T) {
		store := newFakeLCStore()
		store.uploads["b"] = []MultipartUploadInfo{
			{UploadID: "up1", Key: "uploads/big.tar", Initiated: now.AddDate(0, 0, -8)},
			{UploadID: "up2", Key: "other/big.tar", Initiated: now.AddDate(0, 0, -8)},
		}

		e := NewLifecycleEnforcer(store, time.Minute)
		e.now = func() time.Time { return now }
		e.enforceAbortIncomplete("b", "uploads/", 7, now)

		assert.Contains(t, store.abortedUploads, "up1")
		assert.NotContains(t, store.abortedUploads, "up2")
	})
}

// --- runOnce / enforceBucket ---

func TestEnforceBucket_DisabledRule(t *testing.T) {
	now := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)

	store := newFakeLCStore()
	store.buckets = []BucketInfo{{Name: "b"}}
	store.objects["b"] = []ObjectInfo{
		{Key: "old.txt", Metadata: ObjectMetadata{LastModified: now.AddDate(0, 0, -31)}},
	}
	store.lifecycle["b"] = buildLifecycleXML(t, lifecycleConfiguration{
		Rules: []lifecycleRule{
			{Status: "Disabled", Expiration: &lifecycleExpiration{Days: 30}},
		},
	})

	e := NewLifecycleEnforcer(store, time.Minute)
	e.now = func() time.Time { return now }
	e.runOnce()

	assert.Empty(t, store.deletedObjects)
}

func TestEnforceBucket_NoLifecycle(t *testing.T) {
	now := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)

	store := newFakeLCStore()
	store.buckets = []BucketInfo{{Name: "b"}}
	store.objects["b"] = []ObjectInfo{
		{Key: "old.txt", Metadata: ObjectMetadata{LastModified: now.AddDate(0, 0, -100)}},
	}

	e := NewLifecycleEnforcer(store, time.Minute)
	e.now = func() time.Time { return now }
	e.runOnce()

	assert.Empty(t, store.deletedObjects)
}

func TestEnforceBucket_NoncurrentVersionExpirationRule(t *testing.T) {
	now := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)

	store := newFakeLCStore()
	store.buckets = []BucketInfo{{Name: "b"}}
	store.versions["b"] = []VersionInfo{
		{Key: "obj.txt", VersionID: "v1", IsLatest: false, LastModified: now.AddDate(0, 0, -8)},
		{Key: "obj.txt", VersionID: "v2", IsLatest: true, LastModified: now.AddDate(0, 0, -1)},
	}
	store.lifecycle["b"] = buildLifecycleXML(t, lifecycleConfiguration{
		Rules: []lifecycleRule{
			{
				Status: "Enabled",
				NoncurrentVersionExpiration: &lifecycleNoncurrentVersionExpiration{
					NoncurrentDays: 7,
				},
			},
		},
	})

	e := NewLifecycleEnforcer(store, time.Minute)
	e.now = func() time.Time { return now }
	e.runOnce()

	assert.Contains(t, store.deletedVersions, "b/obj.txt/v1")
	assert.NotContains(t, store.deletedVersions, "b/obj.txt/v2")
}

func TestEnforceBucket_AbortIncompleteRule(t *testing.T) {
	now := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)

	store := newFakeLCStore()
	store.buckets = []BucketInfo{{Name: "b"}}
	store.uploads["b"] = []MultipartUploadInfo{
		{UploadID: "up-old", Key: "big.tar", Initiated: now.AddDate(0, 0, -8)},
	}
	store.lifecycle["b"] = buildLifecycleXML(t, lifecycleConfiguration{
		Rules: []lifecycleRule{
			{
				Status: "Enabled",
				AbortIncompleteMultipartUpload: &lifecycleAbortIncompleteMultipartUpload{
					DaysAfterInitiation: 7,
				},
			},
		},
	})

	e := NewLifecycleEnforcer(store, time.Minute)
	e.now = func() time.Time { return now }
	e.runOnce()

	assert.Contains(t, store.abortedUploads, "up-old")
}

func TestEnforceBucket_V2FilterPrefix(t *testing.T) {
	now := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)

	store := newFakeLCStore()
	store.buckets = []BucketInfo{{Name: "b"}}
	store.objects["b"] = []ObjectInfo{
		{Key: "logs/old.txt", Metadata: ObjectMetadata{LastModified: now.AddDate(0, 0, -31)}},
		{Key: "data/old.txt", Metadata: ObjectMetadata{LastModified: now.AddDate(0, 0, -31)}},
	}
	// V2-style: prefix inside Filter element
	store.lifecycle["b"] = buildLifecycleXML(t, lifecycleConfiguration{
		Rules: []lifecycleRule{
			{
				Status:     "Enabled",
				Filter:     &lifecycleFilter{Prefix: "logs/"},
				Expiration: &lifecycleExpiration{Days: 30},
			},
		},
	})

	e := NewLifecycleEnforcer(store, time.Minute)
	e.now = func() time.Time { return now }
	e.runOnce()

	assert.Contains(t, store.deletedObjects, "b/logs/old.txt")
	assert.NotContains(t, store.deletedObjects, "b/data/old.txt")
}

func TestEnforceBucket_V1PrefixStyle(t *testing.T) {
	now := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)

	store := newFakeLCStore()
	store.buckets = []BucketInfo{{Name: "b"}}
	store.objects["b"] = []ObjectInfo{
		{Key: "logs/old.txt", Metadata: ObjectMetadata{LastModified: now.AddDate(0, 0, -31)}},
		{Key: "data/old.txt", Metadata: ObjectMetadata{LastModified: now.AddDate(0, 0, -31)}},
	}
	// V1-style: prefix directly in Rule, no Filter element
	store.lifecycle["b"] = buildLifecycleXML(t, lifecycleConfiguration{
		Rules: []lifecycleRule{
			{
				Status:     "Enabled",
				Prefix:     "logs/",
				Expiration: &lifecycleExpiration{Days: 30},
			},
		},
	})

	e := NewLifecycleEnforcer(store, time.Minute)
	e.now = func() time.Time { return now }
	e.runOnce()

	assert.Contains(t, store.deletedObjects, "b/logs/old.txt")
	assert.NotContains(t, store.deletedObjects, "b/data/old.txt")
}

func TestStart_StopsOnContextCancel(t *testing.T) {
	store := newFakeLCStore()
	e := NewLifecycleEnforcer(store, time.Hour)
	e.now = time.Now

	ctx, cancel := context.WithCancel(context.Background())
	e.Start(ctx)
	cancel()
	// Give the goroutine a moment to exit — no assertion needed; we just verify it doesn't hang.
	time.Sleep(50 * time.Millisecond)
}

func TestStart_TickerFiresRunOnce(t *testing.T) {
	now := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)

	store := newFakeLCStore()
	store.buckets = []BucketInfo{{Name: "b"}}
	store.objects["b"] = []ObjectInfo{
		{Key: "old.txt", Metadata: ObjectMetadata{LastModified: now.AddDate(0, 0, -31)}},
	}
	store.lifecycle["b"] = buildLifecycleXML(t, lifecycleConfiguration{
		Rules: []lifecycleRule{
			{Status: "Enabled", Expiration: &lifecycleExpiration{Days: 30}},
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	e := NewLifecycleEnforcer(store, 20*time.Millisecond)
	e.now = func() time.Time { return now }
	e.Start(ctx)

	assert.Eventually(t, func() bool {
		store.mu.Lock()
		defer store.mu.Unlock()
		for _, d := range store.deletedObjects {
			if d == "b/old.txt" {
				return true
			}
		}
		return false
	}, 500*time.Millisecond, 10*time.Millisecond)
}

// --- error path coverage ---

func TestRunOnce_ListBucketsError(t *testing.T) {
	store := newFakeLCStore()
	store.errListBuckets = errFake

	e := NewLifecycleEnforcer(store, time.Minute)
	e.now = time.Now
	e.runOnce() // must not panic
}

func TestEnforceBucket_InvalidXML(t *testing.T) {
	store := newFakeLCStore()
	store.lifecycle["b"] = "<invalid xml"

	e := NewLifecycleEnforcer(store, time.Minute)
	e.now = time.Now
	e.enforceBucket("b") // must not panic
}

func TestEnforceExpiration_ListObjectsError(t *testing.T) {
	now := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	store := newFakeLCStore()
	store.errListObjects = errFake

	e := NewLifecycleEnforcer(store, time.Minute)
	e.now = func() time.Time { return now }
	e.enforceExpiration("b", "", 30, false, now) // must not panic
}

func TestEnforceExpiration_DeleteObjectError(t *testing.T) {
	now := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	store := newFakeLCStore()
	store.objects["b"] = []ObjectInfo{
		{Key: "old.txt", Metadata: ObjectMetadata{LastModified: now.AddDate(0, 0, -31)}},
	}
	store.errDeleteObject = errFake

	e := NewLifecycleEnforcer(store, time.Minute)
	e.now = func() time.Time { return now }
	e.enforceExpiration("b", "", 30, false, now) // must not panic
	assert.Empty(t, store.deletedObjects)
}

func TestEnforceExpiration_ListObjectVersionsError(t *testing.T) {
	now := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	store := newFakeLCStore()
	store.errListObjectVersions = errFake

	e := NewLifecycleEnforcer(store, time.Minute)
	e.now = func() time.Time { return now }
	e.enforceExpiration("b", "", 30, true, now) // must not panic
}

func TestEnforceExpiration_DeleteObjectVersionedError(t *testing.T) {
	now := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	store := newFakeLCStore()
	store.versions["b"] = []VersionInfo{
		{Key: "obj.txt", VersionID: "v1", IsLatest: true, LastModified: now.AddDate(0, 0, -31)},
	}
	store.errDeleteObjectVersioned = errFake

	e := NewLifecycleEnforcer(store, time.Minute)
	e.now = func() time.Time { return now }
	e.enforceExpiration("b", "", 30, true, now) // must not panic
	assert.Empty(t, store.deletedObjects)
}

func TestEnforceNoncurrentExpiration_ListVersionsError(t *testing.T) {
	now := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	store := newFakeLCStore()
	store.errListObjectVersions = errFake

	e := NewLifecycleEnforcer(store, time.Minute)
	e.now = func() time.Time { return now }
	e.enforceNoncurrentExpiration("b", "", 7, now) // must not panic
}

func TestEnforceNoncurrentExpiration_DeleteVersionError(t *testing.T) {
	now := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	store := newFakeLCStore()
	store.versions["b"] = []VersionInfo{
		{Key: "obj.txt", VersionID: "v1", IsLatest: false, LastModified: now.AddDate(0, 0, -8)},
	}
	store.markers["b"] = []DeleteMarkerInfo{
		{Key: "obj.txt", VersionID: "dm1", IsLatest: false, LastModified: now.AddDate(0, 0, -8)},
	}
	store.errDeleteObjectVersion = errFake

	e := NewLifecycleEnforcer(store, time.Minute)
	e.now = func() time.Time { return now }
	e.enforceNoncurrentExpiration("b", "", 7, now) // must not panic
	assert.Empty(t, store.deletedVersions)
}

func TestEnforceAbortIncomplete_ListUploadsError(t *testing.T) {
	now := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	store := newFakeLCStore()
	store.errListMultipartUploads = errFake

	e := NewLifecycleEnforcer(store, time.Minute)
	e.now = func() time.Time { return now }
	e.enforceAbortIncomplete("b", "", 7, now) // must not panic
}

func TestEnforceBucket_GetVersioningError(t *testing.T) {
	// GetBucketVersioning error should log a warning and continue as unversioned.
	now := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)

	store := newFakeLCStore()
	store.buckets = []BucketInfo{{Name: "b"}}
	store.objects["b"] = []ObjectInfo{
		{Key: "old.txt", Metadata: ObjectMetadata{LastModified: now.AddDate(0, 0, -31)}},
	}
	store.lifecycle["b"] = buildLifecycleXML(t, lifecycleConfiguration{
		Rules: []lifecycleRule{
			{Status: "Enabled", Expiration: &lifecycleExpiration{Days: 30}},
		},
	})
	store.errGetVersioning = errFake

	e := NewLifecycleEnforcer(store, time.Minute)
	e.now = func() time.Time { return now }
	e.runOnce()

	// Continues as non-versioned; object should still be deleted.
	assert.Contains(t, store.deletedObjects, "b/old.txt")
}

func TestEnforceBucket_GetLifecycleError(t *testing.T) {
	// Simulates GetBucketLifecycle returning an error — enforceBucket must return silently.
	store := newFakeLCStore()
	// lifecycle map has no entry for "b", so GetBucketLifecycle returns ("", nil).
	// To trigger the err != nil branch we use a wrapper that returns an error.
	wrapped := &errLifecycleStore{fakeLCStore: store, errGetLifecycle: errFake}

	e := NewLifecycleEnforcer(wrapped, time.Minute)
	e.now = time.Now
	e.enforceBucket("b") // must not panic
}

func TestEnforceNoncurrentExpiration_RecentDeleteMarkerSkipped(t *testing.T) {
	now := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	store := newFakeLCStore()
	store.markers["b"] = []DeleteMarkerInfo{
		{Key: "obj.txt", VersionID: "dm1", IsLatest: false, LastModified: now.AddDate(0, 0, -3)},
	}

	e := NewLifecycleEnforcer(store, time.Minute)
	e.now = func() time.Time { return now }
	e.enforceNoncurrentExpiration("b", "", 7, now)

	assert.Empty(t, store.deletedVersions)
}

func TestEnforceAbortIncomplete_AbortError(t *testing.T) {
	now := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	store := newFakeLCStore()
	store.uploads["b"] = []MultipartUploadInfo{
		{UploadID: "up-old", Key: "big.tar", Initiated: now.AddDate(0, 0, -8)},
	}
	store.errAbortMultipartUpload = errFake

	e := NewLifecycleEnforcer(store, time.Minute)
	e.now = func() time.Time { return now }
	e.enforceAbortIncomplete("b", "", 7, now) // must not panic
	assert.Empty(t, store.abortedUploads)
}
