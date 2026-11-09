locals {
  generation_sync_enabled = var.generation_sync_enabled && local.generation_enabled
  generation_sync_uri     = "${local.generation_manager_origin}/internal/generation/sync"
  generation_sync_job     = "projects/${var.project_id}/locations/${var.region}/jobs/${local.prefix}-generation-sync"
}

# Explicitly enabled only after the new server image/audience are reviewed.
# The public application verifies this dedicated identity's signed ID token;
# this identity gains no Vertex, Firestore, IAM, or deployment permissions.
resource "google_service_account" "generation_sync" {
  count        = local.generation_sync_enabled ? 1 : 0
  project      = var.project_id
  account_id   = "${local.prefix}-generation-sync"
  display_name = "Reconcile the one active generation job"
  depends_on   = [google_project_service.required]
}
resource "google_cloud_scheduler_job" "generation_sync" {
  count            = local.generation_sync_enabled ? 1 : 0
  project          = var.project_id
  name             = "${local.prefix}-generation-sync"
  region           = var.region
  schedule         = "*/2 * * * *"
  time_zone        = "Etc/UTC"
  attempt_deadline = "30s"
  retry_config {
    retry_count        = 0
    max_retry_duration = "0s"
  }
  http_target {
    http_method = "POST"
    uri         = local.generation_sync_uri
    headers     = { "Content-Type" = "application/json" }
    body        = base64encode("{}")
    oidc_token {
      service_account_email = google_service_account.generation_sync[0].email
      audience              = local.generation_manager_origin
    }
  }
  lifecycle {
    ignore_changes = [paused]
    precondition {
      condition     = timecmp(timestamp(), var.deadline) < 0
      error_message = "Do not create generation polling after the presentation deadline."
    }
  }
  depends_on = [google_cloud_run_v2_service.generation, google_cloud_run_v2_service_iam_member.generation_sync_invoker]
}
