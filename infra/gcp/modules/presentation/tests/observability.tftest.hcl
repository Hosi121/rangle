mock_provider "google" {
  mock_data "google_artifact_registry_repository" { defaults = { format = "DOCKER" } }
}
variables {
  project_id         = "rangle-example"
  project_number     = "123456789012"
  region             = "asia-northeast1"
  budget_currency    = "JPY"
  budget_stop_amount = 15000
}

run "logs_survive_the_application_without_shared_bucket_or_iam_changes" {
  command = plan
  assert {
    condition     = google_logging_project_bucket_config.app_logs.bucket_id == "rangle-stg-logs" && google_logging_project_bucket_config.app_logs.location == "asia-northeast1" && google_logging_project_bucket_config.app_logs.retention_days == 30 && !google_logging_project_bucket_config.app_logs.enable_analytics
    error_message = "Use the dedicated Tokyo bucket with 30-day retention and no analytics dependency."
  }
  assert {
    condition     = google_logging_project_sink.app_logs.destination == "logging.googleapis.com/projects/rangle-example/locations/asia-northeast1/buckets/rangle-stg-logs" && !google_logging_project_sink.app_logs.unique_writer_identity
    error_message = "The sink must stay in the same project without granting a writer service account."
  }
  assert {
    condition     = contains(keys(google_project_service.required), "logging.googleapis.com") && contains(keys(google_project_service.required), "monitoring.googleapis.com") && length(google_cloud_run_v2_service.app) == 0 && output.observability.retention_days == 30
    error_message = "Diagnostics must remain provisioned when the application is absent."
  }
  assert {
    condition     = strcontains(google_logging_project_sink.app_logs.filter, "NOT logName:\"cloudaudit.googleapis.com\"") && strcontains(google_logging_project_sink.app_logs.filter, "resource.labels.service_name=\"rangle-stg\"") && strcontains(google_logging_project_sink.app_logs.filter, "resource.labels.forwarding_rule_name=(\"rangle-stg-http\" OR \"rangle-stg-https\")") && strcontains(google_logging_project_sink.app_logs.filter, "jsonPayload.service=(\"rangle-trainer\" OR \"rangle-live\")")
    error_message = "Collect owned resource/worker logs and leave shared audit routing alone."
  }
  assert {
    condition     = length(jsondecode(google_monitoring_dashboard.app_logs.dashboard_json).gridLayout.widgets) == 8 && alltrue([for widget in jsondecode(google_monitoring_dashboard.app_logs.dashboard_json).gridLayout.widgets : widget.logsPanel.resourceNames == ["projects/rangle-example/locations/asia-northeast1/buckets/rangle-stg-logs/views/_AllLogs"] if can(widget.logsPanel)])
    error_message = "The dashboard must read the dedicated log view instead of all project logs."
  }
}

run "configured_app_keeps_its_runtime_and_rollout_contract" {
  command = plan
  variables {
    base_domain = "presentation.example.com"
    application = {
      image_digest   = "asia-northeast1-docker.pkg.dev/rangle-example/rangle-stg/server@sha256:0000000000000000000000000000000000000000000000000000000000000000"
      catalog_sha256 = "1111111111111111111111111111111111111111111111111111111111111111"
    }
  }
  assert {
    condition     = google_cloud_run_v2_service.app[0].scaling[0].max_instance_count == 1 && google_cloud_run_v2_service.app[0].template[0].containers[0].resources[0].limits == tomap({ cpu = "1", memory = "512Mi" }) && google_cloud_run_v2_service.app[0].ingress == "INGRESS_TRAFFIC_INTERNAL_LOAD_BALANCER" && google_compute_backend_service.app[0].log_config[0].sample_rate == 0.1
    error_message = "Logging must not increase app capacity, open direct ingress or change existing sampling."
  }
  assert {
    condition     = strcontains(google_workflows_workflow.guard.source_contents, "observed_cost >= 15000") && !strcontains(google_workflows_workflow.guard.source_contents, "logging.googleapis.com")
    error_message = "Keep the existing cost guard and retained diagnostics during shutdown."
  }
}
