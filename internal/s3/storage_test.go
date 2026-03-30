package s3

import (
	"strings"
	"testing"
)

func newTestStorage(t *testing.T) *Storage {
	t.Helper()
	s, err := NewStorage(t.TempDir())
	if err != nil {
		t.Fatalf("NewStorage: %v", err)
	}
	return s
}

func TestCreateAndDeleteBucket(t *testing.T) {
	s := newTestStorage(t)

	if err := s.CreateBucket("my-bucket"); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}
	if !s.BucketExists("my-bucket") {
		t.Fatal("expected bucket to exist")
	}

	if err := s.DeleteBucket("my-bucket"); err != nil {
		t.Fatalf("DeleteBucket: %v", err)
	}
	if s.BucketExists("my-bucket") {
		t.Fatal("expected bucket to be deleted")
	}
}

func TestDeleteBucketNotEmpty(t *testing.T) {
	s := newTestStorage(t)
	_ = s.CreateBucket("my-bucket")
	_, _ = s.PutObject("my-bucket", "obj.txt", strings.NewReader("hello"), "text/plain")

	if err := s.DeleteBucket("my-bucket"); err != ErrBucketNotEmpty {
		t.Errorf("expected ErrBucketNotEmpty, got %v", err)
	}
}

func TestPutAndGetObject(t *testing.T) {
	s := newTestStorage(t)
	_ = s.CreateBucket("my-bucket")

	meta, err := s.PutObject(
		"my-bucket",
		"hello.txt",
		strings.NewReader("hello world"),
		"text/plain",
	)
	if err != nil {
		t.Fatalf("PutObject: %v", err)
	}
	if meta.Size != 11 {
		t.Errorf("expected size 11, got %d", meta.Size)
	}
	if meta.ContentType != "text/plain" {
		t.Errorf("unexpected content type: %s", meta.ContentType)
	}
	if meta.ETag == "" {
		t.Error("expected non-empty ETag")
	}

	f, gotMeta, err := s.GetObject("my-bucket", "hello.txt")
	if err != nil {
		t.Fatalf("GetObject: %v", err)
	}
	defer func() { _ = f.Close() }()

	if gotMeta.Size != meta.Size {
		t.Errorf("meta size mismatch: %d != %d", gotMeta.Size, meta.Size)
	}
}

func TestGetObjectNotFound(t *testing.T) {
	s := newTestStorage(t)
	_ = s.CreateBucket("my-bucket")

	_, _, err := s.GetObject("my-bucket", "missing.txt")
	if err != ErrObjectNotFound {
		t.Errorf("expected ErrObjectNotFound, got %v", err)
	}
}

func TestDeleteObject(t *testing.T) {
	s := newTestStorage(t)
	_ = s.CreateBucket("my-bucket")
	_, _ = s.PutObject("my-bucket", "obj.txt", strings.NewReader("data"), "text/plain")

	if err := s.DeleteObject("my-bucket", "obj.txt"); err != nil {
		t.Fatalf("DeleteObject: %v", err)
	}
	_, _, err := s.GetObject("my-bucket", "obj.txt")
	if err != ErrObjectNotFound {
		t.Errorf("expected ErrObjectNotFound after delete, got %v", err)
	}
}

func TestListBuckets(t *testing.T) {
	s := newTestStorage(t)
	_ = s.CreateBucket("bucket-a")
	_ = s.CreateBucket("bucket-b")

	buckets, err := s.ListBuckets()
	if err != nil {
		t.Fatalf("ListBuckets: %v", err)
	}
	if len(buckets) != 2 {
		t.Errorf("expected 2 buckets, got %d", len(buckets))
	}
}

func TestListObjects(t *testing.T) {
	s := newTestStorage(t)
	_ = s.CreateBucket("my-bucket")
	_, _ = s.PutObject("my-bucket", "a.txt", strings.NewReader("a"), "text/plain")
	_, _ = s.PutObject("my-bucket", "b.txt", strings.NewReader("b"), "text/plain")

	objects, err := s.ListObjects("my-bucket")
	if err != nil {
		t.Fatalf("ListObjects: %v", err)
	}
	if len(objects) != 2 {
		t.Errorf("expected 2 objects, got %d", len(objects))
	}
}
