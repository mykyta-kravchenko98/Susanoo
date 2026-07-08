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

resource "aws_s3_bucket_lifecycle_configuration" "documents" {
  bucket = aws_s3_bucket.documents.id

  rule {
    id     = "expire-raw-photos"
    status = "Enabled"

    filter {
      prefix = "raw/"
    }

    expiration {
      days = 7
    }

    noncurrent_version_expiration {
      noncurrent_days = 7
    }
  }

  rule {
    id     = "expire-processed-photos"
    status = "Enabled"

    # processed/ — the output of Tesseract-Lambda (rotation + downsampling); it is needed only
    # until the PDF is assembled within the processor. After that, it serves merely
    # as a safeguard—like raw/—rather than as permanent storage.
    filter {
      prefix = "processed/"
    }

    expiration {
      days = 7
    }

    noncurrent_version_expiration {
      noncurrent_days = 7
    }
  }

  rule {
    id     = "expire-unsorted-pdfs"
    status = "Enabled"

    filter {
      prefix = "Unsorted/"
    }

    expiration {
      days = 7
    }

    noncurrent_version_expiration {
      noncurrent_days = 7
    }
  }

  rule {
    id     = "expire-pending-deletion-pdfs"
    status = "Enabled"

    filter {
      prefix = "PendingDeletion/"
    }

    expiration {
      days = 30
    }

    noncurrent_version_expiration {
      noncurrent_days = 30
    }
  }
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

  attribute {
    name = "chat_id"
    type = "N"
  }

  global_secondary_index {
    name            = "chat_id-received_date-index"
    hash_key        = "chat_id"
    range_key       = "received_date"
    projection_type = "ALL"
  }

  # Used for soft-deleted letters: on delete, the app sets status=pending_deletion
  # and expires_at = now + 30 days (mirroring the PDF's PendingDeletion/ S3
  # lifecycle rule above), so the DynamoDB item is cleaned up automatically once
  # the grace period ends. Note DynamoDB TTL deletion is a background process
  # that isn't instant (AWS documents up to ~48h after expiry) and, critically,
  # it does NOT filter query results before that - application code must filter
  # on status/deleted_at itself, not rely on TTL for visibility.
  ttl {
    attribute_name = "expires_at"
    enabled        = true
  }

  # prevent unexpected delete
  point_in_time_recovery {
    enabled = true
  }

  server_side_encryption {
    enabled = true
  }
}