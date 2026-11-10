mock_provider "google" {
  mock_data "google_artifact_registry_repository" { defaults = { format = "DOCKER" } }
}
variables {
  project_id         = "rangle-example"
  project_number     = "123456789012"
  region             = "asia-northeast1"
  base_domain        = "presentation.example.com"
  budget_currency    = "JPY"
  budget_stop_amount = 15000
  application = {
    image_digest             = "asia-northeast1-docker.pkg.dev/rangle-example/rangle-stg/server@sha256:0000000000000000000000000000000000000000000000000000000000000000"
    catalog_sha256           = "1111111111111111111111111111111111111111111111111111111111111111"
    community_enabled        = true
    generation_config_sha256 = "2222222222222222222222222222222222222222222222222222222222222222"
  }
}
run "existing_generation_gains_no_scheduler_or_runtime_flag" {
  command = plan
  assert {
    condition     = length(google_service_account.generation_sync) == 0 && length(google_cloud_scheduler_job.generation_sync) == 0 && !contains(google_cloud_run_v2_service.app[0].template[0].containers[0].args, "-generation-sync-audience")
    error_message = "Polling must remain explicitly disabled for existing generation deployments."
  }
}
run "enabled_polling_has_fixed_identity_audience_frequency_and_guard" {
  command = plan
  variables { generation_sync_enabled = true }
  assert {
    condition     = google_service_account.generation_sync[0].account_id == "rangle-stg-generation-sync" && google_cloud_scheduler_job.generation_sync[0].schedule == "*/2 * * * *" && google_cloud_scheduler_job.generation_sync[0].retry_config[0].retry_count == 0 && google_cloud_scheduler_job.generation_sync[0].attempt_deadline == "30s"
    error_message = "Reconcile at most once per scheduled two-minute tick, without retry amplification."
  }
  assert {
    condition     = google_cloud_scheduler_job.generation_sync[0].http_target[0].uri == "https://rangle-stg-generation-123456789012.asia-northeast1.run.app/internal/generation/sync" && google_cloud_scheduler_job.generation_sync[0].http_target[0].oidc_token[0].audience == "https://rangle-stg-generation-123456789012.asia-northeast1.run.app" && google_cloud_scheduler_job.generation_sync[0].http_target[0].http_method == "POST"
    error_message = "OIDC tokens must target the exact fixed endpoint, with no job-selecting query or payload."
  }
  assert {
    condition     = contains(google_cloud_run_v2_service.generation[0].template[0].containers[0].args, "-generation-sync-audience") && contains(google_cloud_run_v2_service.generation[0].template[0].containers[0].args, "https://rangle-stg-generation-123456789012.asia-northeast1.run.app") && !contains(google_cloud_run_v2_service.app[0].template[0].containers[0].args, "-generation-sync-audience")
    error_message = "Only the isolated manager verifies scheduled generation synchronization."
  }
  assert {
    condition     = one([for step in yamldecode(google_workflows_workflow.guard.source_contents).main.steps : step.pause_generation_sync if can(step.pause_generation_sync)]).switch[0].condition && strcontains(google_workflows_workflow.guard.source_contents, "projects/rangle-example/locations/asia-northeast1/jobs/rangle-stg-generation-sync:pause") && strcontains(google_workflows_workflow.guard.source_contents, "observed_cost >= 15000") && strcontains(google_workflows_workflow.guard.source_contents, "2026-11-14T15:00:00Z")
    error_message = "The existing fixed deadline/JPY-budget shutdown must pause only the owned polling scheduler."
  }
  assert {
    condition     = alltrue([for expression in regexall("\\$\\{[^\\n]*\\}", google_workflows_workflow.guard.source_contents) : length(expression) <= 400])
    error_message = "Rendered Workflows expressions must remain within the real compiler's 400-character limit."
  }
}
run "polling_cannot_enable_without_generation" {
  command = plan
  variables {
    generation_sync_enabled = true
    application             = null
  }
  expect_failures = [var.generation_sync_enabled]
}
