variable "project_id" { type = string }
variable "region" { type = string }
variable "state_bucket_name" { type = string }
provider "google" { project = var.project_id }
resource "google_storage_bucket" "state" {
  project                     = var.project_id
  name                        = var.state_bucket_name
  location                    = var.region
  uniform_bucket_level_access = true
  public_access_prevention    = "enforced"
  force_destroy               = false
  versioning { enabled = true }
  soft_delete_policy { retention_duration_seconds = 604800 }
  lifecycle { prevent_destroy = true }
}
output "backend_config" {
  value = { bucket = google_storage_bucket.state.name, prefix = "rangle/presentation-stg" }
}
