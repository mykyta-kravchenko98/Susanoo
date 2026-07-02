variable "project_name" {
  type        = string
  default     = "susanoo"
}

variable "environment" {
  description = "Env - now only prod, in future can be prod/dev"
  type        = string
  default     = "prod"
}

variable "aws_region" {
  type        = string
  default     = "eu-central-1"
}
