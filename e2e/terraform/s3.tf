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

resource "aws_s3_bucket_versioning" "objectlock" {
  bucket = aws_s3_bucket.objectlock.id

  versioning_configuration {
    status = "Enabled"
  }
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
