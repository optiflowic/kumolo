terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
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
    s3       = "http://localhost:5566"
    dynamodb = "http://localhost:5566"
    sts      = "http://localhost:5566"
  }
}
