
terraform {
  # This module is now only being tested with Terraform 1.0.x. However, to make upgrading easier, we are setting
  # 0.12.26 as the minimum version, as that version added support for required_providers with source URLs, making it
  # forwards compatible with 1.0.x code.
  required_version = ">= 0.12.26"

  required_providers {
    google = {
      source  = "hashicorp/google"
      version = "~> 3.43.0"
    }
    google-beta = {
      source  = "hashicorp/google-beta"
      version = "~> 3.43.0"
    }
    kubernetes = {
      source  = "hashicorp/kubernetes"
      version = "~> 1.7.0"
    }
    helm = {
      source  = "hashicorp/helm"
      version = "~> 1.1.1"
    }
  }
}
