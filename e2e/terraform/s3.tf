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
