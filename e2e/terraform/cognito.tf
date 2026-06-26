resource "aws_cognito_user_pool" "main" {
  name = "kumolo-tf-pool"

  mfa_configuration = "OFF"

  password_policy {
    minimum_length    = 8
    require_lowercase = false
    require_numbers   = false
    require_symbols   = false
    require_uppercase = false
  }

  tags = {
    Environment = "local"
    ManagedBy   = "terraform"
  }
}

resource "aws_cognito_user" "admin" {
  user_pool_id = aws_cognito_user_pool.main.id
  username     = "tf-admin@example.com"

  temporary_password = "TempPass1!"
  message_action     = "SUPPRESS"

  attributes = {
    email = "tf-admin@example.com"
  }
}

resource "aws_cognito_user_pool_client" "main" {
  name         = "kumolo-tf-client"
  user_pool_id = aws_cognito_user_pool.main.id

  explicit_auth_flows = [
    "ALLOW_USER_PASSWORD_AUTH",
    "ALLOW_REFRESH_TOKEN_AUTH",
  ]

  refresh_token_validity = 30
  access_token_validity  = 1
}
