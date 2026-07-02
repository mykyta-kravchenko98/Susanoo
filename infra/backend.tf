terraform {
  required_version = ">= 1.7"

  backend "s3" {
    bucket         = "susanoo-tfstate-528081867341"
    key            = "main/terraform.tfstate"
    region         = "eu-central-1"
    dynamodb_table = "susanoo-tfstate-locks"
    encrypt        = true
  }

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
    archive = {
      source  = "hashicorp/archive"
      version = "~> 2.4"
    }
  }
}

provider "aws" {
  region = "eu-central-1"

  default_tags {
    tags = {
      Project   = "susanoo"
      ManagedBy = "terraform"
    }
  }
}
