locals {
  prefix       = "rangle-stg"
  app_enabled  = var.application != null && var.base_domain != null
  app_host     = var.base_domain == null ? null : "app.${var.base_domain}"
  media_host   = var.base_domain == null ? null : "media.${var.base_domain}"
  app_origin   = var.base_domain == null ? null : "https://${local.app_host}"
  media_origin = var.base_domain == null ? null : "https://${local.media_host}"
  service_name = "projects/${var.project_id}/locations/${var.region}/services/${local.prefix}"
  job_name     = "projects/${var.project_id}/locations/${var.region}/jobs/${local.prefix}-guard"
  labels       = { application = "rangle", environment = "presentation-stg" }
  buckets = {
    catalog = "${local.prefix}-${var.project_number}-catalog"
    inputs  = "${local.prefix}-${var.project_number}-inputs"
    media   = "${local.prefix}-${var.project_number}-media"
  }
}

resource "google_project_service" "required" {
  for_each = toset([
    "artifactregistry.googleapis.com", "run.googleapis.com", "storage.googleapis.com",
    "iam.googleapis.com", "iamcredentials.googleapis.com", "sts.googleapis.com",
    "compute.googleapis.com", "pubsub.googleapis.com",
    "logging.googleapis.com", "monitoring.googleapis.com",
    "workflows.googleapis.com", "workflowexecutions.googleapis.com", "cloudscheduler.googleapis.com", "billingbudgets.googleapis.com"
  ])
  project            = var.project_id
  service            = each.key
  disable_on_destroy = false
}

resource "google_storage_bucket" "private" {
  for_each                    = local.buckets
  project                     = var.project_id
  name                        = each.value
  location                    = var.region
  storage_class               = "STANDARD"
  uniform_bucket_level_access = true
  public_access_prevention    = "enforced"
  force_destroy               = false
  labels                      = local.labels
  # Catalog is mounted through FUSE, which does not support Object Versioning.
  versioning { enabled = each.key != "catalog" }
  soft_delete_policy { retention_duration_seconds = 604800 }
  dynamic "cors" {
    for_each = each.key == "media" ? [1] : []
    content {
      origin          = ["*"]
      method          = ["GET", "HEAD"]
      response_header = ["Content-Type", "Content-Length", "Content-Range", "Accept-Ranges", "ETag", "Cache-Control", "Last-Modified", "If-None-Match", "Range"]
      max_age_seconds = 3600
    }
  }
  lifecycle { prevent_destroy = true }
  depends_on = [google_project_service.required]
}

data "google_artifact_registry_repository" "existing" {
  count         = var.create_artifact_repository ? 0 : 1
  project       = var.project_id
  location      = var.region
  repository_id = local.prefix
  lifecycle {
    postcondition {
      condition     = self.format == "DOCKER"
      error_message = "The existing presentation repository must be Docker format."
    }
  }
}
resource "google_artifact_registry_repository" "images" {
  count         = var.create_artifact_repository ? 1 : 0
  project       = var.project_id
  location      = var.region
  repository_id = local.prefix
  format        = "DOCKER"
  description   = "Dedicated presentation image artifacts"
  labels        = local.labels
  docker_config { immutable_tags = true }
  depends_on = [google_project_service.required]
}

resource "google_service_account" "runtime" {
  project      = var.project_id
  account_id   = "${local.prefix}-runtime"
  display_name = "${local.prefix}-runtime"
  depends_on   = [google_project_service.required]
}
resource "google_service_account" "deployer" {
  project      = var.project_id
  account_id   = "${local.prefix}-deploy"
  display_name = "${local.prefix}-deploy"
  # A guard-triggered disable must survive later refresh/apply.
  lifecycle { ignore_changes = [disabled] }
  depends_on = [google_project_service.required]
}
resource "google_storage_bucket_iam_member" "catalog_reader" {
  bucket = google_storage_bucket.private["catalog"].name
  role   = "roles/storage.objectViewer"
  member = "serviceAccount:${google_service_account.runtime.email}"
}
resource "google_artifact_registry_repository_iam_member" "image_writer" {
  project    = var.project_id
  location   = var.region
  repository = local.prefix
  role       = "roles/artifactregistry.writer"
  member     = "serviceAccount:${google_service_account.deployer.email}"
  # Repository IAM rejected conditional policies in the live foundation apply.
  # Keep the writer repository-scoped; WIF/SA expiry and the guard disable the
  # deployment identity rather than widening this grant to project scope.
}
resource "google_service_account_iam_member" "runtime_act_as" {
  service_account_id = google_service_account.runtime.name
  role               = "roles/iam.serviceAccountUser"
  member             = "serviceAccount:${google_service_account.deployer.email}"
  condition {
    title      = "presentation_not_expired"
    expression = "request.time < timestamp('${var.deadline}')"
  }
}

# Keep the URI available to Google-managed callers; ingress rejects public
# run.app requests, including revision-tag URLs. CI probes through the LB.
resource "google_cloud_run_v2_service" "app" {
  count                = local.app_enabled ? 1 : 0
  project              = var.project_id
  name                 = local.prefix
  location             = var.region
  deletion_protection  = false # The scheduled guard deletes this owned service.
  ingress              = "INGRESS_TRAFFIC_INTERNAL_LOAD_BALANCER"
  default_uri_disabled = false
  invoker_iam_disabled = true
  labels               = local.labels
  scaling {
    min_instance_count = 0
    max_instance_count = 2
  }
  template {
    service_account       = google_service_account.runtime.email
    execution_environment = "EXECUTION_ENVIRONMENT_GEN2"
    # Match the API's eight in-process slots while bounded autoscaling is tried.
    max_instance_request_concurrency = 8
    # Also bound zero-traffic tagged candidates, which do not share service max.
    scaling {
      min_instance_count = 0
      max_instance_count = 2
    }
    timeout = "30s"
    containers {
      image = var.application.image_digest
      args = concat([
        "-listen", "0.0.0.0:8080", "-ui-root", "/app/web",
        "-catalog", "/mnt/catalog/snapshots/${var.application.catalog_sha256}.json",
        "-asset-origin", local.media_origin
        ], var.live_streaming_enabled ? [
        "-live-index", "gs://${google_storage_bucket.private["catalog"].name}/live/index.json"
        ] : [], local.community_enabled ? [
        "-firestore-project", var.project_id,
        "-firestore-database", google_firestore_database.community[0].name
        ] : [], local.generation_enabled ? [
        "-generation-manager-url", local.generation_manager_origin
      ] : [])
      ports { container_port = 8080 }
      resources {
        limits            = { cpu = "1", memory = "512Mi" }
        cpu_idle          = true
        startup_cpu_boost = false
      }
      volume_mounts {
        name       = "catalog"
        mount_path = "/mnt/catalog"
      }
      startup_probe {
        period_seconds    = 5
        timeout_seconds   = 2
        failure_threshold = 20
        http_get { path = "/healthz" }
      }
    }
    volumes {
      name = "catalog"
      gcs {
        bucket    = google_storage_bucket.private["catalog"].name
        read_only = true
      }
    }
  }
  traffic {
    type    = "TRAFFIC_TARGET_ALLOCATION_TYPE_LATEST"
    percent = 100
  }
  lifecycle {
    # gcloud owns release metadata, image, and traffic. Keep the template itself
    # managed here so catalog, mount, and runtime changes still create revisions.
    ignore_changes = [client, client_version, template[0].containers[0].image, traffic]
    precondition {
      condition     = timecmp(timestamp(), var.deadline) < 0
      error_message = "The presentation deadline has passed; do not recreate the stopped service."
    }
  }
  depends_on = [google_storage_bucket_iam_member.catalog_reader, google_cloud_scheduler_job.guard, google_project_iam_member.community_runtime, google_cloud_run_v2_service.generation, google_cloud_run_v2_service_iam_member.generation_runtime_invoker, google_storage_bucket_iam_member.trainer_input, google_storage_bucket_iam_member.trainer_output, google_storage_bucket_iam_member.live_trainer_input, google_storage_bucket_iam_member.live_trainer_immutable, google_storage_bucket_iam_member.live_trainer_pointer]
}
resource "google_cloud_run_v2_service_iam_member" "deployer" {
  count    = local.app_enabled ? 1 : 0
  project  = var.project_id
  location = var.region
  name     = google_cloud_run_v2_service.app[0].name
  role     = "roles/run.developer"
  member   = "serviceAccount:${google_service_account.deployer.email}"
  condition {
    title      = "presentation_not_expired"
    expression = "request.time < timestamp('${var.deadline}')"
  }
}
