data "archive_file" "lambda_placeholder" {
  type        = "zip"
  source_dir  = "${path.module}/lambda_placeholder"
  output_path = "${path.module}/lambda_placeholder.zip"
}

# ---------------------------------------------------------------------------
# Webhook receiver is a thin layer: it receives Telegram updates and puts them into SQS.
# instantly responds with 200 OK. It does NOT make calls to the Telegram API or the LLM -
# this is important in order to stay within Telegram's webhook timeout.
# ---------------------------------------------------------------------------

resource "aws_iam_role" "webhook_receiver" {
  name = "${var.project_name}-webhook-receiver"
  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { Service = "lambda.amazonaws.com" }
      Action    = "sts:AssumeRole"
    }]
  })
}

resource "aws_iam_role_policy" "webhook_receiver" {
  name = "${var.project_name}-webhook-receiver-policy"
  role = aws_iam_role.webhook_receiver.id
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect   = "Allow"
        Action   = ["sqs:SendMessage"]
        Resource = aws_sqs_queue.updates.arn
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

resource "aws_lambda_function" "webhook_receiver" {
  function_name = "${var.project_name}-webhook-receiver"
  role          = aws_iam_role.webhook_receiver.arn

  filename         = data.archive_file.lambda_placeholder.output_path
  source_code_hash = data.archive_file.lambda_placeholder.output_base64sha256

  handler       = "bootstrap"
  runtime       = "provided.al2023"
  architectures = ["arm64"]

  timeout     = 10
  memory_size = 128

  environment {
    variables = {
      SQS_QUEUE_URL = aws_sqs_queue.updates.url
    }
  }

  lifecycle {
    # The actual code is deployed via GitHub Actions (aws lambda update-function-code),
    # not with terraform apply - dont allow Terraform recreate actual binary file
    # with a placeholder during every infrastructure plan/apply.
    ignore_changes = [filename, source_code_hash]
  }
}

resource "aws_cloudwatch_log_group" "webhook_receiver" {
  name              = "/aws/lambda/${aws_lambda_function.webhook_receiver.function_name}"
  retention_in_days = 30
}

# ---------------------------------------------------------------------------
# Processor - all the business logic: session state, downloading photos from Telegram,
# PDF assembly, Vision LLM invocation, writing to S3/DynamoDB, EventBridge reminders
# ---------------------------------------------------------------------------

resource "aws_iam_role" "processor" {
  name = "${var.project_name}-processor"
  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { Service = "lambda.amazonaws.com" }
      Action    = "sts:AssumeRole"
    }]
  })
}

resource "aws_iam_role_policy" "processor" {
  name = "${var.project_name}-processor-policy"
  role = aws_iam_role.processor.id
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
        Resource = aws_sqs_queue.updates.arn
      },
      {
        Effect   = "Allow"
        Action   = ["s3:PutObject", "s3:GetObject", "s3:DeleteObject"]
        Resource = "${aws_s3_bucket.documents.arn}/*"
      },
      {
        Effect = "Allow"
        Action = [
          "dynamodb:PutItem",
          "dynamodb:GetItem",
          "dynamodb:UpdateItem",
          "dynamodb:DeleteItem",
          "dynamodb:Query",
        ]
        Resource = [
          aws_dynamodb_table.letters.arn,
          "${aws_dynamodb_table.letters.arn}/index/*",
          aws_dynamodb_table.sessions.arn,
        ]
      },
      {
        Effect   = "Allow"
        Action   = ["secretsmanager:GetSecretValue"]
        Resource = [
          aws_secretsmanager_secret.telegram_bot_token.arn,
          aws_secretsmanager_secret.anthropic_api_key.arn,
        ]
      },
      {
        Effect   = "Allow"
        Action   = ["events:PutEvents"]
        Resource = "arn:aws:events:${var.aws_region}:${data.aws_caller_identity.current.account_id}:event-bus/default"
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

resource "aws_lambda_function" "processor" {
  function_name = "${var.project_name}-processor"
  role          = aws_iam_role.processor.arn

  filename         = data.archive_file.lambda_placeholder.output_path
  source_code_hash = data.archive_file.lambda_placeholder.output_base64sha256

  handler       = "bootstrap"
  runtime       = "provided.al2023"
  architectures = ["arm64"]

  timeout     = 60
  memory_size = 512

  environment {
    variables = {
      DOCUMENTS_BUCKET      = aws_s3_bucket.documents.id
      LETTERS_TABLE         = aws_dynamodb_table.letters.name
      SESSIONS_TABLE        = aws_dynamodb_table.sessions.name
      TELEGRAM_TOKEN_SECRET = aws_secretsmanager_secret.telegram_bot_token.name
      ANTHROPIC_KEY_SECRET  = aws_secretsmanager_secret.anthropic_api_key.name
      TRANSLATE_TARGET_LANG = "ru"
    }
  }

  lifecycle {
    ignore_changes = [filename, source_code_hash]
  }
}

resource "aws_cloudwatch_log_group" "processor" {
  name              = "/aws/lambda/${aws_lambda_function.processor.function_name}"
  retention_in_days = 30
}

resource "aws_lambda_event_source_mapping" "processor_sqs" {
  event_source_arn = aws_sqs_queue.updates.arn
  function_name    = aws_lambda_function.processor.arn
  batch_size       = 1
}

# ---------------------------------------------------------------------------
# API Gateway HTTP API - receives webhook from Telegram
# ---------------------------------------------------------------------------

resource "aws_apigatewayv2_api" "telegram_webhook" {
  name          = "${var.project_name}-telegram-webhook"
  protocol_type = "HTTP"
}

resource "aws_apigatewayv2_integration" "webhook_receiver" {
  api_id                 = aws_apigatewayv2_api.telegram_webhook.id
  integration_type       = "AWS_PROXY"
  integration_uri        = aws_lambda_function.webhook_receiver.invoke_arn
  payload_format_version = "2.0"
}

resource "aws_apigatewayv2_route" "webhook" {
  api_id    = aws_apigatewayv2_api.telegram_webhook.id
  route_key = "POST /webhook"
  target    = "integrations/${aws_apigatewayv2_integration.webhook_receiver.id}"
}

resource "aws_apigatewayv2_stage" "default" {
  api_id      = aws_apigatewayv2_api.telegram_webhook.id
  name        = "$default"
  auto_deploy = true
}

resource "aws_lambda_permission" "apigateway_invoke" {
  statement_id  = "AllowAPIGatewayInvoke"
  action        = "lambda:InvokeFunction"
  function_name = aws_lambda_function.webhook_receiver.function_name
  principal     = "apigateway.amazonaws.com"
  source_arn    = "${aws_apigatewayv2_api.telegram_webhook.execution_arn}/*/*"
}
