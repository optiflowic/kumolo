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

  enable_token_revocation = true
}

resource "aws_cognito_user_group" "admins" {
  name         = "admins"
  user_pool_id = aws_cognito_user_pool.main.id
  description  = "Administrator group"
  precedence   = 1
}

resource "aws_cognito_user_group" "editors" {
  name         = "editors"
  user_pool_id = aws_cognito_user_pool.main.id
  description  = "Editor group"
  precedence   = 10
}

resource "aws_cognito_user_in_group" "admin_in_admins" {
  user_pool_id = aws_cognito_user_pool.main.id
  group_name   = aws_cognito_user_group.admins.name
  username     = aws_cognito_user.admin.username
}
