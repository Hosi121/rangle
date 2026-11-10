# The existing bucket contains only rangle events. This narrower view exposes
# its single Vertex worker stream, without API/auth/audit logs or project-wide
# log-reader privileges. Progress API responses contain curated counters only.
resource "google_logging_log_view" "generation_progress" {
  count       = local.generation_enabled ? 1 : 0
  name        = "generation-progress"
  parent      = "projects/${var.project_id}"
  location    = var.region
  bucket      = google_logging_project_bucket_config.app_logs.bucket_id
  description = "Measured rangle training progress"
  filter      = "source(\"projects/${var.project_id}\") AND resource.type=\"ml_job\" AND log_id(\"workerpool0-0\")"
}
resource "google_logging_log_view_iam_member" "generation_progress" {
  count    = local.generation_enabled ? 1 : 0
  parent   = google_logging_log_view.generation_progress[0].parent
  location = google_logging_log_view.generation_progress[0].location
  bucket   = google_logging_log_view.generation_progress[0].bucket
  name     = google_logging_log_view.generation_progress[0].name
  role     = "roles/logging.viewAccessor"
  member   = "serviceAccount:${google_service_account.generation_manager[0].email}"
  condition {
    title      = "presentation_not_expired"
    expression = "request.time < timestamp('${var.deadline}')"
  }
}
