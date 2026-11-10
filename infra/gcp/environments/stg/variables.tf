# Types are checked here; shared validation lives in modules/platform.
variable "terraform_service_account" {
  description = "Existing environment-specific execution identity; must match this root's GCS backend identity."
  type        = string
  validation {
    condition     = can(regex("^[a-z][a-z0-9-]+@[a-z][a-z0-9-]+\\.iam\\.gserviceaccount\\.com$", var.terraform_service_account))
    error_message = "Use an existing execution service account email without a key."
  }
}

variable "config" {
  type = object({
    project_id                = string
    project_number            = string
    region                    = string
    name_prefix               = string
    app_domain                = string
    media_domain              = string
    media_bucket_name         = string
    catalog_bucket_name       = string
    input_bucket_name         = string
    publisher_service_account = string
    media_cache_ttl_seconds   = number
  })
}

variable "application" {
  description = "Null until the verified catalog snapshot and compatible image are ready."
  type = object({
    image_digest            = string
    catalog_sha256          = string
    ui_root                 = string
    cpu                     = number
    memory                  = string
    concurrency             = number
    min_instances           = number
    max_instances           = number
    request_timeout_seconds = number
  })
  default  = null
  nullable = true
}
