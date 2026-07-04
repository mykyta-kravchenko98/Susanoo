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

# ---------------------------------------------------------------------------
# Image processing pipeline: processor -> Tesseract-Lambda -> processor.
# The batch sends a "Done" signal only ONCE (for all pages of the letter at once), rather than after every step.
# Photo: the Tesseract-Lambda cold start is hidden within the pause that is already expected
# "Processing letter..." — rather than being spread out across every "Photo added" event.
# ---------------------------------------------------------------------------

resource "aws_sqs_queue" "images_to_process_dlq" {
  name                      = "${var.project_name}-images-to-process-dlq"
  message_retention_seconds = 1209600
}

resource "aws_sqs_queue" "images_to_process" {
  name                       = "${var.project_name}-images-to-process"
  visibility_timeout_seconds = 120 # с запасом на холодный старт Container Image Lambda + обработку всех страниц
  message_retention_seconds  = 86400

  redrive_policy = jsonencode({
    deadLetterTargetArn = aws_sqs_queue.images_to_process_dlq.arn
    maxReceiveCount      = 3
  })
}

resource "aws_sqs_queue" "processed_images_dlq" {
  name                      = "${var.project_name}-processed-images-dlq"
  message_retention_seconds = 1209600
}

resource "aws_sqs_queue" "processed_images" {
  name                       = "${var.project_name}-processed-images"
  visibility_timeout_seconds = 90
  message_retention_seconds  = 86400

  redrive_policy = jsonencode({
    deadLetterTargetArn = aws_sqs_queue.processed_images_dlq.arn
    maxReceiveCount      = 3
  })
}
