output "s3_bucket" {
  value = aws_s3_bucket.main.id
}

output "s3_object_keys" {
  value = [
    aws_s3_object.readme.key,
    aws_s3_object.config.key,
  ]
}

output "s3_replica_bucket" {
  value = aws_s3_bucket.replica.id
}

output "s3_kms_bucket" {
  value = aws_s3_bucket.kms_encrypted.id
}

output "s3_kms_object_key" {
  value = aws_s3_object.kms_encrypted.key
}

output "dynamodb_table" {
  value = aws_dynamodb_table.users.name
}

output "dynamodb_table_arn" {
  value = aws_dynamodb_table.users.arn
}

output "dynamodb_stream_table" {
  value = aws_dynamodb_table.stream_test.name
}

output "dynamodb_stream_arn" {
  value = aws_dynamodb_table.stream_test.stream_arn
}

output "kms_key_id" {
  value = aws_kms_key.main.key_id
}

output "kms_key_arn" {
  value = aws_kms_key.main.arn
}

output "kms_alias" {
  value = aws_kms_alias.main.name
}

output "kms_disabled_key_id" {
  value = aws_kms_key.disabled.key_id
}

output "cognito_user_pool_id" {
  value = aws_cognito_user_pool.main.id
}

output "cognito_user_pool_arn" {
  value = aws_cognito_user_pool.main.arn
}

output "cognito_user_pool_client_id" {
  value = aws_cognito_user_pool_client.main.id
}

output "cognito_admin_user" {
  value = aws_cognito_user.admin.username
}

output "cognito_admin_user_status" {
  value = aws_cognito_user.admin.status
}

output "cognito_admin_user_enabled" {
  value = aws_cognito_user.admin.enabled
}

output "cognito_admin_user_given_name" {
  value = aws_cognito_user.admin.attributes["given_name"]
}

output "cognito_group_admins" {
  value = aws_cognito_user_group.admins.name
}

output "cognito_group_editors" {
  value = aws_cognito_user_group.editors.name
}

output "cognito_client_refresh_token_validity" {
  value = aws_cognito_user_pool_client.main.refresh_token_validity
}

output "cognito_client_access_token_validity" {
  value = aws_cognito_user_pool_client.main.access_token_validity
}

output "cognito_client_id_token_validity" {
  value = aws_cognito_user_pool_client.main.id_token_validity
}

output "cognito_client_token_validity_units" {
  value = aws_cognito_user_pool_client.main.token_validity_units
}

output "cognito_user_pool_tags" {
  value = aws_cognito_user_pool.main.tags_all
}

output "cognito_user_pool_account_recovery_setting" {
  value = aws_cognito_user_pool.main.account_recovery_setting
}

output "cognito_mfa_required_pool_id" {
  value = aws_cognito_user_pool.mfa_required.id
}

output "cognito_mfa_required_pool_mfa_configuration" {
  value = aws_cognito_user_pool.mfa_required.mfa_configuration
}

output "cognito_region_test_pool_id" {
  value = aws_cognito_user_pool.region_test.id
}

output "cognito_region_test_pool_arn" {
  value = aws_cognito_user_pool.region_test.arn
}
