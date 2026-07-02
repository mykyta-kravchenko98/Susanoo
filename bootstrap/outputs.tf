output "state_bucket_name" {
  description = "Name S3 bucket for main Terraform state"
  value       = aws_s3_bucket.terraform_state.id
}

output "lock_table_name" {
  description = "Name of DynamoDB table for state locking"
  value       = aws_dynamodb_table.terraform_locks.name
}

output "github_actions_role_arn" {
  description = "ARN role — for GitHub Actions workflow (permissions: id-token: write + aws-actions/configure-aws-credentials with role-to-assume)"
  value       = aws_iam_role.github_actions_terraform.arn
}

output "aws_region" {
  value = var.aws_region
}
