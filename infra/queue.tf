resource "aws_sqs_queue" "updates_dlq" {
  name                      = "${var.project_name}-updates-dlq"
  message_retention_seconds = 1209600 # 14 days
}

resource "aws_sqs_queue" "updates" {
  name                       = "${var.project_name}-updates"
  visibility_timeout_seconds = 90
  message_retention_seconds  = 86400

  redrive_policy = jsonencode({
    deadLetterTargetArn = aws_sqs_queue.updates_dlq.arn
    maxReceiveCount      = 3
  })
}
