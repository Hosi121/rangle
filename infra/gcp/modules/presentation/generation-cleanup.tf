# Cleanup is restricted to generated outputs. Bucket listing cannot be scoped
# to an object prefix in an ordinary IAM binding; delete can and is scoped.
resource "google_project_iam_custom_role" "generation_output_list" {
  count       = local.generation_enabled ? 1 : 0
  project     = var.project_id
  role_id     = "rangleStgOutputList"
  title       = "List generated media versions for cleanup"
  permissions = ["storage.objects.list"]
}
resource "google_storage_bucket_iam_member" "generation_output_list" {
  count  = local.generation_enabled ? 1 : 0
  bucket = google_storage_bucket.private["media"].name
  role   = google_project_iam_custom_role.generation_output_list[0].name
  member = "serviceAccount:${google_service_account.generation_manager[0].email}"
  condition {
    title      = "presentation_not_expired"
    expression = "request.time < timestamp('${var.deadline}')"
  }
}
resource "google_project_iam_custom_role" "generation_output_delete" {
  count       = local.generation_enabled ? 1 : 0
  project     = var.project_id
  role_id     = "rangleStgOutputDelete"
  title       = "Delete owned generated media versions"
  permissions = ["storage.objects.delete"]
}
resource "google_storage_bucket_iam_member" "generation_output_delete" {
  count  = local.generation_enabled ? 1 : 0
  bucket = google_storage_bucket.private["media"].name
  role   = google_project_iam_custom_role.generation_output_delete[0].name
  member = "serviceAccount:${google_service_account.generation_manager[0].email}"
  condition {
    title      = "generated_outputs_before_deadline"
    expression = "(resource.name.startsWith('projects/_/buckets/${google_storage_bucket.private["media"].name}/objects/generated/') || resource.name.startsWith('projects/_/buckets/${google_storage_bucket.private["media"].name}/objects/live/') || resource.name.startsWith('projects/_/buckets/${google_storage_bucket.private["media"].name}/objects/live-index/')) && request.time < timestamp('${var.deadline}')"
  }
}
# URL maps/operations do not expose resource.name to IAM Conditions. These two
# permissions are project scoped and time bound; the app pins the URL map/host.
resource "google_project_iam_custom_role" "generation_cache_cleanup" {
  count       = local.generation_enabled ? 1 : 0
  project     = var.project_id
  role_id     = "rangleStgCacheCleanup"
  title       = "Invalidate generated media and poll completion"
  permissions = ["compute.urlMaps.invalidateCache", "compute.globalOperations.get"]
}
resource "google_project_iam_member" "generation_cache_cleanup" {
  count   = local.generation_enabled ? 1 : 0
  project = var.project_id
  role    = google_project_iam_custom_role.generation_cache_cleanup[0].name
  member  = "serviceAccount:${google_service_account.generation_manager[0].email}"
  condition {
    title      = "presentation_not_expired"
    expression = "request.time < timestamp('${var.deadline}')"
  }
}
