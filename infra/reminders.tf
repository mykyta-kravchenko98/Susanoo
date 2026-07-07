# ---------------------------------------------------------------------------
# Deadline reminders. The processor creates one one-time EventBridge Scheduler
# schedule per reminder (see internal/reminders + cmd/processor/handlers.go
# handleConfirmSave), scaled by urgency. Each schedule directly invokes
# reminder-sender with the letter's details as its Input payload — there is
# no queue in this path, unlike the photo/PDF pipeline.
# ---------------------------------------------------------------------------

resource "aws_scheduler_schedule_group" "reminders" {
  name = "${var.project_name}-reminders"
}

# ---------------------------------------------------------------------------
# reminder-sender: tiny Lambda, only knows how to format and send one
# Telegram message. It never touches S3/DynamoDB/the Anthropic API, so its
# IAM role only gets the telegram secret - not the broader access the
# processor role has.
# ---------------------------------------------------------------------------

resource "aws_iam_role" "reminder_sender" {
  name = "${var.project_name}-reminder-sender"
  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { Service = "lambda.amazonaws.com" }
      Action    = "sts:AssumeRole"
    }]
  })
}

resource "aws_iam_role_policy" "reminder_sender" {
  name = "${var.project_name}-reminder-sender-policy"
  role = aws_iam_role.reminder_sender.id
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect   = "Allow"
        Action   = ["secretsmanager:GetSecretValue"]
        Resource = aws_secretsmanager_secret.telegram_bot_token.arn
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

resource "aws_lambda_function" "reminder_sender" {
  function_name = "${var.project_name}-reminder-sender"
  role          = aws_iam_role.reminder_sender.arn

  filename         = data.archive_file.lambda_placeholder.output_path
  source_code_hash = data.archive_file.lambda_placeholder.output_base64sha256

  handler       = "bootstrap"
  runtime       = "provided.al2023"
  architectures = ["arm64"]

  timeout     = 10
  memory_size = 128

  environment {
    variables = {
      TELEGRAM_TOKEN_SECRET = aws_secretsmanager_secret.telegram_bot_token.name
    }
  }

  lifecycle {
    # Deployed via GitHub Actions (aws lambda update-function-code), same as
    # webhook_receiver/processor - see the comment on that resource.
    ignore_changes = [filename, source_code_hash]
  }
}

resource "aws_cloudwatch_log_group" "reminder_sender" {
  name              = "/aws/lambda/${aws_lambda_function.reminder_sender.function_name}"
  retention_in_days = 30
}

# ---------------------------------------------------------------------------
# Execution role EventBridge Scheduler assumes to invoke reminder-sender.
# Scoped to just this one function, and the trust policy is scoped to
# schedules in our own schedule group (confused-deputy protection) - some
# other AWS account's schedule can't use this role to invoke our Lambda.
# ---------------------------------------------------------------------------

resource "aws_iam_role" "scheduler_execution" {
  name = "${var.project_name}-scheduler-execution"
  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { Service = "scheduler.amazonaws.com" }
      Action    = "sts:AssumeRole"
      Condition = {
        StringEquals = {
          "aws:SourceAccount" = data.aws_caller_identity.current.account_id
        }
        ArnLike = {
          "aws:SourceArn" = "arn:aws:scheduler:${var.aws_region}:${data.aws_caller_identity.current.account_id}:schedule/${aws_scheduler_schedule_group.reminders.name}/*"
        }
      }
    }]
  })
}

resource "aws_iam_role_policy" "scheduler_execution" {
  name = "${var.project_name}-scheduler-execution-policy"
  role = aws_iam_role.scheduler_execution.id
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect   = "Allow"
        Action   = ["lambda:InvokeFunction"]
        Resource = aws_lambda_function.reminder_sender.arn
      }
    ]
  })
}

# ---------------------------------------------------------------------------
# Grant the processor the ability to create schedules under our group, and to
# pass the scheduler execution role to EventBridge Scheduler when doing so
# (iam:PassRole is required any time a service needs to hand another AWS
# service a role to assume - without it CreateSchedule's RoleArn is rejected).
# ---------------------------------------------------------------------------

resource "aws_iam_role_policy" "processor_scheduler" {
  name = "${var.project_name}-processor-scheduler-policy"
  role = aws_iam_role.processor.id
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect = "Allow"
        Action = [
          "scheduler:CreateSchedule",
          "scheduler:GetSchedule",
          "scheduler:DeleteSchedule",
        ]
        Resource = "arn:aws:scheduler:${var.aws_region}:${data.aws_caller_identity.current.account_id}:schedule/${aws_scheduler_schedule_group.reminders.name}/*"
      },
      {
        Effect   = "Allow"
        Action   = ["iam:PassRole"]
        Resource = aws_iam_role.scheduler_execution.arn
        Condition = {
          StringEquals = {
            "iam:PassedToService" = "scheduler.amazonaws.com"
          }
        }
      }
    ]
  })
}
