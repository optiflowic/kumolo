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

output "cognito_group_admins" {
  value = aws_cognito_user_group.admins.name
}

output "cognito_group_editors" {
  value = aws_cognito_user_group.editors.name
}
