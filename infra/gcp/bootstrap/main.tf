provider "google" {
  project                     = var.project_id
  region                      = var.region
  impersonate_service_account = var.bootstrap_service_account
}

resource "google_project_service" "storage" {
  project            = var.project_id
  service            = "storage.googleapis.com"
  disable_on_destroy = false
}

resource "google_storage_bucket" "state" {
  for_each                    = var.environments
  project                     = var.project_id
  name                        = each.value.state_bucket_name
  location                    = var.region
  storage_class               = "STANDARD"
  uniform_bucket_level_access = true
  public_access_prevention    = "enforced"
  force_destroy               = false
  labels = {
    application = "rangle"
    environment = each.key
    purpose     = "terraform-state"
  }
  versioning { enabled = true }
  soft_delete_policy { retention_duration_seconds = 604800 }
  lifecycle_rule {
    action { type = "Delete" }
    condition {
      with_state                 = "ARCHIVED"
      days_since_noncurrent_time = 30
      num_newer_versions         = 20
    }
  }
  lifecycle { prevent_destroy = true }
  depends_on = [google_project_service.storage]
}

resource "google_storage_bucket_iam_member" "state_runner" {
  for_each = var.environments
  bucket   = google_storage_bucket.state[each.key].name
  role     = "roles/storage.objectAdmin"
  member   = "serviceAccount:${each.value.terraform_service_account}"
}

output "backend_configuration" {
  value = {
    for env, config in var.environments : env => {
      bucket                      = google_storage_bucket.state[env].name
      prefix                      = "rangle/${env}"
      impersonate_service_account = config.terraform_service_account
    }
  }
}
