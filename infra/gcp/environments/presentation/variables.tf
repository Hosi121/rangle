variable "project_id" { type = string }
variable "project_number" {
  type = string
  validation {
    condition     = can(regex("^[0-9]+$", var.project_number))
    error_message = "A numeric project number is required."
  }
}
variable "region" {
  type = string
  validation {
    condition     = var.region == "asia-northeast1"
    error_message = "This bounded presentation deployment is restricted to Tokyo."
  }
}
variable "hosting_site_id" {
  type = string
  validation {
    condition     = can(regex("^[a-z0-9][a-z0-9-]{1,28}[a-z0-9]$", var.hosting_site_id))
    error_message = "Use a new dedicated Hosting site ID, 3-30 characters."
  }
}
variable "initialize_firebase" {
  description = "True only if Firebase is absent from the existing project. Adding Firebase cannot be undone."
  type        = bool
  default     = false
}
variable "deadline" {
  type    = string
  default = "2026-11-14T15:00:00Z"
  validation {
    condition     = var.deadline == "2026-11-14T15:00:00Z"
    error_message = "The authorized presentation shutdown deadline is fixed."
  }
}
variable "budget_currency" {
  description = "Exact currencyCode of the budget notifications; do not assume conversion to USD."
  type        = string
  validation {
    condition     = can(regex("^[A-Z]{3}$", var.budget_currency))
    error_message = "Provide the verified budget currency code."
  }
}
variable "budget_stop_amount" {
  description = "Early-stop threshold in budget_currency, below the authorized total USD300 after verified conversion."
  type        = number
  validation {
    condition     = var.budget_stop_amount > 0
    error_message = "Provide a positive early-stop amount with reporting-lag margin."
  }
}
variable "budget_display_name" {
  type    = string
  default = "rangle-stg-presentation-20261114"
}
variable "application" {
  description = "Null until a real catalog snapshot and OCI image have been published and verified."
  type        = object({ image_digest = string, catalog_sha256 = string })
  default     = null
  nullable    = true
  validation {
    condition     = var.application == null ? true : can(regex("^[^@[:space:]]+@sha256:[0-9a-f]{64}$", var.application.image_digest)) && can(regex("^[0-9a-f]{64}$", var.application.catalog_sha256))
    error_message = "Use an immutable image digest and catalog content SHA256."
  }
}

variable "budget" {
  description = "Optional management of our own project-scoped all-service alert budget; never changes an existing spend cap."
  type        = object({ billing_account_id = string, amount = number, start_date = string, end_date = string })
  default     = null
  nullable    = true
}

variable "create_artifact_repository" {
  description = "False preserves an existing rangle-stg Docker repository and its settings."
  type        = bool
  default     = false
}
