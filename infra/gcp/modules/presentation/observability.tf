locals {
  log_bucket_id   = "${local.prefix}-logs"
  log_bucket_path = "projects/${var.project_id}/locations/${var.region}/buckets/${local.log_bucket_id}"
  log_view_path   = "${local.log_bucket_path}/views/_AllLogs"
  # Collect both owned Run processes only when private generation is enabled.
  # Native performance charts below remain scoped to the public UI/API service.
  run_log_service_filter = local.generation_enabled ? "resource.labels.service_name=(\"${local.prefix}\" OR \"${local.generation_manager_name}\")" : "resource.labels.service_name=\"${local.prefix}\""

  # Use owned resource names and the workers' existing service attribute. Vertex
  # job labels are not assumed to be labels on every Cloud Logging entry.
  log_sources = {
    app = join(" AND ", [
      "resource.type=\"cloud_run_revision\"",
      "resource.labels.project_id=\"${var.project_id}\"",
      "resource.labels.location=\"${var.region}\"",
      local.run_log_service_filter
    ])
    generation = "jsonPayload.service=(\"rangle-trainer\" OR \"rangle-live\")"
    delivery = join(" AND ", [
      "resource.type=\"http_load_balancer\"",
      "resource.labels.project_id=\"${var.project_id}\"",
      "resource.labels.forwarding_rule_name=(\"${local.prefix}-http\" OR \"${local.prefix}-https\")"
    ])
    operations = join(" OR ", [
      "(resource.type=\"workflows.googleapis.com/Workflow\" AND resource.labels.workflow_id=\"${local.prefix}-guard\" AND resource.labels.location=\"${var.region}\")",
      "(resource.type=\"cloud_scheduler_job\" AND resource.labels.job_id=(\"${local.prefix}-guard\" OR \"${local.prefix}-generation-sync\") AND resource.labels.location=\"${var.region}\")"
    ])
  }
  # Audit logs keep their existing required/default routing and retention.
  log_filter = "NOT logName:\"cloudaudit.googleapis.com\" AND (${join(" OR ", [for source in values(local.log_sources) : "(${source})"])})"

  log_panels = [
    {
      title  = "Warnings, errors and HTTP 5xx"
      filter = "severity>=WARNING OR jsonPayload.level=(\"WARN\" OR \"ERROR\") OR httpRequest.status>=500"
    },
    { title = local.generation_enabled ? "Cloud Run UI/API and generation manager" : "Cloud Run UI/API", filter = local.log_sources.app },
    { title = "Vertex training / LIVE worker events", filter = local.log_sources.generation },
    { title = "Load balancer / Cloud CDN", filter = local.log_sources.delivery },
    { title = "Budget guard / generation sync", filter = local.log_sources.operations }
  ]
  run_metric_filter = "resource.type=\"cloud_run_revision\" AND resource.labels.project_id=\"${var.project_id}\" AND resource.labels.location=\"${var.region}\" AND resource.labels.service_name=\"${local.prefix}\""
}

# This is a Logging bucket, not a GCS bucket. Retain diagnostics after the
# presentation guard removes the application. Do not take over shared buckets.
resource "google_logging_project_bucket_config" "app_logs" {
  project          = var.project_id
  location         = var.region
  bucket_id        = local.log_bucket_id
  description      = "rangle presentation application and operational logs"
  retention_days   = 30
  enable_analytics = false
  lifecycle { prevent_destroy = true }
  depends_on = [google_project_service.required]
}

resource "google_logging_project_sink" "app_logs" {
  project     = var.project_id
  name        = "${local.prefix}-logs"
  description = "Copy only rangle runtime logs to the 30-day bucket"
  destination = "logging.googleapis.com/${local.log_bucket_path}"
  filter      = local.log_filter
  # The API omits writerIdentity for a Logging bucket in the SAME project,
  # regardless of this provider's unique_writer_identity default. Do not pin
  # false: the provider reads it back as true and would propose updates forever.
  # No bucketWriter IAM is needed. Existing _Default routing is untouched.
  depends_on = [google_logging_project_bucket_config.app_logs]
}

locals {
  log_dashboard = {
    displayName = "rangle stg - logs and runtime"
    labels      = local.labels
    gridLayout = {
      # The API serializes this int64 as a string and adds style/targetAxis.
      # Spell out defaults to keep a refresh-only round trip free of drift.
      columns = "2"
      widgets = concat([
        {
          title = "rangle diagnostics - 30-day log retention"
          text = {
            format = "MARKDOWN"
            style  = {}
            content = join("\n\n", [
              "Runtime logs are copied to **${local.log_bucket_id}** in **${var.region}** for **30 days**. Collection starts after the sink is applied; historical logs are not backfilled.",
              "Use Logs Explorer with the same log view and filter by **jsonPayload.job_id** or **jsonPayload.session_id**. The Go submitted event links the application job ID to **jsonPayload.provider_name**.",
              "App and candidate LB backends retain 10% request sampling; CDN backend-bucket request logging is automatic. Worker panels include the existing structured service events, not every unstructured library/platform message.",
              "Native Cloud Run charts include UI, health and API traffic. Logs and metrics do not measure browser rendering latency. No SLO alerting or browser telemetry collection is configured."
            ])
          }
        },
        {
          title = "Cloud Run requests / second by response class"
          xyChart = {
            dataSets = [{
              timeSeriesQuery = {
                timeSeriesFilter = {
                  filter = "metric.type=\"run.googleapis.com/request_count\" AND ${local.run_metric_filter}"
                  aggregation = {
                    alignmentPeriod    = "60s"
                    perSeriesAligner   = "ALIGN_RATE"
                    crossSeriesReducer = "REDUCE_SUM"
                    groupByFields      = ["metric.label.response_code_class"]
                  }
                }
              }
              plotType   = "LINE"
              targetAxis = "Y1"
            }]
          }
        },
        {
          title = "Cloud Run request p95 per revision (not browser latency)"
          xyChart = {
            dataSets = [{
              timeSeriesQuery = {
                timeSeriesFilter = {
                  filter = "metric.type=\"run.googleapis.com/request_latencies\" AND ${local.run_metric_filter}"
                  aggregation = {
                    alignmentPeriod  = "60s"
                    perSeriesAligner = "ALIGN_PERCENTILE_95"
                  }
                }
              }
              plotType   = "LINE"
              targetAxis = "Y1"
            }]
          }
        }
        ], [for panel in local.log_panels : {
          title = panel.title
          logsPanel = {
            filter        = panel.filter
            resourceNames = [local.log_view_path]
          }
      }])
    }
  }
}

resource "google_monitoring_dashboard" "app_logs" {
  project        = var.project_id
  dashboard_json = jsonencode(local.log_dashboard)
  depends_on     = [google_logging_project_sink.app_logs]
}

output "observability" {
  value = {
    log_bucket       = local.log_bucket_path
    log_view         = local.log_view_path
    retention_days   = google_logging_project_bucket_config.app_logs.retention_days
    dashboard        = google_monitoring_dashboard.app_logs.id
    dashboard_url    = "https://console.cloud.google.com/monitoring/dashboards/custom/${reverse(split("/", google_monitoring_dashboard.app_logs.id))[0]}?project=${var.project_id}"
    logs_url         = "https://console.cloud.google.com/logs/query;storageScope=${urlencode("storage,${local.log_view_path}")}?project=${var.project_id}"
    collection_scope = local.log_filter
  }
}
