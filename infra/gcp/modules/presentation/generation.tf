locals {
  generation_enabled      = local.app_enabled && (var.application == null ? false : var.application.generation_config_sha256 != null)
  generation_live_enabled = local.generation_enabled && (var.application == null ? false : var.application.generation_live_enabled)
}
resource "google_project_service" "generation" {
  count              = local.generation_enabled ? 1 : 0
  project            = var.project_id
  service            = "aiplatform.googleapis.com"
  disable_on_destroy = false
}
# Provision this Google-managed identity with Service Usage before enabling the
# application. Explicit identity generation does not grant its service role.
resource "google_project_iam_member" "generation_service_agent" {
  count      = local.generation_enabled ? 1 : 0
  project    = var.project_id
  role       = "roles/aiplatform.serviceAgent"
  member     = "serviceAccount:service-${var.project_number}@gcp-sa-aiplatform.iam.gserviceaccount.com"
  depends_on = [google_project_service.generation]
}
resource "google_service_account" "trainer" {
  count        = local.generation_enabled ? 1 : 0
  project      = var.project_id
  account_id   = "${local.prefix}-trainer"
  display_name = "rangle stg bounded Vertex trainer"
  depends_on   = [google_project_service.generation]
}
resource "google_project_iam_custom_role" "generation_runtime" {
  count       = local.generation_enabled ? 1 : 0
  project     = var.project_id
  role_id     = "rangleStgGenerationRuntime"
  title       = "Submit and reconcile bounded generation jobs"
  permissions = ["aiplatform.customJobs.create", "aiplatform.customJobs.get", "aiplatform.customJobs.list", "aiplatform.customJobs.cancel"]
  depends_on  = [google_project_service.generation]
}
# CustomJob/Location resource.name is absent from the official supported IAM
# resource-attribute table. Do not pretend this is a region/job-scoped grant.
# Application policy pins Tokyo, the approved spec, labels and durable budget.
resource "google_project_iam_member" "generation_runtime" {
  count   = local.generation_enabled ? 1 : 0
  project = var.project_id
  role    = google_project_iam_custom_role.generation_runtime[0].name
  member  = "serviceAccount:${google_service_account.generation_manager[0].email}"
  condition {
    title      = "generation_before_presentation_deadline"
    expression = "request.time < timestamp('${var.deadline}')"
  }
}
resource "google_service_account_iam_member" "trainer_act_as" {
  count              = local.generation_enabled ? 1 : 0
  service_account_id = google_service_account.trainer[0].name
  role               = "roles/iam.serviceAccountUser"
  member             = "serviceAccount:${google_service_account.generation_manager[0].email}"
  condition {
    title      = "only_trainer_before_presentation_deadline"
    expression = "request.time < timestamp('${var.deadline}')"
  }
}
resource "google_project_iam_custom_role" "trainer_input" {
  count       = local.generation_enabled ? 1 : 0
  project     = var.project_id
  role_id     = "rangleStgTrainerInputGet"
  title       = "Read exact approved generation input objects"
  permissions = ["storage.objects.get"]
  depends_on  = [google_project_service.generation]
}
resource "google_storage_bucket_iam_member" "trainer_input" {
  count  = local.generation_enabled ? 1 : 0
  bucket = google_storage_bucket.private["inputs"].name
  role   = google_project_iam_custom_role.trainer_input[0].name
  member = "serviceAccount:${google_service_account.trainer[0].email}"
  condition {
    title      = "only_generation_datasets_before_deadline"
    expression = "resource.name.startsWith('projects/_/buckets/${google_storage_bucket.private["inputs"].name}/objects/datasets/') && request.time < timestamp('${var.deadline}')"
  }
}
resource "google_storage_bucket_iam_member" "trainer_output" {
  count  = local.generation_enabled ? 1 : 0
  bucket = google_storage_bucket.private["media"].name
  role   = "roles/storage.objectCreator"
  member = "serviceAccount:${google_service_account.trainer[0].email}"
  condition {
    title      = "only_generated_media_before_deadline"
    expression = "resource.name.startsWith('projects/_/buckets/${google_storage_bucket.private["media"].name}/objects/generated/') && request.time < timestamp('${var.deadline}')"
  }
}
resource "google_project_iam_custom_role" "generation_guard" {
  count       = local.generation_enabled ? 1 : 0
  project     = var.project_id
  role_id     = "rangleStgGenerationGuard"
  title       = "List and cancel owned generation jobs during shutdown"
  permissions = ["aiplatform.customJobs.list", "aiplatform.customJobs.cancel"]
  depends_on  = [google_project_service.generation]
}
# The guard must still cancel after the deadline, so this separate two-operation
# role has no time limit. Fixed regional endpoints and label/name checks are
# code boundaries, not unsupported IAM location or label conditions.
resource "google_project_iam_member" "generation_guard" {
  count   = local.generation_enabled ? 1 : 0
  project = var.project_id
  role    = google_project_iam_custom_role.generation_guard[0].name
  member  = "serviceAccount:${google_service_account.guard.email}"
}
