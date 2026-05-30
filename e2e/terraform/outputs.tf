output "s3_bucket" {
  value = aws_s3_bucket.main.id
}

output "s3_object_keys" {
  value = [
    aws_s3_object.readme.key,
    aws_s3_object.config.key,
  ]
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
