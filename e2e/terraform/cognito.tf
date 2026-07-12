resource "aws_cognito_user_pool" "main" {
  name = "kumolo-tf-pool"

  mfa_configuration = "OFF"

  # Note: the AWS provider's underlying SDK only transmits require_* fields
  # when true (a false value is indistinguishable from an omitted field on
  # the wire — confirmed against real AWS behavior, not kumolo-specific), so
  # require_*=false here would have no effect. minimum_length is the only
  # PasswordPolicy field this resource can meaningfully override; the
  # RequireUppercase/Lowercase/Numbers/Symbols complexity defaults (all true)
  # still apply regardless of this block's require_* settings.
  password_policy {
    minimum_length = 12
  }

  tags = {
    Environment = "local"
    ManagedBy   = "terraform"
  }
}

resource "aws_cognito_user" "admin" {
  user_pool_id = aws_cognito_user_pool.main.id
  username     = "tf-admin@example.com"

  # Satisfies both the pool's custom 12-character minimum_length and the
  # default complexity requirements (upper/lower/number/symbol) that still
  # apply because require_*=false can't be transmitted (see password_policy
  # above). This exercises passwordPolicyFromPool's merge of an explicit
  # MinimumLength override with the inherited default complexity flags.
  temporary_password = "TempPass123!"
  message_action     = "SUPPRESS"

  enabled = var.admin_user_enabled

  attributes = {
    email      = "tf-admin@example.com"
    given_name = var.admin_given_name
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
  access_token_validity  = 10
  id_token_validity      = 30

  token_validity_units {
    access_token  = "minutes"
    id_token      = "minutes"
    refresh_token = "days"
  }

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
