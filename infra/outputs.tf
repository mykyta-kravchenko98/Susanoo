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

output "webhook_url" {
  value       = "${aws_apigatewayv2_api.telegram_webhook.api_endpoint}/webhook"
}

output "sqs_queue_url" {
  value = aws_sqs_queue.updates.url
}

output "sessions_table_name" {
  value = aws_dynamodb_table.sessions.name
}

output "telegram_bot_token_secret_arn" {
  value = aws_secretsmanager_secret.telegram_bot_token.arn
}

output "anthropic_api_key_secret_arn" {
  value = aws_secretsmanager_secret.anthropic_api_key.arn
}
