locals {
  community_enabled = var.application == null ? false : var.application.community_enabled
}

resource "google_project_service" "community" {
  count              = local.community_enabled ? 1 : 0
  project            = var.project_id
  service            = "firestore.googleapis.com"
  disable_on_destroy = false
}

resource "google_firestore_database" "community" {
  count                             = local.community_enabled ? 1 : 0
  project                           = var.project_id
  name                              = local.prefix
  location_id                       = var.region
  type                              = "FIRESTORE_NATIVE"
  database_edition                  = "STANDARD"
  concurrency_mode                  = "PESSIMISTIC"
  app_engine_integration_mode       = "DISABLED"
  point_in_time_recovery_enablement = "POINT_IN_TIME_RECOVERY_ENABLED"
  delete_protection_state           = "DELETE_PROTECTION_ENABLED"
  deletion_policy                   = "PREVENT"
  lifecycle { prevent_destroy = true }
  depends_on = [google_project_service.community]
}

# TTL is storage cleanup, not authentication: Go must reject expires_at <= now.
# Empty index_config exempts the expiry timestamp from single-field indexing.
resource "google_firestore_field" "sessions_expiry" {
  count      = local.community_enabled ? 1 : 0
  project    = var.project_id
  database   = google_firestore_database.community[0].name
  collection = "sessions"
  field      = "expires_at"
  ttl_config {}
  index_config {}
}

resource "google_firestore_field" "staff_sessions_expiry" {
  count      = local.community_enabled ? 1 : 0
  project    = var.project_id
  database   = google_firestore_database.community[0].name
  collection = "staff_sessions"
  field      = "expires_at"
  ttl_config {}
  index_config {}
}

# Minimum document CRUD/query/aggregation and transaction permissions from
# https://docs.cloud.google.com/firestore/native/docs/security/iam
resource "google_project_iam_custom_role" "community" {
  count   = local.community_enabled ? 1 : 0
  project = var.project_id
  role_id = "rangleStgCommunityData"
  title   = "Presentation community document access"
  permissions = [
    "datastore.databases.get",
    "datastore.entities.get",
    "datastore.entities.list",
    "datastore.entities.create",
    "datastore.entities.update",
    "datastore.entities.delete"
  ]
  depends_on = [google_project_service.community]
}

# Firestore database access uses a conditioned project allow policy.
# https://docs.cloud.google.com/firestore/native/docs/manage-databases#configure_per-database_access_permissions
resource "google_project_iam_member" "community_runtime" {
  count   = local.community_enabled ? 1 : 0
  project = var.project_id
  role    = google_project_iam_custom_role.community[0].name
  member  = "serviceAccount:${google_service_account.runtime.email}"
  condition {
    title       = "only_presentation_community_before_deadline"
    description = "Document access to the dedicated database, through the Go server only."
    expression  = "resource.name == 'projects/${var.project_id}/databases/${google_firestore_database.community[0].name}' && request.time < timestamp('${var.deadline}')"
  }
}
