resource "google_pubsub_topic" "budget" {
  project    = var.project_id
  name       = "${local.prefix}-budget"
  labels     = local.labels
  depends_on = [google_project_service.required]
}
resource "google_pubsub_topic_iam_member" "billing_publisher" {
  project = var.project_id
  topic   = google_pubsub_topic.budget.name
  role    = "roles/pubsub.publisher"
  # Official Cloud Billing budget notification identity (not a project agent).
  # https://docs.cloud.google.com/organization-policy/restrict-domains
  member = "serviceAccount:billing-budget-alert@system.gserviceaccount.com"
}
resource "google_pubsub_subscription" "budget" {
  project                    = var.project_id
  name                       = "${local.prefix}-budget-guard"
  topic                      = google_pubsub_topic.budget.id
  ack_deadline_seconds       = 60
  message_retention_duration = "86400s"
  expiration_policy { ttl = "" }
  labels = local.labels
}
resource "google_service_account" "guard" {
  project      = var.project_id
  account_id   = "${local.prefix}-guard"
  display_name = "rangle presentation deadline and budget shutdown"
  depends_on   = [google_project_service.required]
}
resource "google_service_account" "scheduler" {
  project      = var.project_id
  account_id   = "${local.prefix}-scheduler"
  display_name = "rangle presentation guard invoker"
  depends_on   = [google_project_service.required]
}
resource "google_pubsub_subscription_iam_member" "guard" {
  project      = var.project_id
  subscription = google_pubsub_subscription.budget.name
  role         = "roles/pubsub.subscriber"
  member       = "serviceAccount:${google_service_account.guard.email}"
}
resource "google_project_iam_custom_role" "disable_deployer" {
  project     = var.project_id
  role_id     = "rangleStgDisableDeployer"
  title       = "Disable presentation deployer"
  permissions = ["iam.serviceAccounts.disable"]
}
resource "google_service_account_iam_member" "disable_deployer" {
  service_account_id = google_service_account.deployer.name
  role               = google_project_iam_custom_role.disable_deployer.name
  member             = "serviceAccount:${google_service_account.guard.email}"
}
resource "google_project_iam_custom_role" "stop_run" {
  project     = var.project_id
  role_id     = "rangleStgStopService"
  title       = "Delete and inspect presentation service"
  permissions = ["run.services.get", "run.services.delete"]
}
resource "google_cloud_run_v2_service_iam_member" "stop_run" {
  count    = local.app_enabled ? 1 : 0
  project  = var.project_id
  location = var.region
  name     = google_cloud_run_v2_service.app[0].name
  role     = google_project_iam_custom_role.stop_run.name
  member   = "serviceAccount:${google_service_account.guard.email}"
}
resource "google_project_iam_custom_role" "pause_guard" {
  project     = var.project_id
  role_id     = "rangleStgPauseGuard"
  title       = "Pause presentation budget polling after shutdown"
  permissions = ["cloudscheduler.jobs.pause"]
}
resource "google_project_iam_member" "pause_guard" {
  project = var.project_id
  role    = google_project_iam_custom_role.pause_guard.name
  member  = "serviceAccount:${google_service_account.guard.email}"
}
resource "google_storage_bucket_iam_member" "guard_receipt" {
  bucket = google_storage_bucket.private["inputs"].name
  role   = "roles/storage.objectCreator"
  member = "serviceAccount:${google_service_account.guard.email}"
}
resource "google_workflows_workflow" "guard" {
  project             = var.project_id
  name                = "${local.prefix}-guard"
  region              = var.region
  service_account     = google_service_account.guard.email
  deletion_protection = true
  call_log_level      = "LOG_ERRORS_ONLY"
  labels              = local.labels
  source_contents = templatefile("${path.module}/shutdown.yaml.tftpl", {
    deadline                   = var.deadline
    subscription               = "projects/${var.project_id}/subscriptions/${local.prefix}-budget-guard"
    budget_display_name        = var.budget_display_name
    budget_currency            = var.budget_currency
    stop_amount                = var.budget_stop_amount
    project_id                 = var.project_id
    edge_prefix                = local.prefix
    service                    = local.service_name
    operation_prefix           = "projects/${var.project_id}/locations/${var.region}/operations/"
    deployer                   = "projects/${var.project_id}/serviceAccounts/${local.prefix}-deploy@${var.project_id}.iam.gserviceaccount.com"
    receipt_bucket             = google_storage_bucket.private["inputs"].name
    scheduler                  = local.job_name
    generation_enabled         = local.generation_enabled
    generation_manager_enabled = local.generation_enabled
    generation_manager_service = "projects/${var.project_id}/locations/${var.region}/services/${local.prefix}-generation"
    generation_sync_enabled    = local.generation_sync_enabled
    generation_sync_job        = local.generation_sync_job
    backup_functions = templatefile("${path.module}/backup-shutdown.yaml.tftpl", {
      database_paths = local.backup_database_paths
    })
    generation_functions = templatefile("${path.module}/generation-shutdown.yaml.tftpl", {
      project_id     = var.project_id
      project_number = var.project_number
      region         = var.region
    })
  })
  depends_on = [google_project_service.required, google_service_account_iam_member.disable_deployer, google_project_iam_member.pause_guard, google_pubsub_subscription_iam_member.guard, google_storage_bucket_iam_member.guard_receipt, google_project_iam_member.run_operations, google_project_iam_member.inspect_run, google_project_iam_member.edge_reader, google_project_iam_member.stop_forwarding_rules, google_project_iam_member.release_address, google_project_iam_member.generation_guard, google_project_iam_member.detach_service_security, google_project_iam_member.cleanup_security, google_project_iam_member.stop_backup_schedules]
}
resource "google_project_iam_custom_role" "invoke_guard" {
  project     = var.project_id
  role_id     = "rangleStgInvokeGuard"
  title       = "Create a presentation guard execution"
  permissions = ["workflows.executions.create"]
}
# Workflows invocation permission is project-level; only the isolated Scheduler
# identity receives it, with no WIF or public invoker binding on that identity.
resource "google_project_iam_member" "scheduler" {
  project = var.project_id
  role    = google_project_iam_custom_role.invoke_guard.name
  member  = "serviceAccount:${google_service_account.scheduler.email}"
}
resource "google_cloud_scheduler_job" "guard" {
  project  = var.project_id
  name     = "${local.prefix}-guard"
  region   = var.region
  schedule = "*/15 * * * *"
  lifecycle { ignore_changes = [paused] }
  time_zone        = "Etc/UTC"
  attempt_deadline = "60s"
  retry_config {
    retry_count          = 2
    min_backoff_duration = "15s"
    max_backoff_duration = "60s"
    max_doublings        = 2
  }
  http_target {
    http_method = "POST"
    uri         = "https://workflowexecutions.googleapis.com/v1/projects/${var.project_id}/locations/${var.region}/workflows/${google_workflows_workflow.guard.name}/executions"
    headers     = { "Content-Type" = "application/json" }
    body        = base64encode(jsonencode({ argument = "{}" }))
    oauth_token {
      service_account_email = google_service_account.scheduler.email
      scope                 = "https://www.googleapis.com/auth/cloud-platform"
    }
  }
  depends_on = [google_project_iam_member.scheduler]
}

resource "google_project_iam_custom_role" "run_operations" {
  project     = var.project_id
  role_id     = "rangleStgRunOperationReader"
  title       = "Read Cloud Run deployment and shutdown operations"
  permissions = ["run.operations.get"]
  depends_on  = [google_project_service.required]
}
resource "google_project_iam_member" "run_operations" {
  for_each = { deployer = google_service_account.deployer.email, guard = google_service_account.guard.email }
  project  = var.project_id
  role     = google_project_iam_custom_role.run_operations.name
  member   = "serviceAccount:${each.value}"
  dynamic "condition" {
    for_each = each.key == "deployer" ? [1] : []
    content {
      title      = "presentation_not_expired"
      expression = "request.time < timestamp('${var.deadline}')"
    }
  }
}
resource "google_project_iam_custom_role" "inspect_run" {
  project     = var.project_id
  role_id     = "rangleStgRunServiceReader"
  title       = "Confirm owned service is absent after deletion"
  permissions = ["run.services.get"]
  depends_on  = [google_project_service.required]
}
resource "google_project_iam_member" "inspect_run" {
  project = var.project_id
  role    = google_project_iam_custom_role.inspect_run.name
  member  = "serviceAccount:${google_service_account.guard.email}"
}

# Official IAM resource attributes list global forwarding rules by this exact
# relative resource.name; addresses are absent from that supported table.
resource "google_project_iam_custom_role" "stop_forwarding_rules" {
  project     = var.project_id
  role_id     = "rangleStgStopForwardingRules"
  title       = "Delete presentation forwarding rules"
  permissions = ["compute.globalForwardingRules.delete"]
  depends_on  = [google_project_service.required]
}
resource "google_project_iam_member" "stop_forwarding_rules" {
  project = var.project_id
  role    = google_project_iam_custom_role.stop_forwarding_rules.name
  member  = "serviceAccount:${google_service_account.guard.email}"
  condition {
    title      = "only_presentation_forwarding_rules"
    expression = "resource.name in ['projects/${var.project_id}/global/forwardingRules/${local.prefix}-http', 'projects/${var.project_id}/global/forwardingRules/${local.prefix}-https']"
  }
}
resource "google_project_iam_custom_role" "release_address" {
  project     = var.project_id
  role_id     = "rangleStgReleaseAddress"
  title       = "Release presentation external address"
  permissions = ["compute.globalAddresses.delete"]
  depends_on  = [google_project_service.required]
}
# No false per-address scope claim: the supported resource.name table omits
# global addresses. The only mutating code target is rangle-stg-edge.
resource "google_project_iam_member" "release_address" {
  project = var.project_id
  role    = google_project_iam_custom_role.release_address.name
  member  = "serviceAccount:${google_service_account.guard.email}"
}
resource "google_project_iam_custom_role" "edge_reader" {
  project     = var.project_id
  role_id     = "rangleStgEdgeReader"
  title       = "Read edge absence and global deletion operations"
  permissions = ["compute.globalForwardingRules.get", "compute.globalAddresses.get", "compute.globalOperations.get"]
  depends_on  = [google_project_service.required]
}
resource "google_project_iam_member" "edge_reader" {
  project = var.project_id
  role    = google_project_iam_custom_role.edge_reader.name
  member  = "serviceAccount:${google_service_account.guard.email}"
}
