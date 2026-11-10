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
variable "base_domain" {
  description = "Purchased and confirmed DNS domain. Null prepares only the CDN origin and static IP."
  type        = string
  default     = null
  nullable    = true
  validation {
    condition     = var.base_domain == null ? true : can(regex("^([a-z0-9]([a-z0-9-]*[a-z0-9])?\\.)+[a-z]{2,}$", var.base_domain))
    error_message = "Use a lowercase DNS domain without scheme, path or wildcard."
  }
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
  type = object({
    image_digest             = string
    catalog_sha256           = string
    community_enabled        = optional(bool, false)
    generation_config_sha256 = optional(string, null)
    generation_live_enabled  = optional(bool, false)
  })
  default  = null
  nullable = true
  validation {
    condition     = var.application == null ? true : can(regex("^[^@[:space:]]+@sha256:[0-9a-f]{64}$", var.application.image_digest)) && can(regex("^[0-9a-f]{64}$", var.application.catalog_sha256))
    error_message = "Use an immutable image digest and catalog content SHA256."
  }
  validation {
    condition = var.application == null ? true : (
      var.application.generation_config_sha256 == null ? true : can(regex("^[0-9a-f]{64}$", var.application.generation_config_sha256))
    )
    error_message = "Generation config must be null or the lowercase SHA256 of a reviewed immutable private JSON file."
  }
  validation {
    condition     = var.application == null ? true : var.application.generation_config_sha256 == null || var.application.community_enabled
    error_message = "Generation requires persistent public account authentication and an isolated manager database."
  }
  validation {
    condition = var.application == null ? true : (
      !var.application.generation_live_enabled || (
        var.application.generation_config_sha256 != null && var.base_domain != null
      )
    )
    error_message = "Cloud live generation requires a reviewed generation config and a configured domain."
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

variable "generation_sync_enabled" {
  description = "Explicitly enable deadline-bound authenticated active-job polling after server/runtime review."
  type        = bool
  default     = false
}

variable "live_streaming_enabled" {
  description = "Enable the reviewed live-capable server against the existing private catalog bucket."
  type        = bool
  default     = false
  validation {
    condition     = var.application == null ? true : !var.application.generation_live_enabled || var.live_streaming_enabled
    error_message = "Cloud live generation requires live metadata enabled."
  }
}
