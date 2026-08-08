terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 6.0"
    }
  }
}

provider "aws" {
  region = "us-east-1"

  # Dummy credentials — kumolo does not validate them.
  access_key = "test"
  secret_key = "test"

  skip_credentials_validation = true
  skip_metadata_api_check     = true
  skip_requesting_account_id  = true

  # kumolo uses path-style URLs: http://localhost:5566/<bucket>/<key>
  s3_use_path_style = true

  endpoints {
    s3         = var.kumolo_endpoint
    dynamodb   = var.kumolo_endpoint
    sts        = var.kumolo_endpoint
    kms        = var.kumolo_endpoint
    cognitoidp = var.kumolo_endpoint
  }
}

# Second provider configured for a non-default region, used only by
# aws_cognito_user_pool.region_test (cognito.tf) to verify that kumolo derives
# the pool's region from the provider's configured region instead of
# hardcoding us-east-1 (#508).
provider "aws" {
  alias  = "ap_northeast_1"
  region = "ap-northeast-1"

  access_key = "test"
  secret_key = "test"

  skip_credentials_validation = true
  skip_metadata_api_check     = true
  skip_requesting_account_id  = true

  s3_use_path_style = true

  endpoints {
    s3         = var.kumolo_endpoint
    dynamodb   = var.kumolo_endpoint
    sts        = var.kumolo_endpoint
    kms        = var.kumolo_endpoint
    cognitoidp = var.kumolo_endpoint
  }
}
