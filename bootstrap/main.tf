data "aws_caller_identity" "current" {}

# ---------------------------------------------------------------------------
# S3 bucket for Terraform state
# ---------------------------------------------------------------------------

resource "aws_s3_bucket" "terraform_state" {
  bucket = "${var.project_name}-tfstate-${data.aws_caller_identity.current.account_id}"

  # terraform destroy prevent
  lifecycle {
    prevent_destroy = true
  }
}

resource "aws_s3_bucket_versioning" "terraform_state" {
  bucket = aws_s3_bucket.terraform_state.id
  versioning_configuration {
    status = "Enabled"
  }
}

resource "aws_s3_bucket_server_side_encryption_configuration" "terraform_state" {
  bucket = aws_s3_bucket.terraform_state.id
  rule {
    apply_server_side_encryption_by_default {
      sse_algorithm = "AES256"
    }
  }
}

resource "aws_s3_bucket_public_access_block" "terraform_state" {
  bucket = aws_s3_bucket.terraform_state.id

  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

# ---------------------------------------------------------------------------
# DynamoDB table for Terraform state locking
# ---------------------------------------------------------------------------

resource "aws_dynamodb_table" "terraform_locks" {
  name         = "${var.project_name}-tfstate-locks"
  billing_mode = "PAY_PER_REQUEST"
  hash_key     = "LockID"

  attribute {
    name = "LockID"
    type = "S"
  }
}

# ---------------------------------------------------------------------------
# OIDC Identity Provider для GitHub Actions
# ---------------------------------------------------------------------------

resource "aws_iam_openid_connect_provider" "github_actions" {
  url = "https://token.actions.githubusercontent.com"

  client_id_list = [
    "sts.amazonaws.com",
  ]

  # thumbprint GitHub Actions OIDC
  thumbprint_list = [
    "6938fd4d98bab03faadb97b34396831e3780aea1",
  ]
}

# ---------------------------------------------------------------------------
# IAM Role, which GitHub Actions will be assume for OIDC
# ---------------------------------------------------------------------------

data "aws_iam_policy_document" "github_actions_trust" {
  statement {
    effect  = "Allow"
    actions = ["sts:AssumeRoleWithWebIdentity"]

    principals {
      type        = "Federated"
      identifiers = [aws_iam_openid_connect_provider.github_actions.arn]
    }

    condition {
      test     = "StringEquals"
      variable = "token.actions.githubusercontent.com:aud"
      values   = ["sts.amazonaws.com"]
    }

    # format subject: repo:OWNER/REPO:ref:refs/heads/BRANCH
    condition {
      test     = "StringLike"
      variable = "token.actions.githubusercontent.com:sub"
      values = concat(
        [
          for branch in var.github_allowed_branches :
          "repo:${var.github_org}/${var.github_repo}:ref:refs/heads/${branch}"
        ],
        ["repo:${var.github_org}/${var.github_repo}:pull_request"]
      )
    }
  }
}

resource "aws_iam_role" "github_actions_terraform" {
  name               = "${var.project_name}-github-actions"
  assume_role_policy = data.aws_iam_policy_document.github_actions_trust.json

  max_session_duration = 3600
}

# ---------------------------------------------------------------------------
# IAM Policy
# ---------------------------------------------------------------------------

data "aws_iam_policy_document" "terraform_permissions" {
  # Terraform state backend
  statement {
    sid    = "TerraformStateAccess"
    effect = "Allow"
    actions = [
      "s3:GetObject",
      "s3:PutObject",
      "s3:ListBucket",
    ]
    resources = [
      aws_s3_bucket.terraform_state.arn,
      "${aws_s3_bucket.terraform_state.arn}/*",
    ]
  }

  statement {
    sid    = "TerraformStateLocking"
    effect = "Allow"
    actions = [
      "dynamodb:GetItem",
      "dynamodb:PutItem",
      "dynamodb:DeleteItem",
    ]
    resources = [aws_dynamodb_table.terraform_locks.arn]
  }

  # Main Services MVP
  statement {
    sid    = "CoreServices"
    effect = "Allow"
    actions = [
      "s3:*",
      "dynamodb:*",
      "lambda:*",
      "apigateway:*",
      "events:*",     # EventBridge
      "logs:*",       # CloudWatch Logs for Lambda
      "sqs:*",        # Phase 2 — OneDrive replication
      "kms:*",        # Phase 3 — e-sign
      "secretsmanager:*", # Phase 2 — OAuth tokens
      "ecr:*",        # Tesseract Container Image Lambda
      "scheduler:*",
    ]
    resources = ["*"]
  }

  statement {
    sid    = "IAMForLambdaRoles"
    effect = "Allow"
    actions = [
      "iam:CreateRole",
      "iam:DeleteRole",
      "iam:GetRole",
      "iam:PassRole",
      "iam:AttachRolePolicy",
      "iam:DetachRolePolicy",
      "iam:PutRolePolicy",
      "iam:DeleteRolePolicy",
      "iam:GetRolePolicy",
      "iam:ListRolePolicies",
      "iam:ListAttachedRolePolicies",
      "iam:TagRole",
      "iam:CreatePolicy",
      "iam:DeletePolicy",
      "iam:GetPolicy",
      "iam:GetPolicyVersion",
      "iam:ListPolicyVersions",
      "iam:CreatePolicyVersion",
      "iam:DeletePolicyVersion",
    ]
    resources = [
      "arn:aws:iam::${data.aws_caller_identity.current.account_id}:role/${var.project_name}-*",
      "arn:aws:iam::${data.aws_caller_identity.current.account_id}:policy/${var.project_name}-*",
    ]
  }
}

resource "aws_iam_policy" "terraform_permissions" {
  name   = "${var.project_name}-terraform-permissions"
  policy = data.aws_iam_policy_document.terraform_permissions.json
}

resource "aws_iam_role_policy_attachment" "github_actions_terraform" {
  role       = aws_iam_role.github_actions_terraform.name
  policy_arn = aws_iam_policy.terraform_permissions.arn
}
