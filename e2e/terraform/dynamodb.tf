resource "aws_dynamodb_table" "users" {
  name         = "kumolo-tf-users"
  billing_mode = "PAY_PER_REQUEST"
  hash_key     = "user_id"
  range_key    = "created_at"

  attribute {
    name = "user_id"
    type = "S"
  }

  attribute {
    name = "created_at"
    type = "N"
  }

  attribute {
    name = "email"
    type = "S"
  }

  global_secondary_index {
    name            = "email-index"
    projection_type = "ALL"

    key_schema {
      attribute_name = "email"
      key_type       = "HASH"
    }
  }

  ttl {
    attribute_name = "expires_at"
    enabled        = true
  }

  tags = {
    Environment = "local"
    ManagedBy   = "terraform"
  }
}

resource "aws_dynamodb_table_item" "alice" {
  table_name = aws_dynamodb_table.users.name
  hash_key   = aws_dynamodb_table.users.hash_key
  range_key  = aws_dynamodb_table.users.range_key

  item = jsonencode({
    user_id    = { S = "usr-001" }
    created_at = { N = "1700000000" }
    email      = { S = "alice@example.com" }
    name       = { S = "Alice" }
  })
}
