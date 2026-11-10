variable "environment" {
  type = string
  validation {
    condition     = contains(["stg", "main"], var.environment)
    error_message = "Use a separate stg or main root."
  }
}

variable "deployer_service_account" {
  description = "Existing environment execution identity, granted actAs only on this runtime service account."
  type        = string
  validation {
    condition     = can(regex("^[a-z][a-z0-9-]+@[a-z][a-z0-9-]+\\.iam\\.gserviceaccount\\.com$", var.deployer_service_account))
    error_message = "Provide the existing environment execution service account email."
  }
}

variable "config" {
  description = "Environment-specific, newly created infrastructure. No existing buckets are imported."
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
  validation {
    condition     = can(regex("^[a-z][a-z0-9-]{4,28}[a-z0-9]$", var.config.project_id)) && can(regex("^[0-9]+$", var.config.project_number))
    error_message = "Provide the intended project ID and numeric project number."
  }
  validation {
    condition     = can(regex("^[a-z]+-[a-z0-9]+[0-9]$", var.config.region)) && can(regex("^[a-z][a-z0-9-]{1,19}$", var.config.name_prefix))
    error_message = "Provide an explicit GCP region and a 2-20 character lowercase name prefix."
  }
  validation {
    condition = var.config.app_domain != var.config.media_domain && alltrue([
      for host in [var.config.app_domain, var.config.media_domain] :
      can(regex("^([a-z0-9]([a-z0-9-]*[a-z0-9])?\\.)+[a-z]{2,}$", host))
    ])
    error_message = "App and media must use distinct DNS hostnames, without scheme or path."
  }
  validation {
    condition = length(toset([var.config.media_bucket_name, var.config.catalog_bucket_name, var.config.input_bucket_name])) == 3 && alltrue([
      for name in [var.config.media_bucket_name, var.config.catalog_bucket_name, var.config.input_bucket_name] :
      can(regex("^[a-z0-9][a-z0-9-]{1,61}[a-z0-9]$", name))
    ])
    error_message = "Media, catalog and inputs require three distinct new bucket names."
  }
  validation {
    condition     = can(regex("^[a-z][a-z0-9-]+@[a-z][a-z0-9-]+\\.iam\\.gserviceaccount\\.com$", var.config.publisher_service_account))
    error_message = "Provide the existing publisher service account email; no key is used."
  }
  validation {
    condition     = var.config.media_cache_ttl_seconds >= 1 && var.config.media_cache_ttl_seconds <= 31536000 && floor(var.config.media_cache_ttl_seconds) == var.config.media_cache_ttl_seconds
    error_message = "Choose an explicit integral CDN TTL between 1 second and 1 year."
  }
}

variable "application" {
  description = "Null creates storage/media edge first. Enable only after a verified catalog snapshot and compatible image exist."
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
  validation {
    condition     = var.application == null ? true : can(regex("^[^@[:space:]]+@sha256:[0-9a-f]{64}$", var.application.image_digest)) && can(regex("^[0-9a-f]{64}$", var.application.catalog_sha256))
    error_message = "Use an immutable OCI sha256 digest and a lowercase catalog content SHA256."
  }
  validation {
    condition = var.application == null ? true : (
      can(regex("^/[a-zA-Z0-9/_-]+$", var.application.ui_root)) && !strcontains(var.application.ui_root, "..") &&
      contains([1, 2, 4, 6, 8], var.application.cpu) &&
      can(regex("^[1-9][0-9]*(Mi|Gi)$", var.application.memory)) &&
      var.application.concurrency >= 1 && var.application.concurrency <= 1000 && floor(var.application.concurrency) == var.application.concurrency &&
      var.application.min_instances >= 0 && floor(var.application.min_instances) == var.application.min_instances &&
      var.application.max_instances >= 1 && var.application.max_instances >= var.application.min_instances && floor(var.application.max_instances) == var.application.max_instances &&
      var.application.request_timeout_seconds >= 1 && var.application.request_timeout_seconds <= 3600 && floor(var.application.request_timeout_seconds) == var.application.request_timeout_seconds
    )
    error_message = "Set an absolute image UI directory, supported CPU, memory, integral concurrency/min/max/timeout, and a finite max >= min. Validate CPU/memory combinations and quota before deployment."
  }
}
