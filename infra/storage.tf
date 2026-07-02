data "aws_caller_identity" "current" {}

# ---------------------------------------------------------------------------
# S3 bucket - canonical storage PDF-files letters
# Key structure: {organization}/{year}/{filename}.pdf, fallback: Unsorted/...
# ---------------------------------------------------------------------------

resource "aws_s3_bucket" "documents" {
  bucket = "${var.project_name}-documents-${data.aws_caller_identity.current.account_id}"
}

resource "aws_s3_bucket_versioning" "documents" {
  bucket = aws_s3_bucket.documents.id
  versioning_configuration {
    status = "Enabled"
  }
}

resource "aws_s3_bucket_server_side_encryption_configuration" "documents" {
  bucket = aws_s3_bucket.documents.id
  rule {
    apply_server_side_encryption_by_default {
      sse_algorithm = "AES256"
    }
    bucket_key_enabled = true
  }
}

resource "aws_s3_bucket_public_access_block" "documents" {
  bucket = aws_s3_bucket.documents.id

  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}


# ---------------------------------------------------------------------------
# DynamoDB - letters metadata
# ---------------------------------------------------------------------------

resource "aws_dynamodb_table" "letters" {
  name         = "${var.project_name}-letters"
  billing_mode = "PAY_PER_REQUEST"
  hash_key     = "letter_id"

  attribute {
    name = "letter_id"
    type = "S"
  }

  # GSI for request "all leater for organization X date range"
  attribute {
    name = "organization"
    type = "S"
  }

  attribute {
    name = "received_date"
    type = "S" # ISO 8601,
  }

  global_secondary_index {
    name            = "organization-received_date-index"
    hash_key        = "organization"
    range_key       = "received_date"
    projection_type = "ALL"
  }

  # prevent unexpected delete
  point_in_time_recovery {
    enabled = true
  }

  server_side_encryption {
    enabled = true
  }
}
