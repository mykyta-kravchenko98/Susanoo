resource "aws_dynamodb_table" "sessions" {
  name         = "${var.project_name}-sessions"
  billing_mode = "PAY_PER_REQUEST"
  hash_key     = "chat_id"

  attribute {
    name = "chat_id"
    type = "N"
  }

  ttl {
    attribute_name = "expires_at"
    enabled         = true
  }

  server_side_encryption {
    enabled = true
  }
}
