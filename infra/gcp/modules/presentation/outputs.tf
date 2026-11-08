output "deployment" {
  value = {
    project_id                 = var.project_id
    project_number             = var.project_number
    region                     = var.region
    service                    = local.prefix
    app_uri                    = var.application == null ? null : google_cloud_run_v2_service.app[0].uri
    artifact_image_base        = "${var.region}-docker.pkg.dev/${var.project_id}/${local.prefix}/server"
    runtime_service_account    = google_service_account.runtime.email
    deploy_service_account     = google_service_account.deployer.email
    workload_identity_provider = google_iam_workload_identity_pool_provider.github.name
    hosting_site               = google_firebase_hosting_site.media.site_id
    media_origin               = local.media_origin
    catalog_bucket             = google_storage_bucket.private["catalog"].name
    media_mirror_bucket        = google_storage_bucket.private["media"].name
    inputs_bucket              = google_storage_bucket.private["inputs"].name
    catalog_object             = var.application == null ? null : "snapshots/${var.application.catalog_sha256}.json"
    deadline                   = var.deadline
  }
}
output "budget_guard" {
  value = {
    topic          = google_pubsub_topic.budget.id
    subscription   = google_pubsub_subscription.budget.id
    display_name   = var.budget_display_name
    currency       = var.budget_currency
    stop_amount    = var.budget_stop_amount
    workflow       = google_workflows_workflow.guard.id
    scheduler      = google_cloud_scheduler_job.guard.id
    receipt_bucket = google_storage_bucket.private["inputs"].name
    receipt_object = "guard/presentation-stop.json"
  }
}
