output "documents_bucket_name" {
  description = "S3 bucket for PDF letters - required for env vars Lambda"
  value       = aws_s3_bucket.documents.id
}

output "documents_bucket_arn" {
  value = aws_s3_bucket.documents.arn
}

output "letters_table_name" {
  description = "DynamoDB table with metadata - required for env vars Lambda"
  value       = aws_dynamodb_table.letters.name
}

output "letters_table_arn" {
  value = aws_dynamodb_table.letters.arn
}
