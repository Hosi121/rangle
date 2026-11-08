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
  member  = "serviceAccount:billing-budget-pubsub@system.gserviceaccount.com"
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
# Hosting does not support custom roles/site-scoped IAM. This is the narrowest
# available predefined write role; the immutable workflow targets only our site.
# CI deployer has no Hosting role.
resource "google_project_iam_member" "hosting_guard" {
  project = var.project_id
  role    = "roles/firebasehosting.admin"
  member  = "serviceAccount:${google_service_account.guard.email}"
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
  count    = var.application == null ? 0 : 1
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
    deadline            = var.deadline
    subscription        = "projects/${var.project_id}/subscriptions/${local.prefix}-budget-guard"
    budget_display_name = var.budget_display_name
    budget_currency     = var.budget_currency
    stop_amount         = var.budget_stop_amount
    site                = var.hosting_site_id
    service             = local.service_name
    operation_prefix    = "projects/${var.project_id}/locations/${var.region}/operations/"
    deployer            = "projects/${var.project_id}/serviceAccounts/${local.prefix}-deploy@${var.project_id}.iam.gserviceaccount.com"
    receipt_bucket      = google_storage_bucket.private["inputs"].name
    scheduler           = local.job_name
  })
  depends_on = [google_project_service.required, google_project_iam_member.hosting_guard, google_service_account_iam_member.disable_deployer, google_project_iam_member.pause_guard, google_pubsub_subscription_iam_member.guard, google_storage_bucket_iam_member.guard_receipt, google_firebase_hosting_site.media, google_project_iam_member.run_operations, google_project_iam_member.inspect_run]
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
