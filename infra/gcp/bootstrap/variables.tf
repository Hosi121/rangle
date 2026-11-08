variable "project_id" {
  description = "Existing project for state buckets. Bootstrap does not create a project or billing account."
  type        = string
}

variable "region" {
  description = "Explicit location for the state buckets."
  type        = string
}

variable "bootstrap_service_account" {
  description = "Existing administration identity to impersonate for this root only. No key files."
  type        = string
  validation {
    condition     = can(regex("^[a-z][a-z0-9-]+@[a-z][a-z0-9-]+\\.iam\\.gserviceaccount\\.com$", var.bootstrap_service_account))
    error_message = "Provide an existing bootstrap service account email, without credentials or key paths."
  }
}

variable "environments" {
  description = "Existing, distinct execution identities and new, distinct state bucket names."
  type = map(object({
    state_bucket_name         = string
    terraform_service_account = string
  }))
  validation {
    condition = toset(keys(var.environments)) == toset(["stg", "main"]) && (
      length(toset([for env in var.environments : env.state_bucket_name])) == 2 &&
      length(toset([for env in var.environments : env.terraform_service_account])) == 2
      ) && alltrue([
        for env in var.environments :
        can(regex("^[a-z0-9][a-z0-9-]{1,61}[a-z0-9]$", env.state_bucket_name)) &&
        can(regex("^[a-z][a-z0-9-]+@[a-z][a-z0-9-]+\\.iam\\.gserviceaccount\\.com$", env.terraform_service_account))
    ])
    error_message = "Exactly stg and main, each with its own bucket and execution service account, are required."
  }
}
