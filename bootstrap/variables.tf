variable "aws_region" {
  type        = string
  default     = "eu-central-1"
}

variable "aws_profile" {
  type        = string
  default     = "telegram-archiver"
}

variable "project_name" {
  type        = string
  default     = "susanoo"
}

variable "github_org" {
  description = "GitHub username"
  type        = string
  default     = "mykyta-kravchenko98"
}

variable "github_repo" {
  type        = string
  default     = "Susanoo"
}

variable "github_allowed_branches" {
  type        = list(string)
  default     = ["main"]
}
