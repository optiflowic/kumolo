resource "aws_s3_bucket" "main" {
  bucket        = "kumolo-tf-verify"
  force_destroy = true
}

resource "aws_s3_bucket_versioning" "main" {
  bucket = aws_s3_bucket.main.id

  versioning_configuration {
    status = "Enabled"
  }
}

resource "aws_s3_bucket_server_side_encryption_configuration" "main" {
  bucket = aws_s3_bucket.main.id

  rule {
    apply_server_side_encryption_by_default {
      sse_algorithm = "AES256"
    }
  }
}

resource "aws_s3_object" "readme" {
  bucket       = aws_s3_bucket.main.id
  key          = "docs/README.txt"
  content      = "Hello from kumolo via Terraform!"
  content_type = "text/plain"

  tags = {
    Environment = "local"
    ManagedBy   = "terraform"
  }
}

resource "aws_s3_object" "config" {
  bucket       = aws_s3_bucket.main.id
  key          = "config/app.json"
  content      = jsonencode({ env = "local", emulator = "kumolo" })
  content_type = "application/json"
}

# ---------------------------------------------------------------------------
# BucketLifecycle — NoncurrentVersionExpiration + NoncurrentVersionTransition
# ---------------------------------------------------------------------------
resource "aws_s3_bucket_lifecycle_configuration" "main" {
  depends_on = [aws_s3_bucket_versioning.main]

  bucket = aws_s3_bucket.main.id

  rule {
    id     = "expire-noncurrent"
    status = "Enabled"

    filter {}

    noncurrent_version_expiration {
      noncurrent_days = 90
    }
  }

  rule {
    id     = "transition-noncurrent"
    status = "Enabled"

    filter {}

    noncurrent_version_transition {
      noncurrent_days = 30
      storage_class   = "GLACIER"
    }
  }

  rule {
    id     = "abort-multipart-uploads"
    status = "Enabled"

    filter {}

    abort_incomplete_multipart_upload {
      days_after_initiation = 7
    }
  }
}

# ---------------------------------------------------------------------------
# BucketReplication
# ---------------------------------------------------------------------------
resource "aws_s3_bucket" "replica" {
  bucket        = "kumolo-tf-verify-replica"
  force_destroy = true
}

resource "aws_s3_bucket_versioning" "replica" {
  bucket = aws_s3_bucket.replica.id

  versioning_configuration {
    status = "Enabled"
  }
}

resource "aws_s3_bucket_replication_configuration" "main" {
  depends_on = [aws_s3_bucket_versioning.main, aws_s3_bucket_versioning.replica]

  bucket = aws_s3_bucket.main.id
  role   = "arn:aws:iam::000000000000:role/replication-role"

  rule {
    id     = "replicate-all"
    status = "Enabled"

    delete_marker_replication {
      status = "Enabled"
    }

    destination {
      bucket = aws_s3_bucket.replica.arn
    }
  }
}

# ---------------------------------------------------------------------------
# ObjectLock — DefaultRetention applied automatically to new objects
# ---------------------------------------------------------------------------
resource "aws_s3_bucket" "objectlock" {
  bucket              = "kumolo-tf-verify-objectlock"
  force_destroy       = true
  object_lock_enabled = true
}

resource "aws_s3_bucket_object_lock_configuration" "objectlock" {
  bucket = aws_s3_bucket.objectlock.id

  rule {
    default_retention {
      mode = "GOVERNANCE"
      days = 1
    }
  }
}

# ---------------------------------------------------------------------------
# SSE-KMS — separate bucket using the KMS key defined in kms.tf
# ---------------------------------------------------------------------------
resource "aws_s3_bucket" "kms_encrypted" {
  bucket        = "kumolo-tf-verify-kms"
  force_destroy = true
}

resource "aws_s3_bucket_server_side_encryption_configuration" "kms_encrypted" {
  bucket = aws_s3_bucket.kms_encrypted.id

  rule {
    apply_server_side_encryption_by_default {
      sse_algorithm     = "aws:kms"
      kms_master_key_id = aws_kms_key.main.arn
    }
  }
}

resource "aws_s3_object" "kms_encrypted" {
  bucket  = aws_s3_bucket.kms_encrypted.id
  key     = "encrypted.txt"
  content = "KMS-encrypted content via Terraform"
}

# ---------------------------------------------------------------------------
# BucketPolicy
# ---------------------------------------------------------------------------
resource "aws_s3_bucket_policy" "main" {
  bucket = aws_s3_bucket.main.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Sid       = "AllowOwnerGet"
      Effect    = "Allow"
      Principal = { AWS = "arn:aws:iam::000000000000:root" }
      Action    = "s3:GetObject"
      Resource  = "${aws_s3_bucket.main.arn}/*"
    }]
  })
}

# ---------------------------------------------------------------------------
# PublicAccessBlock
# ---------------------------------------------------------------------------
resource "aws_s3_bucket_public_access_block" "main" {
  bucket = aws_s3_bucket.main.id

  block_public_acls       = true
  ignore_public_acls      = true
  block_public_policy     = false
  restrict_public_buckets = false
}

# ---------------------------------------------------------------------------
# OwnershipControls
# ---------------------------------------------------------------------------
resource "aws_s3_bucket_ownership_controls" "main" {
  bucket = aws_s3_bucket.main.id

  rule {
    object_ownership = "BucketOwnerPreferred"
  }
}
