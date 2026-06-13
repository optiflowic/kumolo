package s3

import (
	"cmp"
	"crypto/md5" // #nosec G501 -- MD5 is required by the S3 ETag specification
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"hash"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

// bucketMeta is stored as a <bucket>.bucket.json file at the storage root.
type bucketMeta struct {
	Region            string     `json:"region"`
	Tags              []Tag      `json:"tags,omitempty"`
	VersioningStatus  string     `json:"versioningStatus,omitempty"`
	CORSRules         []CORSRule `json:"corsRules,omitempty"`
	Policy            string     `json:"policy,omitempty"`
	PublicAccessBlock string     `json:"publicAccessBlock,omitempty"`
	Encryption        string     `json:"encryption,omitempty"`
	OwnershipControls string     `json:"ownershipControls,omitempty"`
	Notification      string     `json:"notification,omitempty"`
	Lifecycle         string     `json:"lifecycle,omitempty"`
	Website           string     `json:"website,omitempty"`
	Logging           string     `json:"logging,omitempty"`
	Accelerate        string     `json:"accelerate,omitempty"`
	Replication       string     `json:"replication,omitempty"`
	RequestPayment    string     `json:"requestPayment,omitempty"`
	ObjectLock        string     `json:"objectLock,omitempty"`
	ACL               string     `json:"acl,omitempty"`
}

// Storage is a filesystem-backed S3 backend. os.Root scopes all access to the
// storage root, preventing path traversal attacks.
type Storage struct {
	mu         sync.RWMutex
	root       *os.Root
	removeFile func(name string) error
	openFile   func(name string, flag int, perm os.FileMode) (io.WriteCloser, error)
	readAll    func(r io.Reader) ([]byte, error)
	randRead   func(b []byte) (int, error)
	listDirFn  func(name string) ([]os.DirEntry, error)
	now        func() time.Time // injectable for testing; defaults to time.Now
}

// NewStorage roots the storage at dataDir/s3, creating the directory if needed.
func NewStorage(dataDir string) (*Storage, error) {
	return newStorage(dataDir, os.OpenRoot)
}

// Close releases the os.Root handle held by the storage.
func (s *Storage) Close() error {
	return s.root.Close()
}

func newStorage(dataDir string, openRoot func(string) (*os.Root, error)) (*Storage, error) {
	rootPath := filepath.Join(dataDir, "s3")
	if err := os.MkdirAll(rootPath, 0o750); err != nil {
		return nil, fmt.Errorf("create storage root: %w", err)
	}
	root, err := openRoot(rootPath)
	if err != nil {
		return nil, fmt.Errorf("open storage root: %w", err)
	}
	s := &Storage{root: root}
	s.removeFile = s.root.Remove
	s.openFile = func(name string, flag int, perm os.FileMode) (io.WriteCloser, error) {
		return s.root.OpenFile(name, flag, perm)
	}
	s.readAll = io.ReadAll
	s.randRead = rand.Read
	s.now = time.Now
	s.listDirFn = func(name string) ([]os.DirEntry, error) {
		f, err := s.root.Open(name)
		if err != nil {
			return nil, err
		}
		defer func() { _ = f.Close() }()
		return f.ReadDir(-1)
	}
	return s, nil
}

func (s *Storage) CreateBucket(bucket, region string, objectLockEnabled bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.root.Mkdir(bucket, 0o750); err != nil {
		return err
	}
	meta := bucketMeta{Region: region}
	if objectLockEnabled {
		meta.VersioningStatus = "Enabled"
		meta.ObjectLock = `<ObjectLockConfiguration xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><ObjectLockEnabled>Enabled</ObjectLockEnabled></ObjectLockConfiguration>`
	}
	if err := s.writeBucketMeta(bucket, meta); err != nil {
		if removeErr := s.root.Remove(bucket); removeErr != nil {
			slog.Warn(
				"failed to clean up bucket after meta write failure",
				"bucket",
				bucket,
				"err",
				removeErr,
			)
		}
		return err
	}
	return nil
}

func (s *Storage) DeleteBucket(bucket string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := s.readDir(bucket)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ErrBucketNotFound
		}
		return err
	}
	for _, e := range entries {
		if e.Name() == ".ver" {
			// .ver is an internal system directory; evaluated separately below.
			continue
		}
		return ErrBucketNotEmpty
	}
	// If .ver exists, make sure it holds no actual versioned objects.
	verDir := filepath.Join(bucket, ".ver")
	if !s.verDirIsEmpty(verDir) {
		return ErrBucketNotEmpty
	}
	// Remove the (empty) .ver tree so that the bucket directory itself becomes
	// empty and the final Remove call below succeeds.
	s.removeAllDir(verDir)

	if err := s.root.Remove(bucket + ".bucket.json"); err != nil &&
		!errors.Is(err, os.ErrNotExist) {
		slog.Warn("failed to remove bucket metadata", "bucket", bucket, "err", err)
	}
	return s.root.Remove(bucket)
}

func (s *Storage) BucketExists(bucket string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.bucketExistsLocked(bucket)
}

func (s *Storage) bucketExistsLocked(bucket string) bool {
	info, err := s.root.Stat(bucket)
	return err == nil && info.IsDir()
}

func (s *Storage) ListBuckets() ([]BucketInfo, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	entries, err := s.readDir(".")
	if err != nil {
		return nil, err
	}
	buckets := make([]BucketInfo, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		var creationDate time.Time
		if info, err := e.Info(); err == nil {
			creationDate = info.ModTime()
		}
		var region string
		if meta, err := s.readBucketMeta(e.Name()); err == nil {
			region = meta.Region
		}
		buckets = append(buckets, BucketInfo{
			Name:         e.Name(),
			CreationDate: creationDate,
			Region:       region,
		})
	}
	slices.SortFunc(buckets, func(a, b BucketInfo) int {
		return cmp.Compare(a.Name, b.Name)
	})
	return buckets, nil
}

func (s *Storage) PutObject(
	bucket, key string,
	r io.Reader,
	contentType string,
	userMetadata map[string]string,
	sseAlgorithm, sseKMSKeyID string,
	sseBucketKeyEnabled bool,
	ssecKeyMD5 string,
	retention *ObjectRetention,
	legalHold *ObjectLegalHold,
	storageClass string,
) (ObjectMetadata, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.bucketExistsLocked(bucket) {
		return ObjectMetadata{}, ErrBucketNotFound
	}
	if retention == nil {
		retention = s.bucketDefaultRetentionLocked(bucket)
	}

	objPath := filepath.Join(bucket, key)
	if dir := filepath.Dir(objPath); dir != bucket {
		if err := s.root.MkdirAll(dir, 0o750); err != nil {
			return ObjectMetadata{}, err
		}
	}

	versionID := ""
	if enabled, err := s.isVersioningEnabledLocked(bucket); err != nil {
		return ObjectMetadata{}, err
	} else if enabled {
		if err := s.archiveCurrentVersionLocked(bucket, key, objPath); err != nil {
			return ObjectMetadata{}, err
		}
		vid, err := s.newVersionID()
		if err != nil {
			return ObjectMetadata{}, err
		}
		versionID = vid
	}

	return s.writeObject(
		objPath,
		r,
		contentType,
		"",
		userMetadata,
		versionID,
		sseAlgorithm,
		sseKMSKeyID,
		sseBucketKeyEnabled,
		ssecKeyMD5,
		retention,
		legalHold,
		storageClass,
	)
}

// PutObjectIfNotExists is like PutObject but returns ErrObjectAlreadyExists if a live object already exists at key.
func (s *Storage) PutObjectIfNotExists(
	bucket, key string,
	r io.Reader,
	contentType string,
	userMetadata map[string]string,
	sseAlgorithm, sseKMSKeyID string,
	sseBucketKeyEnabled bool,
	ssecKeyMD5 string,
	retention *ObjectRetention,
	legalHold *ObjectLegalHold,
	storageClass string,
) (ObjectMetadata, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.bucketExistsLocked(bucket) {
		return ObjectMetadata{}, ErrBucketNotFound
	}
	if retention == nil {
		retention = s.bucketDefaultRetentionLocked(bucket)
	}

	objPath := filepath.Join(bucket, key)
	if existing, err := s.readMeta(objPath); err == nil {
		if !existing.IsDeleteMarker {
			return ObjectMetadata{}, &ObjectAlreadyExistsError{ETag: existing.ETag}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return ObjectMetadata{}, err
	}

	if dir := filepath.Dir(objPath); dir != bucket {
		if err := s.root.MkdirAll(dir, 0o750); err != nil {
			return ObjectMetadata{}, err
		}
	}

	versionID := ""
	if enabled, err := s.isVersioningEnabledLocked(bucket); err != nil {
		return ObjectMetadata{}, err
	} else if enabled {
		if err := s.archiveCurrentVersionLocked(bucket, key, objPath); err != nil {
			return ObjectMetadata{}, err // untestable: same lock prevents corrupting meta between the existence check above and this call
		}
		vid, err := s.newVersionID()
		if err != nil {
			return ObjectMetadata{}, err
		}
		versionID = vid
	}

	return s.writeObject(
		objPath,
		r,
		contentType,
		"",
		userMetadata,
		versionID,
		sseAlgorithm,
		sseKMSKeyID,
		sseBucketKeyEnabled,
		ssecKeyMD5,
		retention,
		legalHold,
		storageClass,
	)
}

// writeObject writes r to objPath and records metadata. If etag is non-empty
// it is used as-is (multipart ETag); otherwise the MD5 of the content is used.
// versionID is stored in the metadata; pass "" for non-versioned objects.
func (s *Storage) writeObject(
	objPath string,
	r io.Reader,
	contentType string,
	etag string,
	userMetadata map[string]string,
	versionID string,
	sseAlgorithm, sseKMSKeyID string,
	sseBucketKeyEnabled bool,
	ssecKeyMD5 string,
	retention *ObjectRetention,
	legalHold *ObjectLegalHold,
	storageClass string,
) (retMeta ObjectMetadata, retErr error) {
	f, err := s.openFile(objPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return ObjectMetadata{}, err
	}
	defer func() {
		if err := f.Close(); err != nil && retErr == nil {
			retErr = err
		}
	}()

	var w io.Writer = f
	var h hash.Hash
	if etag == "" {
		h = md5.New() // #nosec G401 -- MD5 is required by the S3 ETag specification
		w = io.MultiWriter(f, h)
	}
	size, err := io.Copy(w, r)
	if err != nil {
		return ObjectMetadata{}, err
	}

	if etag == "" {
		etag = `"` + hex.EncodeToString(h.Sum(nil)) + `"`
	}
	meta := ObjectMetadata{
		ContentType:         contentType,
		ETag:                etag,
		LastModified:        time.Now().UTC(),
		Size:                size,
		UserMetadata:        userMetadata,
		VersionID:           versionID,
		SSEAlgorithm:        sseAlgorithm,
		SSEKMSKeyID:         sseKMSKeyID,
		SSEBucketKeyEnabled: sseBucketKeyEnabled,
		SSECKeyMD5:          ssecKeyMD5,
		StorageClass:        storageClass,
		Retention:           retention,
		LegalHold:           legalHold,
	}
	if err := s.writeMeta(objPath, meta); err != nil {
		if removeErr := s.removeFile(objPath); removeErr != nil &&
			!errors.Is(removeErr, os.ErrNotExist) {
			slog.Warn("failed to clean up object after meta write error", "err", removeErr)
		}
		return ObjectMetadata{}, err
	}
	return meta, nil
}

func (s *Storage) GetObject(bucket, key string) (*os.File, ObjectMetadata, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.bucketExistsLocked(bucket) {
		return nil, ObjectMetadata{}, ErrBucketNotFound
	}
	objPath := filepath.Join(bucket, key)
	meta, err := s.readMeta(objPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ObjectMetadata{}, ErrObjectNotFound
		}
		return nil, ObjectMetadata{}, err
	}
	if meta.IsDeleteMarker {
		return nil, ObjectMetadata{}, &DeleteMarkerError{VersionID: meta.VersionID}
	}
	f, err := s.root.Open(objPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ObjectMetadata{}, ErrObjectNotFound
		}
		return nil, ObjectMetadata{}, err
	}
	return f, meta, nil
}

// CopyObject copies an object from src to dst. nil userMetadata and empty
// contentType mean COPY (inherit from source). Non-nil userMetadata (even an
// empty map) and non-empty contentType mean REPLACE (use provided values).
// srcVersionID, if non-empty, copies from that specific source version.
// sseAlgorithm and sseKMSKeyID are applied to the destination object.
// nil tags means COPY (inherit source tags); non-nil (even empty) means REPLACE.
func (s *Storage) CopyObject(
	srcBucket, srcKey, srcVersionID, dstBucket, dstKey string,
	contentType string,
	userMetadata map[string]string,
	sseAlgorithm, sseKMSKeyID string,
	sseBucketKeyEnabled bool,
	ssecKeyMD5 string,
	retention *ObjectRetention,
	legalHold *ObjectLegalHold,
	storageClass string,
	tags []Tag,
) (ObjectMetadata, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.bucketExistsLocked(srcBucket) {
		return ObjectMetadata{}, ErrBucketNotFound
	}

	// Resolve source path (current or versioned).
	var srcPath string
	var srcMeta ObjectMetadata
	if srcVersionID != "" {
		// Try current version first.
		curPath := filepath.Join(srcBucket, srcKey)
		cm, err := s.readMeta(curPath)
		if err == nil && cm.VersionID == srcVersionID {
			srcPath = curPath
			srcMeta = cm
		} else {
			// Fall back to archived version.
			vp := verPath(srcBucket, srcKey, srcVersionID)
			vm, err := s.readMeta(vp)
			if err != nil {
				if errors.Is(err, os.ErrNotExist) {
					return ObjectMetadata{}, ErrObjectNotFound
				}
				return ObjectMetadata{}, err
			}
			srcPath = vp
			srcMeta = vm
		}
		if srcMeta.IsDeleteMarker {
			return ObjectMetadata{}, ErrObjectNotFound
		}
	} else {
		srcPath = filepath.Join(srcBucket, srcKey)
		var err error
		srcMeta, err = s.readMeta(srcPath)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return ObjectMetadata{}, ErrObjectNotFound
			}
			return ObjectMetadata{}, err
		}
		if srcMeta.IsDeleteMarker {
			return ObjectMetadata{}, ErrObjectNotFound
		}
	}

	if !s.bucketExistsLocked(dstBucket) {
		return ObjectMetadata{}, ErrBucketNotFound
	}
	if retention == nil {
		retention = s.bucketDefaultRetentionLocked(dstBucket)
	}
	if userMetadata == nil {
		userMetadata = srcMeta.UserMetadata
	}
	if contentType == "" {
		contentType = srcMeta.ContentType
	}
	if storageClass == "" {
		storageClass = srcMeta.StorageClass
	}
	dstPath := filepath.Join(dstBucket, dstKey)

	dstVersionID := ""
	if enabled, err := s.isVersioningEnabledLocked(dstBucket); err != nil {
		return ObjectMetadata{}, err
	} else if enabled {
		if err := s.archiveCurrentVersionLocked(dstBucket, dstKey, dstPath); err != nil {
			return ObjectMetadata{}, err
		}
		vid, err := s.newVersionID()
		if err != nil {
			return ObjectMetadata{}, err
		}
		dstVersionID = vid
	}

	// Same-source-and-destination copy (non-versioned): opening the destination
	// with O_TRUNC would truncate the source file before reading it. Just refresh
	// LastModified instead.
	if srcPath == dstPath && dstVersionID == "" {
		meta := srcMeta
		meta.LastModified = time.Now().UTC()
		meta.ContentType = contentType
		meta.UserMetadata = userMetadata
		meta.SSEAlgorithm = sseAlgorithm
		meta.SSEKMSKeyID = sseKMSKeyID
		meta.SSEBucketKeyEnabled = sseBucketKeyEnabled
		meta.SSECKeyMD5 = ssecKeyMD5
		meta.StorageClass = storageClass
		meta.Retention = retention
		meta.LegalHold = legalHold
		if err := s.writeMeta(dstPath, meta); err != nil {
			return ObjectMetadata{}, err
		}
		// tags == nil means COPY; source tags already on dstPath — no action needed.
		if tags != nil {
			if err := s.applyTagsLocked(dstPath, tags); err != nil {
				return ObjectMetadata{}, err
			}
		}
		return meta, nil
	}
	srcFile, err := s.root.Open(srcPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ObjectMetadata{}, ErrObjectNotFound
		}
		return ObjectMetadata{}, err
	}
	defer func() { _ = srcFile.Close() }()
	if dir := filepath.Dir(dstPath); dir != dstBucket {
		if err := s.root.MkdirAll(dir, 0o750); err != nil {
			return ObjectMetadata{}, err
		}
	}
	dstMeta, err := s.writeObject(
		dstPath,
		srcFile,
		contentType,
		srcMeta.ETag,
		userMetadata,
		dstVersionID,
		sseAlgorithm,
		sseKMSKeyID,
		sseBucketKeyEnabled,
		ssecKeyMD5,
		retention,
		legalHold,
		storageClass,
	)
	if err != nil {
		return ObjectMetadata{}, err
	}
	// Resolve the tag set to write: COPY reads source tags, REPLACE uses provided tags.
	var dstTags []Tag
	if tags == nil {
		srcTags, readErr := readJSON[[]Tag](s, srcPath+".tags.json")
		if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
			// untestable: root.Open failures on .tags.json cannot be injected
			_ = s.deleteObjectFilesLocked(dstPath)
			return ObjectMetadata{}, readErr
		}
		dstTags = srcTags
	} else {
		dstTags = tags
	}
	if err := s.applyTagsLocked(dstPath, dstTags); err != nil {
		_ = s.deleteObjectFilesLocked(dstPath)
		return ObjectMetadata{}, err
	}
	return dstMeta, nil
}

// applyTagsLocked writes tags to the .tags.json sidecar for objPath when the
// set is non-empty, or removes a stale sidecar when the set is empty.
// Caller must hold the write lock.
func (s *Storage) applyTagsLocked(objPath string, tags []Tag) error {
	if len(tags) > 0 {
		return s.writeJSON(objPath+".tags.json", tags)
	}
	if err := s.removeFile(objPath + ".tags.json"); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// checkObjectLockLocked returns ErrObjectLocked if the object at objPath is
// protected by an active LegalHold or a Retention period that prevents deletion.
// bypassGovernance allows skipping GOVERNANCE-mode retention only.
// Caller must hold at least a read lock.
func (s *Storage) checkObjectLockLocked(objPath string, bypassGovernance bool) error {
	meta, err := s.readMeta(objPath)
	if err != nil {
		// Object doesn't exist or metadata unreadable — let the caller handle it.
		return nil
	}
	if meta.LegalHold != nil && meta.LegalHold.Status == "ON" {
		return ErrObjectLocked
	}
	if meta.Retention != nil {
		if s.now().UTC().Before(meta.Retention.RetainUntilDate) {
			if meta.Retention.Mode == "COMPLIANCE" {
				return ErrObjectLocked
			}
			// GOVERNANCE mode: blocked unless caller holds bypass privilege.
			if !bypassGovernance {
				return ErrObjectLocked
			}
		}
	}
	return nil
}

func (s *Storage) DeleteObject(bucket, key string, bypassGovernance bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.bucketExistsLocked(bucket) {
		return ErrBucketNotFound
	}
	objPath := filepath.Join(bucket, key)
	if err := s.checkObjectLockLocked(objPath, bypassGovernance); err != nil {
		return err
	}
	return s.deleteObjectFilesLocked(objPath)
}

// deleteObjectFilesLocked removes the body, metadata, and tags for an object.
// Caller must hold the write lock.
func (s *Storage) deleteObjectFilesLocked(objPath string) error {
	if err := s.root.Remove(objPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ErrObjectNotFound
		}
		return err
	}
	if err := s.root.Remove(objPath + ".meta.json"); err != nil && !errors.Is(err, os.ErrNotExist) {
		slog.Warn( // #nosec G706 -- objPath is an internal filesystem path derived from bucket/key, not direct user input
			"failed to remove metadata",
			"path",
			objPath,
			"err",
			err,
		)
	}
	if err := s.removeFile(objPath + ".tags.json"); err != nil && !errors.Is(err, os.ErrNotExist) {
		slog.Warn( // #nosec G706 -- objPath is an internal filesystem path derived from bucket/key, not direct user input
			"failed to remove tags",
			"path",
			objPath,
			"err",
			err,
		)
	}
	s.pruneEmptyAncestorsLocked(objPath)
	return nil
}

// pruneEmptyDirsUpTo removes empty ancestor directories starting from dir up
// to (but not including) stopDir. Caller must hold the write lock.
func (s *Storage) pruneEmptyDirsUpTo(dir, stopDir string) {
	for dir != stopDir && dir != "." && dir != "" {
		entries, err := s.readDir(dir)
		if err != nil || len(entries) != 0 {
			return
		}
		if err := s.root.Remove(dir); err != nil {
			return // untestable: os.Root.Remove failure cannot be injected
		}
		dir = filepath.Dir(dir)
	}
}

// pruneEmptyAncestorsLocked removes empty ancestor directories of objPath up
// to (but not including) the bucket root, so that DeleteBucket can succeed
// after all objects in a prefix have been removed.
// Caller must hold the write lock.
func (s *Storage) pruneEmptyAncestorsLocked(objPath string) {
	bucketRoot := strings.SplitN(objPath, string(filepath.Separator), 2)[0]
	s.pruneEmptyDirsUpTo(filepath.Dir(objPath), bucketRoot)
}

// DeleteObjectVersioned performs a versioning-aware delete.
// If versioning is enabled: archives current version and creates a delete marker.
// If versioning is disabled: removes the object normally (same as DeleteObject).
// Returns the versionID of the created delete marker (or "") and whether a
// delete marker was created.
func (s *Storage) DeleteObjectVersioned(
	bucket, key string,
	bypassGovernance bool,
) (string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.bucketExistsLocked(bucket) {
		return "", false, ErrBucketNotFound
	}

	enabled, err := s.isVersioningEnabledLocked(bucket)
	if err != nil {
		return "", false, err
	}

	objPath := filepath.Join(bucket, key)

	if !enabled {
		if err := s.checkObjectLockLocked(objPath, bypassGovernance); err != nil {
			return "", false, err
		}
		if err := s.deleteObjectFilesLocked(objPath); err != nil {
			if errors.Is(err, ErrObjectNotFound) {
				return "", false, nil // S3 returns 204 for non-existent objects
			}
			return "", false, err // untestable: os.Root.Remove failure cannot be injected
		}
		return "", false, nil
	}

	// Versioning enabled: archive current version (if any) then write delete marker.
	if err := s.archiveCurrentVersionLocked(bucket, key, objPath); err != nil {
		return "", false, err
	}

	vid, err := s.newVersionID()
	if err != nil {
		return "", false, err
	}

	// Write empty body for the delete marker.
	f, err := s.openFile(objPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return "", false, err
	}
	if err := f.Close(); err != nil {
		return "", false, err
	}
	marker := ObjectMetadata{
		LastModified:   time.Now().UTC(),
		VersionID:      vid,
		IsDeleteMarker: true,
	}
	if err := s.writeMeta(objPath, marker); err != nil {
		_ = s.removeFile(objPath)
		return "", false, err
	}
	return vid, true, nil
}

// DeleteObjectVersion permanently deletes a specific version (or delete marker) by versionID.
// Returns whether the deleted entry was a delete marker.
func (s *Storage) DeleteObjectVersion(
	bucket, key, versionID string,
	bypassGovernance bool,
) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.bucketExistsLocked(bucket) {
		return false, ErrBucketNotFound
	}

	objPath := filepath.Join(bucket, key)

	// Check if it's the current version.
	if cm, err := s.readMeta(objPath); err == nil && cm.VersionID == versionID {
		if !cm.IsDeleteMarker {
			if err := s.checkObjectLockLocked(objPath, bypassGovernance); err != nil {
				return false, err
			}
		}
		isMarker := cm.IsDeleteMarker
		if err := s.deleteObjectFilesLocked(objPath); err != nil &&
			!errors.Is(err, ErrObjectNotFound) {
			return false, err // untestable: os.Root.Remove failure cannot be injected
		}
		return isMarker, nil
	}

	// Check archived versions.
	vp := verPath(bucket, key, versionID)
	vm, err := s.readMeta(vp)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, ErrObjectNotFound
		}
		return false, err
	}
	if !vm.IsDeleteMarker {
		if err := s.checkObjectLockLocked(vp, bypassGovernance); err != nil {
			return false, err
		}
	}
	isMarker := vm.IsDeleteMarker
	if err := s.removeFile(vp); err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	if err := s.removeFile(vp + ".meta.json"); err != nil && !errors.Is(err, os.ErrNotExist) {
		slog.Warn( // #nosec G706 -- vp is an internal filesystem path derived from bucket/key, not direct user input
			"failed to remove archived version metadata",
			"path",
			vp,
			"err",
			err,
		)
	}
	if err := s.removeFile(vp + ".tags.json"); err != nil && !errors.Is(err, os.ErrNotExist) {
		slog.Warn( // #nosec G706 -- vp is an internal filesystem path derived from bucket/key, not direct user input
			"failed to remove archived version tags",
			"path",
			vp,
			"err",
			err,
		)
	}
	// Prune empty ancestor directories within the .ver tree so that
	// verDirIsEmpty does not treat leftover dirs as versioned objects.
	s.pruneEmptyDirsUpTo(filepath.Dir(vp), filepath.Join(bucket, ".ver"))
	return isMarker, nil
}

// GetObjectVersion retrieves a specific version of an object by versionID.
func (s *Storage) GetObjectVersion(
	bucket, key, versionID string,
) (*os.File, ObjectMetadata, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.bucketExistsLocked(bucket) {
		return nil, ObjectMetadata{}, ErrBucketNotFound
	}

	// Check current version.
	objPath := filepath.Join(bucket, key)
	if cm, err := s.readMeta(objPath); err == nil && cm.VersionID == versionID {
		if cm.IsDeleteMarker {
			return nil, ObjectMetadata{}, &DeleteMarkerError{VersionID: versionID}
		}
		f, err := s.root.Open(objPath)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil, ObjectMetadata{}, ErrObjectNotFound
			}
			return nil, ObjectMetadata{}, err // untestable: os.Root.Open failure cannot be injected
		}
		return f, cm, nil
	}

	// Check archived versions.
	vp := verPath(bucket, key, versionID)
	vm, err := s.readMeta(vp)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ObjectMetadata{}, ErrObjectNotFound
		}
		return nil, ObjectMetadata{}, err
	}
	if vm.IsDeleteMarker {
		return nil, ObjectMetadata{}, &DeleteMarkerError{VersionID: versionID}
	}
	f, err := s.root.Open(vp)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ObjectMetadata{}, ErrObjectNotFound
		}
		return nil, ObjectMetadata{}, err // untestable: os.Root.Open failure cannot be injected
	}
	return f, vm, nil
}

// HeadObjectVersion retrieves metadata for a specific version of an object.
func (s *Storage) HeadObjectVersion(bucket, key, versionID string) (ObjectMetadata, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.bucketExistsLocked(bucket) {
		return ObjectMetadata{}, ErrBucketNotFound
	}

	// Check current version.
	objPath := filepath.Join(bucket, key)
	if cm, err := s.readMeta(objPath); err == nil && cm.VersionID == versionID {
		if cm.IsDeleteMarker {
			return ObjectMetadata{}, &DeleteMarkerError{VersionID: versionID}
		}
		return cm, nil
	}

	// Check archived versions.
	vp := verPath(bucket, key, versionID)
	vm, err := s.readMeta(vp)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ObjectMetadata{}, ErrObjectNotFound
		}
		return ObjectMetadata{}, err
	}
	if vm.IsDeleteMarker {
		return ObjectMetadata{}, &DeleteMarkerError{VersionID: versionID}
	}
	return vm, nil
}

// ListObjectVersions returns all versions and delete markers for objects in the bucket.
func (s *Storage) ListObjectVersions(bucket string) ([]VersionInfo, []DeleteMarkerInfo, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.bucketExistsLocked(bucket) {
		return nil, nil, ErrBucketNotFound
	}

	var entries []versionEntry

	// Collect current-version objects (including delete markers).
	if err := s.collectVersionEntries(bucket, bucket, &entries); err != nil {
		return nil, nil, err
	}

	// Collect archived versions from .ver/<key>/<versionID>.
	verRoot := filepath.Join(bucket, ".ver")
	if err := s.collectArchivedEntries(verRoot, "", &entries); err != nil &&
		!errors.Is(err, os.ErrNotExist) {
		return nil, nil, err
	}

	// Sort by key ASC, then LastModified DESC (most recent first per key).
	slices.SortFunc(entries, func(a, b versionEntry) int {
		if c := cmp.Compare(a.key, b.key); c != 0 {
			return c
		}
		return b.meta.LastModified.Compare(a.meta.LastModified) // DESC: newest first
	})

	var versions []VersionInfo
	var deleteMarkers []DeleteMarkerInfo
	seenKeys := make(map[string]bool)

	for _, e := range entries {
		isLatest := !seenKeys[e.key]
		seenKeys[e.key] = true
		vid := e.meta.VersionID
		if vid == "" {
			vid = "null"
		}
		if e.meta.IsDeleteMarker {
			deleteMarkers = append(deleteMarkers, DeleteMarkerInfo{
				Key:             e.key,
				VersionID:       vid,
				IsLatest:        isLatest,
				LastModified:    e.meta.LastModified,
				NoncurrentSince: e.meta.NoncurrentSince,
			})
		} else {
			versions = append(versions, VersionInfo{
				Key:             e.key,
				VersionID:       vid,
				IsLatest:        isLatest,
				LastModified:    e.meta.LastModified,
				ETag:            e.meta.ETag,
				Size:            e.meta.Size,
				StorageClass:    e.meta.StorageClass,
				NoncurrentSince: e.meta.NoncurrentSince,
			})
		}
	}

	return versions, deleteMarkers, nil
}

// versionEntry is used internally by ListObjectVersions to collect versions.
type versionEntry struct {
	key  string
	meta ObjectMetadata
}

// collectVersionEntries walks dir collecting all current-version objects (and delete markers).
func (s *Storage) collectVersionEntries(bucket, dir string, entries *[]versionEntry) error {
	dirEntries, err := s.readDir(dir)
	if err != nil {
		return err
	}
	for _, e := range dirEntries {
		entryPath := filepath.Join(dir, e.Name())
		if e.IsDir() {
			if e.Name() == ".mpu" || e.Name() == ".ver" {
				continue
			}
			if err := s.collectVersionEntries(bucket, entryPath, entries); err != nil {
				return err
			}
			continue
		}
		if strings.HasSuffix(e.Name(), ".meta.json") || strings.HasSuffix(e.Name(), ".tags.json") {
			continue
		}
		key, _ := filepath.Rel(bucket, entryPath)
		meta, err := s.readMeta(entryPath)
		if err != nil {
			slog.Warn( // #nosec G706 -- entryPath is an internal filesystem path, not direct user input
				"skipping object with unreadable metadata",
				"path",
				entryPath,
				"err",
				err,
			)
			continue
		}
		*entries = append(*entries, versionEntry{key: key, meta: meta})
	}
	return nil
}

// collectArchivedEntries walks the .ver tree collecting archived versions.
// baseVerDir is the .ver directory path; relPath is the path relative to it.
func (s *Storage) collectArchivedEntries(
	baseVerDir, relPath string,
	entries *[]versionEntry,
) error {
	dirPath := baseVerDir
	if relPath != "" {
		dirPath = filepath.Join(baseVerDir, relPath)
	}
	dirEntries, err := s.readDir(dirPath)
	if err != nil {
		return err
	}
	for _, e := range dirEntries {
		entryRel := filepath.Join(relPath, e.Name())
		entryAbs := filepath.Join(baseVerDir, entryRel)
		if e.IsDir() {
			if err := s.collectArchivedEntries(baseVerDir, entryRel, entries); err != nil {
				return err
			}
			continue
		}
		if strings.HasSuffix(e.Name(), ".meta.json") || strings.HasSuffix(e.Name(), ".tags.json") {
			continue
		}
		// entryRel is <key>/<versionID>; key is everything but the last component.
		key := filepath.Dir(entryRel)
		meta, err := s.readMeta(entryAbs)
		if err != nil {
			slog.Warn( // #nosec G706 -- entryAbs is an internal filesystem path, not direct user input
				"skipping archived version with unreadable metadata",
				"path",
				entryAbs,
				"err",
				err,
			)
			continue
		}
		*entries = append(*entries, versionEntry{key: key, meta: meta})
	}
	return nil
}

func (s *Storage) HeadObject(bucket, key string) (ObjectMetadata, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.bucketExistsLocked(bucket) {
		return ObjectMetadata{}, ErrBucketNotFound
	}
	meta, err := s.readMeta(filepath.Join(bucket, key))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ObjectMetadata{}, ErrObjectNotFound
		}
		return ObjectMetadata{}, err
	}
	if meta.IsDeleteMarker {
		return ObjectMetadata{}, &DeleteMarkerError{VersionID: meta.VersionID}
	}
	return meta, nil
}

// SetObjectRestoreInitiated marks an object as having a restore request in progress.
// Subsequent calls to handleRestoreObject will detect this and return 200 instead of 202.
func (s *Storage) SetObjectRestoreInitiated(bucket, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.bucketExistsLocked(bucket) {
		return ErrBucketNotFound
	}
	objPath := filepath.Join(bucket, key)
	meta, err := s.readMeta(objPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ErrObjectNotFound
		}
		return err
	}
	meta.RestoreInitiated = true
	return s.writeMeta(objPath, meta)
}

// SetObjectReplicationStatus updates the ReplicationStatus field on the current
// version of an object.
func (s *Storage) SetObjectReplicationStatus(bucket, key, status string) error {
	if status != ReplicationStatusCompleted && status != ReplicationStatusReplica &&
		status != ReplicationStatusPending && status != ReplicationStatusFailed {
		return fmt.Errorf("invalid replication status: %s", status)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.bucketExistsLocked(bucket) {
		return ErrBucketNotFound
	}
	objPath := filepath.Join(bucket, key)
	meta, err := s.readMeta(objPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ErrObjectNotFound
		}
		return err // untestable: non-ErrNotExist readMeta failure cannot be injected via current test helpers
	}
	meta.ReplicationStatus = status
	return s.writeMeta(objPath, meta)
}

func (s *Storage) ListObjects(bucket string) ([]ObjectInfo, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.bucketExistsLocked(bucket) {
		return nil, ErrBucketNotFound
	}
	var objects []ObjectInfo
	if err := s.walkDir(bucket, bucket, &objects); err != nil {
		return nil, err
	}
	slices.SortFunc(objects, func(a, b ObjectInfo) int {
		return cmp.Compare(a.Key, b.Key)
	})
	return objects, nil
}

func (s *Storage) walkDir(bucket, dir string, objects *[]ObjectInfo) error {
	entries, err := s.readDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		entryPath := filepath.Join(dir, e.Name())
		if e.IsDir() {
			if e.Name() == ".mpu" || e.Name() == ".ver" {
				continue
			}
			if err := s.walkDir(bucket, entryPath, objects); err != nil {
				return err
			}
			continue
		}
		if strings.HasSuffix(e.Name(), ".meta.json") || strings.HasSuffix(e.Name(), ".tags.json") {
			continue
		}
		key, _ := filepath.Rel(bucket, entryPath)
		meta, err := s.readMeta(entryPath)
		if err != nil {
			slog.Warn( // #nosec G706 -- entryPath is an internal filesystem path, not direct user input
				"skipping object with unreadable metadata",
				"path",
				entryPath,
				"err",
				err,
			)
			continue
		}
		if meta.IsDeleteMarker {
			continue
		}
		*objects = append(*objects, ObjectInfo{Key: key, Metadata: meta})
	}
	return nil
}

func (s *Storage) readDir(name string) ([]os.DirEntry, error) {
	return s.listDirFn(name)
}

// newVersionID generates a random 16-character hex version ID.
func (s *Storage) newVersionID() (string, error) {
	b := make([]byte, 8)
	if _, err := s.randRead(b); err != nil {
		return "", fmt.Errorf("generate version ID: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// isVersioningEnabledLocked reports whether versioning is enabled on bucket.
// Caller must hold at least a read lock.
func (s *Storage) isVersioningEnabledLocked(bucket string) (bool, error) {
	meta, err := s.readBucketMeta(bucket)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	return meta.VersioningStatus == "Enabled", nil
}

// verPath returns the storage path for an archived version.
func verPath(bucket, key, versionID string) string {
	return filepath.Join(bucket, ".ver", key, versionID)
}

// archiveCurrentVersionLocked copies the current object at objPath to the
// versioned archive (.ver/<key>/<versionID>). It is a no-op if the object
// does not yet exist. Caller must hold the write lock.
func (s *Storage) archiveCurrentVersionLocked(bucket, key, objPath string) error {
	meta, err := s.readMeta(objPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil // nothing to archive
		}
		return err
	}

	vid := meta.VersionID
	if vid == "" {
		// Object predates versioning; assign a version ID now.
		vid, err = s.newVersionID()
		if err != nil {
			return err
		}
		meta.VersionID = vid
	}

	vp := verPath(bucket, key, vid)
	if err := s.root.MkdirAll(filepath.Dir(vp), 0o750); err != nil {
		return err // untestable: os.Root.MkdirAll failure cannot be injected
	}

	// Copy body.
	src, err := s.root.Open(objPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err // untestable: os.Root.Open failure cannot be injected
	}
	dst, err := s.openFile(vp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		_ = src.Close()
		return err
	}
	_, copyErr := io.Copy(dst, src)
	_ = src.Close()
	if err := dst.Close(); err != nil && copyErr == nil {
		copyErr = err
	}
	if copyErr != nil {
		_ = s.removeFile(vp)
		return copyErr
	}

	meta.NoncurrentSince = s.now()

	// Write metadata with resolved version ID.
	if err := s.writeJSON(vp+".meta.json", meta); err != nil {
		_ = s.removeFile(vp)
		return err
	}
	// Archive tags sidecar if present.
	if tagsData, tagsErr := readJSON[[]Tag](s, objPath+".tags.json"); tagsErr == nil {
		if writeErr := s.writeJSON(vp+".tags.json", tagsData); writeErr != nil {
			// untestable: openFile failure on the version-specific tags path cannot be injected separately from the body path
			_ = s.removeFile(vp)
			_ = s.removeFile(vp + ".meta.json")
			_ = s.removeFile(vp + ".tags.json")
			return writeErr
		}
	} else if !errors.Is(tagsErr, os.ErrNotExist) {
		// untestable: root.Open failures on .tags.json cannot be injected
		_ = s.removeFile(vp)
		_ = s.removeFile(vp + ".meta.json")
		return tagsErr
	}
	return nil
}

func (s *Storage) GetBucketRegion(bucket string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.bucketExistsLocked(bucket) {
		return "", ErrBucketNotFound
	}
	meta, err := s.readBucketMeta(bucket)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	return meta.Region, nil
}

// writeJSON marshals v as JSON and writes it to path, creating or truncating the file.
func (s *Storage) writeJSON(path string, v any) (retErr error) {
	data, _ := json.Marshal(v) // json.Marshal only fails for unmarshalable types (channels, funcs)
	f, err := s.openFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer func() {
		if err := f.Close(); err != nil && retErr == nil {
			retErr = err
		}
	}()
	_, retErr = f.Write(data)
	return
}

// readJSON reads path from the storage root and unmarshals its JSON content into T.
func readJSON[T any](s *Storage, path string) (T, error) {
	var zero T
	f, err := s.root.Open(path)
	if err != nil {
		return zero, err
	}
	defer func() { _ = f.Close() }()
	data, err := s.readAll(f)
	if err != nil {
		return zero, err
	}
	var v T
	if err := json.Unmarshal(data, &v); err != nil {
		return zero, err
	}
	return v, nil
}

func (s *Storage) writeBucketMeta(bucket string, meta bucketMeta) error {
	return s.writeJSON(bucket+".bucket.json", meta)
}

func (s *Storage) readBucketMeta(bucket string) (bucketMeta, error) {
	return readJSON[bucketMeta](s, bucket+".bucket.json")
}

func (s *Storage) writeMeta(objPath string, meta ObjectMetadata) error {
	return s.writeJSON(objPath+".meta.json", meta)
}

func (s *Storage) readMeta(objPath string) (ObjectMetadata, error) {
	return readJSON[ObjectMetadata](s, objPath+".meta.json")
}

func (s *Storage) PutObjectTagging(bucket, key string, tags []Tag) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.bucketExistsLocked(bucket) {
		return ErrBucketNotFound
	}
	objPath := filepath.Join(bucket, key)
	if _, err := s.readMeta(objPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ErrObjectNotFound
		}
		return err
	}
	return s.writeJSON(objPath+".tags.json", tags)
}

func (s *Storage) GetObjectTagging(bucket, key string) ([]Tag, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.bucketExistsLocked(bucket) {
		return nil, ErrBucketNotFound
	}
	objPath := filepath.Join(bucket, key)
	if _, err := s.readMeta(objPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrObjectNotFound
		}
		return nil, err
	}
	tags, err := readJSON[[]Tag](s, objPath+".tags.json")
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []Tag{}, nil
		}
		return nil, err
	}
	return tags, nil
}

func (s *Storage) DeleteObjectTagging(bucket, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.bucketExistsLocked(bucket) {
		return ErrBucketNotFound
	}
	objPath := filepath.Join(bucket, key)
	if _, err := s.readMeta(objPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ErrObjectNotFound
		}
		return err
	}
	if err := s.removeFile(objPath + ".tags.json"); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (s *Storage) PutBucketTagging(bucket string, tags []Tag) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.bucketExistsLocked(bucket) {
		return ErrBucketNotFound
	}
	meta, err := s.readBucketMeta(bucket)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		meta = bucketMeta{}
	}
	meta.Tags = tags
	return s.writeBucketMeta(bucket, meta)
}

func (s *Storage) GetBucketTagging(bucket string) ([]Tag, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.bucketExistsLocked(bucket) {
		return nil, ErrBucketNotFound
	}
	meta, err := s.readBucketMeta(bucket)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []Tag{}, nil
		}
		return nil, err
	}
	if meta.Tags == nil {
		return []Tag{}, nil
	}
	return meta.Tags, nil
}

func (s *Storage) DeleteBucketTagging(bucket string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.bucketExistsLocked(bucket) {
		return ErrBucketNotFound
	}
	meta, err := s.readBucketMeta(bucket)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if meta.Tags == nil {
		return nil
	}
	meta.Tags = nil
	return s.writeBucketMeta(bucket, meta)
}

func (s *Storage) PutBucketVersioning(bucket, status string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.bucketExistsLocked(bucket) {
		return ErrBucketNotFound
	}
	meta, err := s.readBucketMeta(bucket)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		meta = bucketMeta{}
	}
	meta.VersioningStatus = status
	return s.writeBucketMeta(bucket, meta)
}

func (s *Storage) GetBucketVersioning(bucket string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.bucketExistsLocked(bucket) {
		return "", ErrBucketNotFound
	}
	meta, err := s.readBucketMeta(bucket)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	return meta.VersioningStatus, nil
}

func (s *Storage) PutBucketCors(bucket string, rules []CORSRule) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.bucketExistsLocked(bucket) {
		return ErrBucketNotFound
	}
	meta, err := s.readBucketMeta(bucket)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		meta = bucketMeta{}
	}
	meta.CORSRules = rules
	return s.writeBucketMeta(bucket, meta)
}

func (s *Storage) GetBucketCors(bucket string) ([]CORSRule, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.bucketExistsLocked(bucket) {
		return nil, ErrBucketNotFound
	}
	meta, err := s.readBucketMeta(bucket)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrNoCORSConfiguration
		}
		return nil, err
	}
	if len(meta.CORSRules) == 0 {
		return nil, ErrNoCORSConfiguration
	}
	return meta.CORSRules, nil
}

func (s *Storage) DeleteBucketCors(bucket string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.bucketExistsLocked(bucket) {
		return ErrBucketNotFound
	}
	meta, err := s.readBucketMeta(bucket)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if meta.CORSRules == nil {
		return nil
	}
	meta.CORSRules = nil
	return s.writeBucketMeta(bucket, meta)
}

func (s *Storage) PutBucketPolicy(bucket, policy string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.bucketExistsLocked(bucket) {
		return ErrBucketNotFound
	}
	meta, err := s.readBucketMeta(bucket)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		meta = bucketMeta{}
	}
	meta.Policy = policy
	return s.writeBucketMeta(bucket, meta)
}

func (s *Storage) GetBucketPolicy(bucket string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.bucketExistsLocked(bucket) {
		return "", ErrBucketNotFound
	}
	meta, err := s.readBucketMeta(bucket)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", ErrNoBucketPolicy
		}
		return "", err
	}
	if meta.Policy == "" {
		return "", ErrNoBucketPolicy
	}
	return meta.Policy, nil
}

func (s *Storage) DeleteBucketPolicy(bucket string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.bucketExistsLocked(bucket) {
		return ErrBucketNotFound
	}
	meta, err := s.readBucketMeta(bucket)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if meta.Policy == "" {
		return nil
	}
	meta.Policy = ""
	return s.writeBucketMeta(bucket, meta)
}

// uploadMeta is stored as .mpu/<uploadID>/upload.json.
type uploadMeta struct {
	Bucket              string           `json:"bucket"`
	Key                 string           `json:"key"`
	ContentType         string           `json:"contentType"`
	Initiated           time.Time        `json:"initiated"`
	SSEAlgorithm        string           `json:"sseAlgorithm,omitempty"`
	SSEKMSKeyID         string           `json:"sseKmsKeyId,omitempty"`
	SSEBucketKeyEnabled bool             `json:"sseBucketKeyEnabled,omitempty"`
	SSECKeyMD5          string           `json:"ssecKeyMd5,omitempty"`
	StorageClass        string           `json:"storageClass,omitempty"`
	Retention           *ObjectRetention `json:"retention,omitempty"`
	LegalHold           *ObjectLegalHold `json:"legalHold,omitempty"`
}

// partMeta is stored as .mpu/<uploadID>/<partNumber>.part.meta.json.
type partMeta struct {
	ETag string `json:"etag"`
	Size int64  `json:"size"`
}

const mpuDir = ".mpu"

func (s *Storage) CreateMultipartUpload(
	bucket, key, contentType, sseAlgorithm, sseKMSKeyID string,
	sseBucketKeyEnabled bool,
	ssecKeyMD5 string,
	retention *ObjectRetention,
	legalHold *ObjectLegalHold,
	storageClass string,
) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.bucketExistsLocked(bucket) {
		return "", ErrBucketNotFound
	}
	if retention == nil {
		retention = s.bucketDefaultRetentionLocked(bucket)
	}
	var b [16]byte
	if _, err := s.randRead(b[:]); err != nil {
		return "", fmt.Errorf("generate upload ID: %w", err)
	}
	uploadID := hex.EncodeToString(b[:])
	uploadDir := filepath.Join(mpuDir, uploadID)
	if err := s.root.MkdirAll(uploadDir, 0o750); err != nil {
		return "", err
	}
	meta := uploadMeta{
		Bucket:              bucket,
		Key:                 key,
		ContentType:         contentType,
		Initiated:           time.Now().UTC(),
		SSEAlgorithm:        sseAlgorithm,
		SSEKMSKeyID:         sseKMSKeyID,
		SSEBucketKeyEnabled: sseBucketKeyEnabled,
		SSECKeyMD5:          ssecKeyMD5,
		StorageClass:        storageClass,
		Retention:           retention,
		LegalHold:           legalHold,
	}
	data, _ := json.Marshal(meta) // json.Marshal never fails for uploadMeta
	var uploadJSONWritten bool
	defer func() {
		if !uploadJSONWritten {
			if err := s.removeUploadDir(uploadDir); err != nil {
				slog.Warn( // #nosec G706 -- uploadDir is an internal path derived from the upload ID
					"failed to clean up upload dir after write failure",
					"uploadDir",
					uploadDir,
					"err",
					err,
				)
			}
		}
	}()
	f, err := s.openFile(
		filepath.Join(uploadDir, "upload.json"),
		os.O_WRONLY|os.O_CREATE|os.O_TRUNC,
		0o600,
	)
	if err != nil {
		return "", err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	uploadJSONWritten = true
	return uploadID, nil
}

func (s *Storage) UploadPart(uploadID string, partNumber int, r io.Reader) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.root.Stat(filepath.Join(mpuDir, uploadID, "upload.json")); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", ErrUploadNotFound
		}
		return "", err
	}
	partPath := filepath.Join(mpuDir, uploadID, fmt.Sprintf("%d.part", partNumber))
	f, err := s.openFile(partPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return "", err
	}
	defer func() {
		if err := f.Close(); err != nil {
			slog.Warn("failed to close part file", "err", err)
		}
	}()
	h := md5.New() // #nosec G401 -- MD5 is required by the S3 ETag specification
	size, err := io.Copy(io.MultiWriter(f, h), r)
	if err != nil {
		return "", err
	}
	etag := `"` + hex.EncodeToString(h.Sum(nil)) + `"`
	meta := partMeta{ETag: etag, Size: size}
	data, _ := json.Marshal(meta) // json.Marshal never fails for partMeta
	mf, err := s.openFile(partPath+".meta.json", os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return "", err
	}
	if _, err := mf.Write(data); err != nil {
		_ = mf.Close()
		return "", err
	}
	return etag, mf.Close()
}

// DeletePart removes the data and metadata files for a single upload part.
func (s *Storage) DeletePart(uploadID string, partNumber int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	partPath := filepath.Join(mpuDir, uploadID, fmt.Sprintf("%d.part", partNumber))
	if err := s.removeFile(partPath + ".meta.json"); err != nil &&
		!errors.Is(err, os.ErrNotExist) {
		slog.Warn("failed to remove part meta file", "path", partPath+".meta.json", "err", err)
	}
	return s.root.Remove(partPath)
}

// UploadPartCopy copies bytes from a source object (optionally a specific byte
// range) into a multipart upload part. It mirrors the AWS S3 UploadPartCopy API.
// copySourceVersionID is the VersionID of the source object actually copied
// (empty when the source bucket has versioning disabled or suspended).
func (s *Storage) UploadPartCopy(
	uploadID string,
	partNumber int,
	srcBucket, srcKey, srcVersionID string,
	br *byteRange,
) (string, time.Time, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Verify the upload exists.
	if _, err := s.root.Stat(filepath.Join(mpuDir, uploadID, "upload.json")); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", time.Time{}, "", ErrUploadNotFound
		}
		return "", time.Time{}, "", err
	}

	if !s.bucketExistsLocked(srcBucket) {
		return "", time.Time{}, "", ErrBucketNotFound
	}

	// Resolve source object path and metadata.
	var srcPath string
	var srcMeta ObjectMetadata
	if srcVersionID != "" {
		curPath := filepath.Join(srcBucket, srcKey)
		cm, err := s.readMeta(curPath)
		if err == nil && cm.VersionID == srcVersionID {
			srcPath = curPath
			srcMeta = cm
		} else {
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				return "", time.Time{}, "", err
			}
			vp := verPath(srcBucket, srcKey, srcVersionID)
			vm, err := s.readMeta(vp)
			if err != nil {
				if errors.Is(err, os.ErrNotExist) {
					return "", time.Time{}, "", ErrObjectNotFound
				}
				return "", time.Time{}, "", err // untestable: non-ErrNotExist readMeta failure cannot be injected
			}
			srcPath = vp
			srcMeta = vm
		}
		if srcMeta.IsDeleteMarker {
			return "", time.Time{}, "", ErrObjectNotFound
		}
	} else {
		srcPath = filepath.Join(srcBucket, srcKey)
		var err error
		srcMeta, err = s.readMeta(srcPath)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return "", time.Time{}, "", ErrObjectNotFound
			}
			return "", time.Time{}, "", err // untestable: non-ErrNotExist readMeta failure cannot be injected
		}
		if srcMeta.IsDeleteMarker {
			return "", time.Time{}, "", ErrObjectNotFound // untestable: non-versioned delete markers do not arise in normal operation
		}
	}

	srcFile, err := s.root.Open(srcPath)
	if err != nil { // untestable: os.Root.Open failure cannot be injected
		if errors.Is(err, os.ErrNotExist) {
			return "", time.Time{}, "", ErrObjectNotFound // untestable: race between readMeta and Open cannot be triggered in tests
		}
		return "", time.Time{}, "", err // untestable: non-ErrNotExist os.Root.Open failure cannot be injected
	}
	defer func() { _ = srcFile.Close() }()

	// Apply byte range if specified.
	var reader io.Reader = srcFile
	if br != nil {
		if _, err := srcFile.Seek(br.Start, io.SeekStart); err != nil {
			return "", time.Time{}, "", err // untestable: Seek failure on a valid file cannot be injected
		}
		reader = io.LimitReader(srcFile, br.End-br.Start+1)
	}

	// Write the part file.
	partPath := filepath.Join(mpuDir, uploadID, fmt.Sprintf("%d.part", partNumber))
	f, err := s.openFile(partPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return "", time.Time{}, "", err // untestable: os.Root.OpenFile failure cannot be injected
	}
	defer func() {
		if err := f.Close(); err != nil { // untestable: Close failure on a local file cannot be injected
			slog.Warn("failed to close part file", "err", err)
		}
	}()
	h := md5.New() // #nosec G401 -- MD5 is required by the S3 ETag specification
	size, err := io.Copy(io.MultiWriter(f, h), reader)
	if err != nil {
		return "", time.Time{}, "", err // untestable: io.Copy failure on a local file cannot be injected
	}
	resultEtag := `"` + hex.EncodeToString(h.Sum(nil)) + `"`
	now := time.Now().UTC()
	meta := partMeta{ETag: resultEtag, Size: size}
	data, _ := json.Marshal(meta) // json.Marshal never fails for partMeta
	mf, err := s.openFile(partPath+".meta.json", os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return "", time.Time{}, "", err // untestable: os.Root.OpenFile failure cannot be injected
	}
	if _, err := mf.Write(data); err != nil {
		_ = mf.Close()
		return "", time.Time{}, "", err // untestable: Write failure on a local file cannot be injected
	}
	return resultEtag, now, srcMeta.VersionID, mf.Close()
}

func (s *Storage) CompleteMultipartUpload(
	uploadID string,
	parts []CompletePart,
) (ObjectMetadata, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	uploadDir := filepath.Join(mpuDir, uploadID)
	umeta, err := s.readUploadMeta(uploadID)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ObjectMetadata{}, ErrUploadNotFound
		}
		return ObjectMetadata{}, err
	}
	if !s.bucketExistsLocked(umeta.Bucket) {
		return ObjectMetadata{}, ErrBucketNotFound
	}
	if len(parts) == 0 {
		return ObjectMetadata{}, ErrInvalidPart
	}
	for i, p := range parts {
		if i > 0 &&
			p.PartNumber <= parts[i-1].PartNumber { // #nosec G602 -- i > 0 guard ensures parts[i-1] is valid
			return ObjectMetadata{}, ErrInvalidPartOrder
		}
		pm, err := s.readPartMeta(uploadID, p.PartNumber)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return ObjectMetadata{}, ErrInvalidPart
			}
			return ObjectMetadata{}, err
		}
		if pm.ETag != p.ETag {
			return ObjectMetadata{}, ErrInvalidPart
		}
		if i < len(parts)-1 && pm.Size < minPartSize {
			return ObjectMetadata{}, ErrEntityTooSmall
		}
	}
	// Open all part files and assemble via io.MultiReader.
	files := make([]*os.File, 0, len(parts))
	defer func() {
		for _, f := range files {
			_ = f.Close()
		}
	}()
	readers := make([]io.Reader, 0, len(parts))
	// Compute multipart ETag: MD5 of the concatenated raw MD5 digests.
	h := md5.New() // #nosec G401 -- MD5 is required by the S3 ETag specification
	for _, p := range parts {
		partPath := filepath.Join(uploadDir, fmt.Sprintf("%d.part", p.PartNumber))
		f, err := s.root.Open(partPath)
		if err != nil {
			return ObjectMetadata{}, err
		}
		files = append(files, f)
		readers = append(readers, f)
		raw, _ := hex.DecodeString(strings.Trim(p.ETag, `"`))
		_, _ = h.Write(raw)
	}
	multipartETag := fmt.Sprintf(`"%s-%d"`, hex.EncodeToString(h.Sum(nil)), len(parts))
	objPath := filepath.Join(umeta.Bucket, umeta.Key)
	if dir := filepath.Dir(objPath); dir != umeta.Bucket {
		if err := s.root.MkdirAll(dir, 0o750); err != nil {
			return ObjectMetadata{}, err
		}
	}
	versionID := ""
	if enabled, verErr := s.isVersioningEnabledLocked(umeta.Bucket); verErr != nil {
		return ObjectMetadata{}, verErr
	} else if enabled {
		if err := s.archiveCurrentVersionLocked(umeta.Bucket, umeta.Key, objPath); err != nil {
			return ObjectMetadata{}, err
		}
		vid, err := s.newVersionID()
		if err != nil {
			return ObjectMetadata{}, err
		}
		versionID = vid
	}
	meta, err := s.writeObject(
		objPath,
		io.MultiReader(readers...),
		umeta.ContentType,
		multipartETag,
		nil,
		versionID,
		umeta.SSEAlgorithm,
		umeta.SSEKMSKeyID,
		umeta.SSEBucketKeyEnabled,
		umeta.SSECKeyMD5,
		umeta.Retention,
		umeta.LegalHold,
		umeta.StorageClass,
	)
	if err != nil {
		return ObjectMetadata{}, err
	}
	if err := s.removeUploadDir(uploadDir); err != nil {
		slog.Warn( // #nosec G706 -- uploadDir is an internal path derived from the upload ID
			"failed to clean up multipart upload temp files",
			"uploadDir",
			uploadDir,
			"err",
			err,
		)
	}
	return meta, nil
}

func (s *Storage) AbortMultipartUpload(uploadID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	uploadDir := filepath.Join(mpuDir, uploadID)
	if _, err := s.root.Stat(filepath.Join(uploadDir, "upload.json")); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ErrUploadNotFound
		}
		return err
	}
	return s.removeUploadDir(uploadDir)
}

func (s *Storage) readUploadMeta(uploadID string) (uploadMeta, error) {
	return readJSON[uploadMeta](s, filepath.Join(mpuDir, uploadID, "upload.json"))
}

func (s *Storage) GetUploadMeta(uploadID string) (uploadMeta, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	meta, err := s.readUploadMeta(uploadID)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return uploadMeta{}, ErrUploadNotFound
		}
		return uploadMeta{}, err // untestable: real-filesystem I/O errors on upload.json cannot be injected via the current test helpers
	}
	return meta, nil
}

func (s *Storage) readPartMeta(uploadID string, partNumber int) (partMeta, error) {
	return readJSON[partMeta](
		s,
		filepath.Join(mpuDir, uploadID, fmt.Sprintf("%d.part.meta.json", partNumber)),
	)
}

func (s *Storage) ListMultipartUploads(bucket string) ([]MultipartUploadInfo, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.bucketExistsLocked(bucket) {
		return nil, ErrBucketNotFound
	}
	entries, err := s.readDir(mpuDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var uploads []MultipartUploadInfo
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		uploadID := e.Name()
		umeta, err := s.readUploadMeta(uploadID)
		if err != nil {
			slog.Warn( // #nosec G706 -- uploadID is an internal identifier, not direct user input
				"skipping upload with unreadable metadata",
				"uploadID",
				uploadID,
				"err",
				err,
			)
			continue
		}
		if umeta.Bucket != bucket {
			continue
		}
		uploads = append(uploads, MultipartUploadInfo{
			UploadID:     uploadID,
			Key:          umeta.Key,
			Initiated:    umeta.Initiated,
			StorageClass: umeta.StorageClass,
		})
	}
	return uploads, nil
}

func (s *Storage) ListParts(uploadID string) (uploadMeta, []PartInfo, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	umeta, err := s.readUploadMeta(uploadID)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return uploadMeta{}, nil, ErrUploadNotFound
		}
		return uploadMeta{}, nil, err
	}
	entries, err := s.readDir(filepath.Join(mpuDir, uploadID))
	if err != nil {
		return uploadMeta{}, nil, err
	}
	var parts []PartInfo
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".part.meta.json") {
			continue
		}
		var partNumber int
		if _, err := fmt.Sscanf(e.Name(), "%d.part.meta.json", &partNumber); err != nil {
			continue
		}
		pm, err := s.readPartMeta(uploadID, partNumber)
		if err != nil {
			slog.Warn( // #nosec G706 -- uploadID/partNumber are internal identifiers
				"skipping part with unreadable metadata",
				"uploadID",
				uploadID,
				"partNumber",
				partNumber,
				"err",
				err,
			)
			continue
		}
		var lastModified time.Time
		if info, err := e.Info(); err == nil {
			lastModified = info.ModTime()
		}
		parts = append(parts, PartInfo{
			PartNumber:   partNumber,
			ETag:         pm.ETag,
			Size:         pm.Size,
			LastModified: lastModified,
		})
	}
	slices.SortFunc(parts, func(a, b PartInfo) int {
		return cmp.Compare(a.PartNumber, b.PartNumber)
	})
	return umeta, parts, nil
}

// putBucketConfigField reads bucket meta, applies set(), and writes it back.
func (s *Storage) putBucketConfigField(bucket string, set func(*bucketMeta)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.bucketExistsLocked(bucket) {
		return ErrBucketNotFound
	}
	meta, err := s.readBucketMeta(bucket)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		meta = bucketMeta{}
	}
	set(&meta)
	return s.writeBucketMeta(bucket, meta)
}

// getBucketConfigField reads bucket meta and extracts one field.
func (s *Storage) getBucketConfigField(bucket string, get func(bucketMeta) string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.bucketExistsLocked(bucket) {
		return "", ErrBucketNotFound
	}
	meta, err := s.readBucketMeta(bucket)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	return get(meta), nil
}

func (s *Storage) PutPublicAccessBlock(bucket, xmlBody string) error {
	return s.putBucketConfigField(bucket, func(m *bucketMeta) { m.PublicAccessBlock = xmlBody })
}

func (s *Storage) GetPublicAccessBlock(bucket string) (string, error) {
	return s.getBucketConfigField(bucket, func(m bucketMeta) string { return m.PublicAccessBlock })
}

func (s *Storage) DeletePublicAccessBlock(bucket string) error {
	return s.putBucketConfigField(bucket, func(m *bucketMeta) { m.PublicAccessBlock = "" })
}

func (s *Storage) PutBucketEncryption(bucket, xmlBody string) error {
	return s.putBucketConfigField(bucket, func(m *bucketMeta) { m.Encryption = xmlBody })
}

func (s *Storage) GetBucketEncryption(bucket string) (string, error) {
	return s.getBucketConfigField(bucket, func(m bucketMeta) string { return m.Encryption })
}

func (s *Storage) DeleteBucketEncryption(bucket string) error {
	return s.putBucketConfigField(bucket, func(m *bucketMeta) { m.Encryption = "" })
}

func (s *Storage) PutBucketOwnershipControls(bucket, xmlBody string) error {
	return s.putBucketConfigField(bucket, func(m *bucketMeta) { m.OwnershipControls = xmlBody })
}

func (s *Storage) GetBucketOwnershipControls(bucket string) (string, error) {
	return s.getBucketConfigField(bucket, func(m bucketMeta) string { return m.OwnershipControls })
}

func (s *Storage) DeleteBucketOwnershipControls(bucket string) error {
	return s.putBucketConfigField(bucket, func(m *bucketMeta) { m.OwnershipControls = "" })
}

func (s *Storage) PutBucketNotification(bucket, xmlBody string) error {
	return s.putBucketConfigField(bucket, func(m *bucketMeta) { m.Notification = xmlBody })
}

func (s *Storage) GetBucketNotification(bucket string) (string, error) {
	return s.getBucketConfigField(bucket, func(m bucketMeta) string { return m.Notification })
}

func (s *Storage) PutBucketLifecycle(bucket, xmlBody string) error {
	return s.putBucketConfigField(bucket, func(m *bucketMeta) { m.Lifecycle = xmlBody })
}

func (s *Storage) GetBucketLifecycle(bucket string) (string, error) {
	return s.getBucketConfigField(bucket, func(m bucketMeta) string { return m.Lifecycle })
}

func (s *Storage) DeleteBucketLifecycle(bucket string) error {
	return s.putBucketConfigField(bucket, func(m *bucketMeta) { m.Lifecycle = "" })
}

func (s *Storage) PutBucketWebsite(bucket, xmlBody string) error {
	return s.putBucketConfigField(bucket, func(m *bucketMeta) { m.Website = xmlBody })
}

func (s *Storage) GetBucketWebsite(bucket string) (string, error) {
	return s.getBucketConfigField(bucket, func(m bucketMeta) string { return m.Website })
}

func (s *Storage) DeleteBucketWebsite(bucket string) error {
	return s.putBucketConfigField(bucket, func(m *bucketMeta) { m.Website = "" })
}

func (s *Storage) PutBucketLogging(bucket, xmlBody string) error {
	return s.putBucketConfigField(bucket, func(m *bucketMeta) { m.Logging = xmlBody })
}

func (s *Storage) GetBucketLogging(bucket string) (string, error) {
	return s.getBucketConfigField(bucket, func(m bucketMeta) string { return m.Logging })
}

// WriteAccessLog writes a log record as a new S3 object at key inside
// targetBucket. The object is plain text with no encryption or versioning
// parameters (versionID is always empty; log keys include timestamps and nonces
// to avoid collisions without requiring versioning).
func (s *Storage) WriteAccessLog(targetBucket, key, content string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.bucketExistsLocked(targetBucket) {
		return ErrBucketNotFound
	}
	objPath := filepath.Join(targetBucket, key)
	if dir := filepath.Dir(objPath); dir != targetBucket {
		if err := s.root.MkdirAll(dir, 0o750); err != nil {
			return err // untestable: s.root.MkdirAll failure cannot be injected
		}
	}
	_, err := s.writeObject(
		objPath,
		strings.NewReader(content),
		"text/plain",
		"",
		nil,
		"",
		"",
		"",
		false,
		"",
		nil,
		nil,
		"STANDARD",
	)
	return err
}

func (s *Storage) PutBucketAccelerate(bucket, xmlBody string) error {
	return s.putBucketConfigField(bucket, func(m *bucketMeta) { m.Accelerate = xmlBody })
}

func (s *Storage) GetBucketAccelerate(bucket string) (string, error) {
	return s.getBucketConfigField(bucket, func(m bucketMeta) string { return m.Accelerate })
}

func (s *Storage) PutBucketReplication(bucket, xmlBody string) error {
	return s.putBucketConfigField(bucket, func(m *bucketMeta) { m.Replication = xmlBody })
}

func (s *Storage) GetBucketReplication(bucket string) (string, error) {
	return s.getBucketConfigField(bucket, func(m bucketMeta) string { return m.Replication })
}

func (s *Storage) DeleteBucketReplication(bucket string) error {
	return s.putBucketConfigField(bucket, func(m *bucketMeta) { m.Replication = "" })
}

func (s *Storage) PutBucketRequestPayment(bucket, xmlBody string) error {
	return s.putBucketConfigField(bucket, func(m *bucketMeta) { m.RequestPayment = xmlBody })
}

func (s *Storage) GetBucketRequestPayment(bucket string) (string, error) {
	return s.getBucketConfigField(bucket, func(m bucketMeta) string { return m.RequestPayment })
}

func (s *Storage) PutBucketObjectLock(bucket, xmlBody string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.bucketExistsLocked(bucket) {
		return ErrBucketNotFound
	}
	meta, err := s.readBucketMeta(bucket)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		slog.Error("bucket exists but metadata file is missing", "bucket", bucket) // #nosec G706
		meta = bucketMeta{}
	}
	if meta.VersioningStatus != "Enabled" {
		return ErrInvalidBucketState
	}
	meta.ObjectLock = xmlBody
	return s.writeBucketMeta(bucket, meta)
}

func (s *Storage) GetBucketObjectLock(bucket string) (string, error) {
	return s.getBucketConfigField(bucket, func(m bucketMeta) string { return m.ObjectLock })
}

// resolveObjectMetaLocked returns the metafile path and metadata for
// (bucket, key, versionID). If versionID is empty, resolves to the current
// version. Caller must hold at least a read lock.
func (s *Storage) resolveObjectMetaLocked(
	bucket, key, versionID string,
) (string, ObjectMetadata, error) {
	objPath := filepath.Join(bucket, key)
	if versionID == "" {
		meta, err := s.readMeta(objPath)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return "", ObjectMetadata{}, ErrObjectNotFound
			}
			return "", ObjectMetadata{}, err // untestable: non-ErrNotExist readMeta failure cannot be injected
		}
		if meta.IsDeleteMarker {
			return "", ObjectMetadata{}, &DeleteMarkerError{VersionID: meta.VersionID}
		}
		return objPath, meta, nil
	}
	// versionID specified: check current version first, then archived.
	if cm, err := s.readMeta(objPath); err == nil && cm.VersionID == versionID {
		if cm.IsDeleteMarker {
			return "", ObjectMetadata{}, &DeleteMarkerError{VersionID: versionID}
		}
		return objPath, cm, nil
	}
	vp := verPath(bucket, key, versionID)
	vm, err := s.readMeta(vp)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", ObjectMetadata{}, ErrObjectNotFound
		}
		return "", ObjectMetadata{}, err // untestable: non-ErrNotExist readMeta failure cannot be injected
	}
	if vm.IsDeleteMarker {
		return "", ObjectMetadata{}, &DeleteMarkerError{VersionID: versionID}
	}
	return vp, vm, nil
}

func (s *Storage) PutObjectRetention(
	bucket, key, versionID string,
	retention ObjectRetention,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.bucketExistsLocked(bucket) {
		return ErrBucketNotFound
	}
	path, meta, err := s.resolveObjectMetaLocked(bucket, key, versionID)
	if err != nil {
		return err
	}
	meta.Retention = &retention
	return s.writeMeta(path, meta)
}

func (s *Storage) GetObjectRetention(bucket, key, versionID string) (ObjectRetention, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.bucketExistsLocked(bucket) {
		return ObjectRetention{}, ErrBucketNotFound
	}
	_, meta, err := s.resolveObjectMetaLocked(bucket, key, versionID)
	if err != nil {
		return ObjectRetention{}, err
	}
	if meta.Retention == nil {
		return ObjectRetention{}, ErrNoObjectRetention
	}
	return *meta.Retention, nil
}

func (s *Storage) PutObjectLegalHold(bucket, key, versionID, status string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.bucketExistsLocked(bucket) {
		return ErrBucketNotFound
	}
	path, meta, err := s.resolveObjectMetaLocked(bucket, key, versionID)
	if err != nil {
		return err
	}
	meta.LegalHold = &ObjectLegalHold{Status: status}
	return s.writeMeta(path, meta)
}

func (s *Storage) GetObjectLegalHold(bucket, key, versionID string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.bucketExistsLocked(bucket) {
		return "", ErrBucketNotFound
	}
	_, meta, err := s.resolveObjectMetaLocked(bucket, key, versionID)
	if err != nil {
		return "", err
	}
	if meta.LegalHold == nil {
		return "", ErrNoObjectLegalHold
	}
	return meta.LegalHold.Status, nil
}

// removeUploadDir removes all files in uploadDir and then the directory itself.
// os.Root does not expose RemoveAll, so we must walk and remove manually.
func (s *Storage) removeUploadDir(uploadDir string) error {
	entries, err := s.readDir(uploadDir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if err := s.root.Remove(filepath.Join(uploadDir, e.Name())); err != nil &&
			!errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return s.root.Remove(uploadDir)
}

// removeAllDir recursively removes dir and all of its contents.
// Silently ignores os.ErrNotExist at each step.
func (s *Storage) removeAllDir(dir string) {
	entries, err := s.readDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		child := filepath.Join(dir, e.Name())
		if e.IsDir() {
			s.removeAllDir(child)
		} else {
			_ = s.root.Remove(child) // untestable: verDirIsEmpty guarantees only empty dirs reach here
		}
	}
	_ = s.root.Remove(dir)
}

// verDirIsEmpty reports whether the .ver directory tree contains no versioned
// objects.  Returns true when verDir does not exist or contains only empty
// subdirectories (which can happen after all versions of every object have
// been individually deleted).
func (s *Storage) verDirIsEmpty(verDir string) bool {
	entries, err := s.readDir(verDir)
	if err != nil {
		return true // .ver does not exist
	}
	for _, e := range entries {
		if !e.IsDir() {
			return false // unexpected file — treat as non-empty
		}
		if !s.verDirIsEmpty(filepath.Join(verDir, e.Name())) {
			return false
		}
	}
	return true
}

// parseBucketDefaultRetention parses a stored ObjectLockConfiguration XML and
// returns the computed ObjectRetention if a DefaultRetention rule is present.
// Returns nil when no rule is configured or the XML is unparseable.
func parseBucketDefaultRetention(xmlBody string, now time.Time) *ObjectRetention {
	if xmlBody == "" {
		return nil
	}
	var cfg struct {
		Rule *struct {
			DefaultRetention *struct {
				Mode  string `xml:"Mode"`
				Days  int    `xml:"Days"`
				Years int    `xml:"Years"`
			} `xml:"DefaultRetention"`
		} `xml:"Rule"`
	}
	if err := xml.Unmarshal([]byte(xmlBody), &cfg); err != nil || cfg.Rule == nil ||
		cfg.Rule.DefaultRetention == nil {
		return nil
	}
	dr := cfg.Rule.DefaultRetention
	if dr.Mode != "GOVERNANCE" && dr.Mode != "COMPLIANCE" {
		return nil
	}
	// Clamp to AWS-documented limits (Days: 1–36500, Years: 1–100) to prevent
	// pathologically large values from producing nonsensical retention dates.
	const maxDays, maxYears = 36500, 100
	var retainUntil time.Time
	switch {
	case dr.Days > 0 && dr.Days <= maxDays:
		retainUntil = now.UTC().AddDate(0, 0, dr.Days)
	case dr.Years > 0 && dr.Years <= maxYears:
		retainUntil = now.UTC().AddDate(dr.Years, 0, 0)
	default:
		return nil
	}
	return &ObjectRetention{Mode: dr.Mode, RetainUntilDate: retainUntil}
}

func (s *Storage) PutBucketACL(bucket, xmlBody string) error {
	return s.putBucketConfigField(bucket, func(m *bucketMeta) { m.ACL = xmlBody })
}

func (s *Storage) GetBucketACL(bucket string) (string, error) {
	return s.getBucketConfigField(bucket, func(m bucketMeta) string { return m.ACL })
}

func (s *Storage) PutObjectACL(bucket, key, xmlBody string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.bucketExistsLocked(bucket) {
		return ErrBucketNotFound
	}
	objPath := filepath.Join(bucket, key)
	meta, err := s.readMeta(objPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ErrObjectNotFound
		}
		return err // untestable: non-ErrNotExist readMeta failure cannot be injected
	}
	meta.ACL = xmlBody
	return s.writeMeta(objPath, meta)
}

func (s *Storage) GetObjectACL(bucket, key string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.bucketExistsLocked(bucket) {
		return "", ErrBucketNotFound
	}
	meta, err := s.readMeta(filepath.Join(bucket, key))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", ErrObjectNotFound
		}
		return "", err // untestable: non-ErrNotExist readMeta failure cannot be injected
	}
	return meta.ACL, nil
}

// bucketDefaultRetentionLocked returns the bucket-level default ObjectRetention,
// or nil if none is configured or the meta file cannot be read. Caller must
// hold at least a read lock.
func (s *Storage) bucketDefaultRetentionLocked(bucket string) *ObjectRetention {
	meta, err := s.readBucketMeta(bucket)
	if err != nil {
		return nil // silently return nil on read error; bucket was just confirmed to exist
	}
	return parseBucketDefaultRetention(meta.ObjectLock, s.now())
}
