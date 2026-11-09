output "deployment" {
  value = {
    project_id                 = var.project_id
    project_number             = var.project_number
    region                     = var.region
    service                    = local.prefix
    app_uri                    = local.app_enabled ? google_cloud_run_v2_service.app[0].uri : null
    artifact_image_base        = "${var.region}-docker.pkg.dev/${var.project_id}/${local.prefix}/server"
    runtime_service_account    = google_service_account.runtime.email
    deploy_service_account     = google_service_account.deployer.email
    workload_identity_provider = google_iam_workload_identity_pool_provider.github.name
    base_domain                = var.base_domain
    edge_ip                    = google_compute_global_address.edge.address
    app_origin                 = local.app_origin
    dns_records = var.base_domain == null ? [] : [
      { type = "A", name = local.app_host, value = google_compute_global_address.edge.address, proxied = false },
      { type = "A", name = local.media_host, value = google_compute_global_address.edge.address, proxied = false }
    ]
    media_origin        = local.media_origin
    catalog_bucket      = google_storage_bucket.private["catalog"].name
    media_mirror_bucket = google_storage_bucket.private["media"].name
    inputs_bucket       = google_storage_bucket.private["inputs"].name
    catalog_object      = var.application == null ? null : "snapshots/${var.application.catalog_sha256}.json"
    community_storage = local.community_enabled ? {
      project_id  = var.project_id
      database    = google_firestore_database.community[0].name
      location    = google_firestore_database.community[0].location_id
      session_ttl = "sessions.expires_at"
    } : null
    generation = local.generation_enabled ? {
      live = local.generation_live_enabled ? {
        trainer_service_account = google_service_account.live_trainer[0].email
        input_prefix            = "gs://${google_storage_bucket.private["inputs"].name}/datasets/live/"
        output_prefix           = "gs://${google_storage_bucket.private["media"].name}/live/"
        session_index_prefix    = "gs://${google_storage_bucket.private["media"].name}/live-index/"
        catalog_index           = "gs://${google_storage_bucket.private["catalog"].name}/live/index.json"
      } : null
      sync = local.generation_sync_enabled ? {
        scheduler = local.generation_sync_job
        audience  = local.generation_manager_origin
        identity  = google_service_account.generation_sync[0].email
      } : null
      config_object           = "generation/${var.application.generation_config_sha256}.json"
      manager_origin          = local.generation_manager_origin
      manager_service_account = google_service_account.generation_manager[0].email
      database                = google_firestore_database.generation[0].name
      trainer_service_account = google_service_account.trainer[0].email
      region                  = var.region
      input_prefix            = "gs://${google_storage_bucket.private["inputs"].name}/datasets/"
      output_prefix           = "gs://${google_storage_bucket.private["media"].name}/generated/"
    } : null
    deadline = var.deadline
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
