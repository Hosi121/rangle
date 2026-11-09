# The existing generation admission ledger and Vertex cancellation guard also
# cover live jobs. This opt-in adds an identity and object-scoped grants only;
# no always-on GPU, independent budget, retry pool or worker service is created.
resource "google_service_account" "live_trainer" {
  count        = local.generation_live_enabled ? 1 : 0
  project      = var.project_id
  account_id   = "${local.prefix}-live-trainer"
  display_name = "rangle stg bounded live Vertex trainer"
  depends_on   = [google_project_service.generation]
}

resource "google_service_account_iam_member" "live_trainer_act_as" {
  count              = local.generation_live_enabled ? 1 : 0
  service_account_id = google_service_account.live_trainer[0].name
  role               = "roles/iam.serviceAccountUser"
  member             = "serviceAccount:${google_service_account.generation_manager[0].email}"
  condition {
    title      = "only_live_trainer_before_presentation_deadline"
    expression = "request.time < timestamp('${var.deadline}')"
  }
}

resource "google_storage_bucket_iam_member" "live_trainer_input" {
  count  = local.generation_live_enabled ? 1 : 0
  bucket = google_storage_bucket.private["inputs"].name
  role   = google_project_iam_custom_role.trainer_input[0].name
  member = "serviceAccount:${google_service_account.live_trainer[0].email}"
  condition {
    title      = "only_live_inputs_before_deadline"
    expression = "resource.name.startsWith('projects/_/buckets/${google_storage_bucket.private["inputs"].name}/objects/datasets/live/') && request.time < timestamp('${var.deadline}')"
  }
}

resource "google_project_iam_custom_role" "live_immutable" {
  count       = local.generation_live_enabled ? 1 : 0
  project     = var.project_id
  role_id     = "rangleStgLiveImmutable"
  title       = "Read or create immutable live models and index archives"
  permissions = ["storage.objects.get", "storage.objects.create"]
  depends_on  = [google_project_service.generation]
}

resource "google_storage_bucket_iam_member" "live_trainer_immutable" {
  for_each = local.generation_live_enabled ? {
    media   = "live/"
    catalog = "live/snapshots/"
  } : {}
  bucket = google_storage_bucket.private[each.key].name
  role   = google_project_iam_custom_role.live_immutable[0].name
  member = "serviceAccount:${google_service_account.live_trainer[0].email}"
  condition {
    title      = "only_immutable_live_objects_before_deadline"
    expression = "resource.name.startsWith('projects/_/buckets/${google_storage_bucket.private[each.key].name}/objects/${each.value}') && request.time < timestamp('${var.deadline}')"
  }
}

# Replacing object bytes requires create AND delete, even with generation CAS.
# Metadata is supplied at insert, so storage.objects.update is unnecessary.
# https://docs.cloud.google.com/storage/docs/json_api/v1/objects/insert
resource "google_project_iam_custom_role" "live_pointer" {
  count       = local.generation_live_enabled ? 1 : 0
  project     = var.project_id
  role_id     = "rangleStgLivePointer"
  title       = "Replace only bounded live metadata pointers"
  permissions = ["storage.objects.get", "storage.objects.create", "storage.objects.delete"]
  depends_on  = [google_project_service.generation]
}

resource "google_storage_bucket_iam_member" "live_trainer_pointer" {
  for_each = local.generation_live_enabled ? {
    media   = "resource.name.startsWith('projects/_/buckets/${google_storage_bucket.private["media"].name}/objects/live-index/')"
    catalog = "resource.name == 'projects/_/buckets/${google_storage_bucket.private["catalog"].name}/objects/live/index.json'"
  } : {}
  bucket = google_storage_bucket.private[each.key].name
  role   = google_project_iam_custom_role.live_pointer[0].name
  member = "serviceAccount:${google_service_account.live_trainer[0].email}"
  condition {
    title      = "only_live_metadata_pointers_before_deadline"
    expression = "${each.value} && request.time < timestamp('${var.deadline}')"
  }
}
