# ---------------------------------------------------------------------------
# ECR — repository for the Tesseract-Lambda Docker image. Container Image Lambda
# (not a zip file) — a standard AWS pattern for functions with native binaries
# (Tesseract is a C/C++ library; it cannot be compiled into a pure Go binary).
# ---------------------------------------------------------------------------

resource "aws_ecr_repository" "tesseract_processor" {
  name                 = "${var.project_name}-tesseract-processor"
  image_tag_mutability = "MUTABLE"

  image_scanning_configuration {
    scan_on_push = true
  }
}

resource "aws_ecr_lifecycle_policy" "tesseract_processor" {
  repository = aws_ecr_repository.tesseract_processor.name
  policy = jsonencode({
    rules = [{
      rulePriority = 1
      description  = "Keep only the last 5 images — save space in ECR"
      selection = {
        tagStatus   = "any"
        countType   = "imageCountMoreThan"
        countNumber = 5
      }
      action = { type = "expire" }
    }]
  })
}

# ---------------------------------------------------------------------------
# Tesseract-Lambda — single responsibility: photo orientation correction
# (Tesseract OSD) + downsampling. Operates exclusively via S3 (raw -> processed),
# is unaware of Telegram, and is not called directly by other Lambdas —
# it is triggered by a batch containing all pages of a letter at once (SQS "images-to-process"),
# sent by the processor upon completion ("Done").
# ---------------------------------------------------------------------------

resource "aws_iam_role" "tesseract_processor" {
  name = "${var.project_name}-tesseract-processor"
  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { Service = "lambda.amazonaws.com" }
      Action    = "sts:AssumeRole"
    }]
  })
}

resource "aws_iam_role_policy" "tesseract_processor" {
  name = "${var.project_name}-tesseract-processor-policy"
  role = aws_iam_role.tesseract_processor.id
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect = "Allow"
        Action = [
          "sqs:ReceiveMessage",
          "sqs:DeleteMessage",
          "sqs:GetQueueAttributes",
        ]
        Resource = aws_sqs_queue.images_to_process.arn
      },
      {
        Effect   = "Allow"
        Action   = ["sqs:SendMessage"]
        Resource = aws_sqs_queue.processed_images.arn
      },
      {
        Effect   = "Allow"
        Action   = ["s3:GetObject"]
        Resource = "${aws_s3_bucket.documents.arn}/raw/*"
      },
      {
        Effect   = "Allow"
        Action   = ["s3:PutObject"]
        Resource = "${aws_s3_bucket.documents.arn}/processed/*"
      },
      {
        Effect = "Allow"
        Action = [
          "logs:CreateLogGroup",
          "logs:CreateLogStream",
          "logs:PutLogEvents",
        ]
        Resource = "arn:aws:logs:${var.aws_region}:${data.aws_caller_identity.current.account_id}:*"
      }
    ]
  })
}

resource "aws_lambda_function" "tesseract_processor" {
  function_name = "${var.project_name}-tesseract-processor"
  role          = aws_iam_role.tesseract_processor.arn

  package_type = "Image"
  image_uri = "${aws_ecr_repository.tesseract_processor.repository_url}:latest"

  timeout     = 60
  memory_size = 1024

  environment {
    variables = {
      DOCUMENTS_BUCKET           = aws_s3_bucket.documents.id
      PROCESSED_IMAGES_QUEUE_URL = aws_sqs_queue.processed_images.url
    }
  }

  lifecycle {
    ignore_changes = [image_uri]
  }
}

resource "aws_cloudwatch_log_group" "tesseract_processor" {
  name              = "/aws/lambda/${aws_lambda_function.tesseract_processor.function_name}"
  retention_in_days = 30
}

resource "aws_lambda_event_source_mapping" "tesseract_images_to_process" {
  event_source_arn = aws_sqs_queue.images_to_process.arn
  function_name    = aws_lambda_function.tesseract_processor.arn
  batch_size       = 1

  depends_on = [aws_iam_role_policy.tesseract_processor]
}

