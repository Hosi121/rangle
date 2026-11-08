locals {
  name = "${var.config.name_prefix}-${var.environment}"
  labels = {
    application = "rangle"
    environment = var.environment
    managed_by  = "terraform"
  }
  private_buckets = {
    catalog = var.config.catalog_bucket_name
    inputs  = var.config.input_bucket_name
  }
}

resource "google_project_service" "required" {
  for_each           = toset(["compute.googleapis.com", "iam.googleapis.com", "run.googleapis.com", "storage.googleapis.com"])
  project            = var.config.project_id
  service            = each.key
  disable_on_destroy = false
}

resource "google_storage_bucket" "media" {
  project                     = var.config.project_id
  name                        = var.config.media_bucket_name
  location                    = var.config.region
  storage_class               = "STANDARD"
  uniform_bucket_level_access = true
  public_access_prevention    = "enforced"
  force_destroy               = false
  labels                      = local.labels
  versioning { enabled = true }
  soft_delete_policy { retention_duration_seconds = 604800 }
  cors {
    origin          = ["https://${var.config.app_domain}"]
    method          = ["GET", "HEAD"]
    response_header = ["Content-Type", "Content-Length", "Content-Range", "Accept-Ranges", "ETag", "Cache-Control", "Last-Modified"]
    max_age_seconds = 3600
  }
  lifecycle { prevent_destroy = true }
  depends_on = [google_project_service.required]
}

resource "google_storage_bucket" "private" {
  for_each                    = local.private_buckets
  project                     = var.config.project_id
  name                        = each.value
  location                    = var.config.region
  storage_class               = "STANDARD"
  uniform_bucket_level_access = true
  public_access_prevention    = "enforced"
  force_destroy               = false
  labels                      = local.labels
  # Cloud Storage FUSE does not formally support Object Versioning.
  # Catalog snapshots instead use content-addressed, append-only object names.
  versioning { enabled = each.key != "catalog" }
  soft_delete_policy { retention_duration_seconds = 604800 }
  lifecycle { prevent_destroy = true }
  depends_on = [google_project_service.required]
}

resource "google_service_account" "runtime" {
  project      = var.config.project_id
  account_id   = "${local.name}-run"
  display_name = "rangle ${var.environment} runtime (catalog read only)"
  depends_on   = [google_project_service.required]
}

resource "google_storage_bucket_iam_member" "runtime_catalog" {
  bucket = google_storage_bucket.private["catalog"].name
  role   = "roles/storage.objectViewer"
  member = "serviceAccount:${google_service_account.runtime.email}"
}

resource "google_service_account_iam_member" "runtime_deployer" {
  service_account_id = google_service_account.runtime.name
  role               = "roles/iam.serviceAccountUser"
  member             = "serviceAccount:${var.deployer_service_account}"
}

# Append/read permission only. The publisher must use generation-match=0;
# Object Creator cannot replace or delete an existing immutable key.
resource "google_storage_bucket_iam_member" "publisher" {
  for_each = {
    media_create   = { bucket = google_storage_bucket.media.name, role = "roles/storage.objectCreator" }
    media_verify   = { bucket = google_storage_bucket.media.name, role = "roles/storage.objectViewer" }
    catalog_create = { bucket = google_storage_bucket.private["catalog"].name, role = "roles/storage.objectCreator" }
    catalog_verify = { bucket = google_storage_bucket.private["catalog"].name, role = "roles/storage.objectViewer" }
  }
  bucket = each.value.bucket
  role   = each.value.role
  member = "serviceAccount:${var.config.publisher_service_account}"
}
