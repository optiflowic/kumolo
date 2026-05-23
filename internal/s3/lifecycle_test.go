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
			e.enforceExpiration("b", tc.prefix, now.AddDate(0, 0, -tc.days), false)

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
		e.enforceExpiration("b", "", now.AddDate(0, 0, -30), true)

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
		e.enforceExpiration("b", "", now.AddDate(0, 0, -30), true)

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

	t.Run("uses NoncurrentSince not LastModified when available", func(t *testing.T) {
		// v1 was created 100 days ago but became noncurrent only 3 days ago.
		// With NoncurrentDays=7 it should NOT be expired yet.
		store := newFakeLCStore()
		store.versions["b"] = []VersionInfo{
			{
				Key:             "obj.txt",
				VersionID:       "v1",
				IsLatest:        false,
				LastModified:    now.AddDate(0, 0, -100),
				NoncurrentSince: now.AddDate(0, 0, -3),
			},
		}

		e := NewLifecycleEnforcer(store, time.Minute)
		e.now = func() time.Time { return now }
		e.enforceNoncurrentExpiration("b", "", 7, now)

		assert.Empty(t, store.deletedVersions)
	})

	t.Run("expires version whose NoncurrentSince exceeds retention", func(t *testing.T) {
		// v1 was created 2 days ago but became noncurrent 8 days ago (NoncurrentDays=7).
		store := newFakeLCStore()
		store.versions["b"] = []VersionInfo{
			{
				Key:             "obj.txt",
				VersionID:       "v1",
				IsLatest:        false,
				LastModified:    now.AddDate(0, 0, -2),
				NoncurrentSince: now.AddDate(0, 0, -8),
			},
		}

		e := NewLifecycleEnforcer(store, time.Minute)
		e.now = func() time.Time { return now }
		e.enforceNoncurrentExpiration("b", "", 7, now)

		assert.Contains(t, store.deletedVersions, "b/obj.txt/v1")
	})

	t.Run("falls back to LastModified when NoncurrentSince is zero", func(t *testing.T) {
		// Pre-existing version without NoncurrentSince set; LastModified is 8 days ago.
		store := newFakeLCStore()
		store.versions["b"] = []VersionInfo{
			{Key: "obj.txt", VersionID: "v1", IsLatest: false, LastModified: now.AddDate(0, 0, -8)},
		}

		e := NewLifecycleEnforcer(store, time.Minute)
		e.now = func() time.Time { return now }
		e.enforceNoncurrentExpiration("b", "", 7, now)

		assert.Contains(t, store.deletedVersions, "b/obj.txt/v1")
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
	e.enforceExpiration("b", "", now.AddDate(0, 0, -30), false) // must not panic
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
	e.enforceExpiration("b", "", now.AddDate(0, 0, -30), false) // must not panic
	assert.Empty(t, store.deletedObjects)
}

func TestEnforceExpiration_ListObjectVersionsError(t *testing.T) {
	now := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	store := newFakeLCStore()
	store.errListObjectVersions = errFake

	e := NewLifecycleEnforcer(store, time.Minute)
	e.now = func() time.Time { return now }
	e.enforceExpiration("b", "", now.AddDate(0, 0, -30), true) // must not panic
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
	e.enforceExpiration("b", "", now.AddDate(0, 0, -30), true) // must not panic
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

// --- Expiration.Date ---

func TestEnforceBucket_ExpirationDate(t *testing.T) {
	now := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	expDate := time.Date(2026, 4, 15, 0, 0, 0, 0, time.UTC) // past date

	tests := []struct {
		name           string
		nowOverride    time.Time
		wantDeleted    []string
		wantNotDeleted []string
	}{
		{
			name:        "expires objects when now is after expiration date",
			nowOverride: now,
			wantDeleted: []string{"b/old.txt"},
		},
		{
			name:           "does not expire when now is before expiration date",
			nowOverride:    time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
			wantNotDeleted: []string{"b/old.txt"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := newFakeLCStore()
			store.buckets = []BucketInfo{{Name: "b"}}
			store.objects["b"] = []ObjectInfo{
				{
					Key: "old.txt",
					Metadata: ObjectMetadata{
						LastModified: time.Date(2026, 4, 10, 0, 0, 0, 0, time.UTC),
					},
				},
			}
			store.lifecycle["b"] = buildLifecycleXML(t, lifecycleConfiguration{
				Rules: []lifecycleRule{
					{Status: "Enabled", Expiration: &lifecycleExpiration{Date: expDate}},
				},
			})

			e := NewLifecycleEnforcer(store, time.Minute)
			e.now = func() time.Time { return tc.nowOverride }
			e.runOnce()

			for _, key := range tc.wantDeleted {
				assert.Contains(t, store.deletedObjects, key)
			}
			for _, key := range tc.wantNotDeleted {
				assert.NotContains(t, store.deletedObjects, key)
			}
		})
	}
}

func TestEnforceBucket_ExpirationDate_ObjectCreatedAfterDate(t *testing.T) {
	// AWS: date-based expiration is not relative to object creation time.
	// All matching objects are expired when now >= Date, including objects
	// created after the Date itself.
	now := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	expDate := time.Date(2026, 4, 15, 0, 0, 0, 0, time.UTC)

	store := newFakeLCStore()
	store.buckets = []BucketInfo{{Name: "b"}}
	store.objects["b"] = []ObjectInfo{
		{
			Key:      "new.txt",
			Metadata: ObjectMetadata{LastModified: time.Date(2026, 4, 20, 0, 0, 0, 0, time.UTC)},
		},
	}
	store.lifecycle["b"] = buildLifecycleXML(t, lifecycleConfiguration{
		Rules: []lifecycleRule{
			{Status: "Enabled", Expiration: &lifecycleExpiration{Date: expDate}},
		},
	})

	e := NewLifecycleEnforcer(store, time.Minute)
	e.now = func() time.Time { return now }
	e.runOnce()

	assert.Contains(t, store.deletedObjects, "b/new.txt")
}

// --- Expiration.ExpiredObjectDeleteMarker ---

func TestEnforceExpiredObjectDeleteMarker(t *testing.T) {
	tests := []struct {
		name            string
		versions        []VersionInfo
		markers         []DeleteMarkerInfo
		wantDeletedVers []string
		wantKeptVers    []string
	}{
		{
			name: "removes lone latest delete marker",
			markers: []DeleteMarkerInfo{
				{Key: "obj.txt", VersionID: "dm1", IsLatest: true},
			},
			wantDeletedVers: []string{"b/obj.txt/dm1"},
		},
		{
			name: "keeps delete marker when non-current version still exists",
			versions: []VersionInfo{
				{Key: "obj.txt", VersionID: "v1", IsLatest: false},
			},
			markers: []DeleteMarkerInfo{
				{Key: "obj.txt", VersionID: "dm1", IsLatest: true},
			},
			wantKeptVers: []string{"b/obj.txt/dm1"},
		},
		{
			name: "skips non-latest delete markers",
			markers: []DeleteMarkerInfo{
				{Key: "obj.txt", VersionID: "dm1", IsLatest: false},
			},
			wantKeptVers: []string{"b/obj.txt/dm1"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := newFakeLCStore()
			store.versions["b"] = tc.versions
			store.markers["b"] = tc.markers

			e := NewLifecycleEnforcer(store, time.Minute)
			e.now = time.Now
			e.enforceExpiredObjectDeleteMarker("b", "")

			for _, v := range tc.wantDeletedVers {
				assert.Contains(t, store.deletedVersions, v)
			}
			for _, v := range tc.wantKeptVers {
				assert.NotContains(t, store.deletedVersions, v)
			}
		})
	}
}

func TestEnforceBucket_ExpiredObjectDeleteMarkerRule(t *testing.T) {
	store := newFakeLCStore()
	store.buckets = []BucketInfo{{Name: "b"}}
	store.markers["b"] = []DeleteMarkerInfo{
		{Key: "gone.txt", VersionID: "dm1", IsLatest: true},
	}
	store.lifecycle["b"] = buildLifecycleXML(t, lifecycleConfiguration{
		Rules: []lifecycleRule{
			{
				Status:     "Enabled",
				Expiration: &lifecycleExpiration{ExpiredObjectDeleteMarker: true},
			},
		},
	})

	e := NewLifecycleEnforcer(store, time.Minute)
	e.now = time.Now
	e.runOnce()

	assert.Contains(t, store.deletedVersions, "b/gone.txt/dm1")
}

func TestEnforceExpiredObjectDeleteMarker_ListVersionsError(t *testing.T) {
	store := newFakeLCStore()
	store.errListObjectVersions = errFake

	e := NewLifecycleEnforcer(store, time.Minute)
	e.now = time.Now
	e.enforceExpiredObjectDeleteMarker("b", "") // must not panic
}

func TestEnforceExpiredObjectDeleteMarker_DeleteVersionError(t *testing.T) {
	store := newFakeLCStore()
	store.markers["b"] = []DeleteMarkerInfo{
		{Key: "obj.txt", VersionID: "dm1", IsLatest: true},
	}
	store.errDeleteObjectVersion = errFake

	e := NewLifecycleEnforcer(store, time.Minute)
	e.now = time.Now
	e.enforceExpiredObjectDeleteMarker("b", "") // must not panic
	assert.Empty(t, store.deletedVersions)
}
