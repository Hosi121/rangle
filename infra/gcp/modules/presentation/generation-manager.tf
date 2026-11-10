locals {
  generation_manager_name   = "${local.prefix}-generation"
  generation_manager_origin = "https://${local.generation_manager_name}-${var.project_number}.${var.region}.run.app"
}

# The public process may invoke this bounded API but cannot edit its admission
# ledger, submit Vertex specs, or act as either trainer identity.
resource "google_service_account" "generation_manager" {
  count        = local.generation_enabled ? 1 : 0
  project      = var.project_id
  account_id   = local.generation_manager_name
  display_name = "Private bounded rangle generation controller"
  depends_on   = [google_project_service.generation]
}

resource "google_firestore_database" "generation" {
  count                             = local.generation_enabled ? 1 : 0
  project                           = var.project_id
  name                              = local.generation_manager_name
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

resource "google_firestore_backup_schedule" "generation_daily" {
  count     = local.generation_enabled ? 1 : 0
  project   = var.project_id
  database  = google_firestore_database.generation[0].name
  retention = "604800s"
  daily_recurrence {}
}

resource "google_project_iam_custom_role" "generation_data" {
  count   = local.generation_enabled ? 1 : 0
  project = var.project_id
  role_id = "rangleStgGenerationData"
  title   = "Read and update isolated generation admission and results"
  permissions = [
    "datastore.databases.get", "datastore.entities.get", "datastore.entities.list",
    "datastore.entities.create", "datastore.entities.update", "datastore.entities.delete"
  ]
  depends_on = [google_project_service.community]
}

resource "google_project_iam_member" "generation_data" {
  count   = local.generation_enabled ? 1 : 0
  project = var.project_id
  role    = google_project_iam_custom_role.generation_data[0].name
  member  = "serviceAccount:${google_service_account.generation_manager[0].email}"
  condition {
    title      = "only_generation_database_before_deadline"
    expression = "resource.name == 'projects/${var.project_id}/databases/${google_firestore_database.generation[0].name}' && request.time < timestamp('${var.deadline}')"
  }
}

resource "google_storage_bucket_iam_member" "generation_catalog" {
  count  = local.generation_enabled ? 1 : 0
  bucket = google_storage_bucket.private["catalog"].name
  role   = "roles/storage.objectViewer"
  member = "serviceAccount:${google_service_account.generation_manager[0].email}"
}

resource "google_service_account_iam_member" "generation_deployer_act_as" {
  count              = local.generation_enabled ? 1 : 0
  service_account_id = google_service_account.generation_manager[0].name
  role               = "roles/iam.serviceAccountUser"
  member             = "serviceAccount:${google_service_account.deployer.email}"
  condition {
    title      = "presentation_not_expired"
    expression = "request.time < timestamp('${var.deadline}')"
  }
}

# Network reachability is not authorization. Cloud Run IAM rejects anonymous
# requests; the application separately verifies the signed runtime identity.
# No VPC connector/NAT, new hostname, public invoker or application secret.
resource "google_cloud_run_v2_service" "generation" {
  count                = local.generation_enabled ? 1 : 0
  project              = var.project_id
  name                 = local.generation_manager_name
  location             = var.region
  deletion_protection  = false
  ingress              = "INGRESS_TRAFFIC_ALL"
  default_uri_disabled = false
  invoker_iam_disabled = false
  labels               = local.labels
  scaling {
    min_instance_count = 0
    max_instance_count = 1
  }
  template {
    service_account                  = google_service_account.generation_manager[0].email
    execution_environment            = "EXECUTION_ENVIRONMENT_GEN2"
    max_instance_request_concurrency = 20
    timeout                          = "30s"
    containers {
      image = var.application.image_digest
      args = concat([
        "-listen", "0.0.0.0:8080", "-generation-manager",
        "-catalog", "/mnt/catalog/snapshots/${var.application.catalog_sha256}.json",
        "-generation-config", "/mnt/catalog/generation/${var.application.generation_config_sha256}.json",
        "-generation-manager-audience", local.generation_manager_origin,
        "-generation-caller-service-account", google_service_account.runtime.email,
        "-generation-cleanup-url-map", "${local.prefix}-https"
      ], local.generation_sync_enabled ? ["-generation-sync-audience", local.generation_manager_origin] : [])
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
    ignore_changes = [client, client_version, template[0].containers[0].image, traffic]
    precondition {
      condition     = timecmp(timestamp(), var.deadline) < 0
      error_message = "Do not recreate generation control after the presentation deadline."
    }
  }
  depends_on = [google_project_iam_member.generation_data, google_storage_bucket_iam_member.generation_catalog, google_project_iam_member.generation_runtime, google_project_iam_member.generation_service_agent, google_service_account_iam_member.trainer_act_as, google_service_account_iam_member.live_trainer_act_as, google_storage_bucket_iam_member.generation_output_list, google_storage_bucket_iam_member.generation_output_delete, google_project_iam_member.generation_cache_cleanup]
}

resource "google_cloud_run_v2_service_iam_member" "generation_runtime_invoker" {
  count    = local.generation_enabled ? 1 : 0
  project  = var.project_id
  location = var.region
  name     = google_cloud_run_v2_service.generation[0].name
  role     = "roles/run.invoker"
  member   = "serviceAccount:${google_service_account.runtime.email}"
  condition {
    title      = "presentation_not_expired"
    expression = "request.time < timestamp('${var.deadline}')"
  }
}

resource "google_cloud_run_v2_service_iam_member" "generation_sync_invoker" {
  count    = local.generation_sync_enabled ? 1 : 0
  project  = var.project_id
  location = var.region
  name     = google_cloud_run_v2_service.generation[0].name
  role     = "roles/run.invoker"
  member   = "serviceAccount:${google_service_account.generation_sync[0].email}"
  condition {
    title      = "presentation_not_expired"
    expression = "request.time < timestamp('${var.deadline}')"
  }
}

resource "google_cloud_run_v2_service_iam_member" "generation_deployer" {
  for_each = local.generation_enabled ? toset(["roles/run.developer", "roles/run.invoker"]) : toset([])
  project  = var.project_id
  location = var.region
  name     = google_cloud_run_v2_service.generation[0].name
  role     = each.key
  member   = "serviceAccount:${google_service_account.deployer.email}"
  condition {
    title      = "presentation_not_expired"
    expression = "request.time < timestamp('${var.deadline}')"
  }
}

resource "google_cloud_run_v2_service_iam_member" "generation_guard" {
  count    = local.generation_enabled ? 1 : 0
  project  = var.project_id
  location = var.region
  name     = google_cloud_run_v2_service.generation[0].name
  role     = google_project_iam_custom_role.stop_run.name
  member   = "serviceAccount:${google_service_account.guard.email}"
}
