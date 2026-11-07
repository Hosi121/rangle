resource "google_cloud_run_v2_service" "app" {
  count                = var.application == null ? 0 : 1
  project              = var.config.project_id
  name                 = local.name
  location             = var.config.region
  deletion_protection  = true
  ingress              = "INGRESS_TRAFFIC_INTERNAL_LOAD_BALANCER"
  default_uri_disabled = true
  invoker_iam_disabled = true
  labels               = local.labels

  scaling {
    min_instance_count = var.application.min_instances
    max_instance_count = var.application.max_instances
  }
  template {
    service_account                  = google_service_account.runtime.email
    execution_environment            = "EXECUTION_ENVIRONMENT_GEN2"
    max_instance_request_concurrency = var.application.concurrency
    timeout                          = "${var.application.request_timeout_seconds}s"
    containers {
      image = var.application.image_digest
      # The OCI image entrypoint is rangle-server; do not use asset-root or scene-dir.
      args = [
        "-listen", "0.0.0.0:8080",
        "-ui-root", var.application.ui_root,
        "-catalog", "/mnt/catalog/snapshots/${var.application.catalog_sha256}.json",
        "-asset-origin", "https://${var.config.media_domain}"
      ]
      ports { container_port = 8080 }
      resources {
        limits            = { cpu = tostring(var.application.cpu), memory = var.application.memory }
        cpu_idle          = true
        startup_cpu_boost = false
      }
      volume_mounts {
        name       = "catalog"
        mount_path = "/mnt/catalog"
      }
      startup_probe {
        initial_delay_seconds = 0
        period_seconds        = 5
        timeout_seconds       = 2
        failure_threshold     = 20
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
  depends_on = [google_storage_bucket_iam_member.runtime_catalog, google_service_account_iam_member.runtime_deployer]
}

resource "google_compute_region_network_endpoint_group" "app" {
  count                 = var.application == null ? 0 : 1
  project               = var.config.project_id
  name                  = "${local.name}-app"
  region                = var.config.region
  network_endpoint_type = "SERVERLESS"
  cloud_run { service = google_cloud_run_v2_service.app[0].name }
}

resource "google_compute_backend_service" "app" {
  count                 = var.application == null ? 0 : 1
  project               = var.config.project_id
  name                  = "${local.name}-app"
  protocol              = "HTTP"
  load_balancing_scheme = "EXTERNAL_MANAGED"
  enable_cdn            = false
  backend { group = google_compute_region_network_endpoint_group.app[0].id }
  # Serverless NEGs do not use a health_check or configurable backend timeout.
  log_config {
    enable      = true
    sample_rate = 0.1
  }
}
