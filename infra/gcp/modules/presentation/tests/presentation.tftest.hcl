mock_provider "google" {
  mock_data "google_artifact_registry_repository" {
    defaults = { format = "DOCKER" }
  }
}
mock_provider "google-beta" {}

variables {
  project_id         = "rangle-example"
  project_number     = "123456789012"
  region             = "asia-northeast1"
  hosting_site_id    = "rangle-example-stg-media"
  budget_currency    = "JPY"
  budget_stop_amount = 15000
}

run "foundation_without_dummy_app" {
  command = plan
  assert {
    condition     = length(google_cloud_run_v2_service.app) == 0 && length(google_artifact_registry_repository.images) == 0
    error_message = "An absent catalog must not start a dummy app or alter the existing artifact repository."
  }
  assert {
    condition     = alltrue([for bucket in google_storage_bucket.private : bucket.public_access_prevention == "enforced"]) && !google_storage_bucket.private["catalog"].versioning[0].enabled
    error_message = "All stored data must stay private and FUSE catalog versioning must remain off."
  }
  assert {
    condition     = strcontains(google_iam_workload_identity_pool_provider.github.attribute_condition, "assertion.repository_id == '1448803636'") && strcontains(google_iam_workload_identity_pool_provider.github.attribute_condition, "assertion.repository_owner_id == '128687243'") && strcontains(google_iam_workload_identity_pool_provider.github.attribute_condition, "deploy-stg.yml@refs/heads/stg") && strcontains(google_iam_workload_identity_pool_provider.github.attribute_condition, "['push', 'workflow_dispatch']")
    error_message = "WIF must pin the numeric repository/owner, branch, workflow path, and allowed event types."
  }
  assert {
    condition     = strcontains(google_service_account_iam_member.github_deployer.condition[0].expression, "2026-11-14T15:00:00Z") && google_cloud_scheduler_job.guard.schedule == "*/15 * * * *"
    error_message = "Federation must expire and the budget/deadline guard must poll every 15 minutes."
  }
  assert {
    condition     = strcontains(google_workflows_workflow.guard.source_contents, "SITE_DISABLE") && strcontains(google_workflows_workflow.guard.source_contents, "observed_cost >= 15000") && strcontains(google_workflows_workflow.guard.source_contents, "Budget currency mismatch")
    error_message = "The fixed-site stop must check the budget amount and exact configured currency."
  }
  assert {
    condition     = contains(keys(yamldecode(google_workflows_workflow.guard.source_contents)), "main") && contains(keys(yamldecode(google_workflows_workflow.guard.source_contents)), "wait_until_absent")
    error_message = "The rendered workflow must be valid YAML with both entrypoint and bounded service verification."
  }
}

run "public_app_is_bounded" {
  command = plan
  variables {
    application = {
      image_digest   = "asia-northeast1-docker.pkg.dev/rangle-example/rangle-stg/server@sha256:0000000000000000000000000000000000000000000000000000000000000000"
      catalog_sha256 = "1111111111111111111111111111111111111111111111111111111111111111"
    }
  }
  assert {
    condition     = google_cloud_run_v2_service.app[0].ingress == "INGRESS_TRAFFIC_ALL" && !google_cloud_run_v2_service.app[0].default_uri_disabled && google_cloud_run_v2_service.app[0].scaling[0].max_instance_count == 1 && google_cloud_run_v2_service.app[0].scaling[0].min_instance_count == 0
    error_message = "Use free run.app HTTPS with one maximum instance and no idle minimum."
  }
  assert {
    condition     = google_cloud_run_v2_service.app[0].template[0].max_instance_request_concurrency == 40 && google_cloud_run_v2_service.app[0].template[0].containers[0].resources[0].cpu_idle && contains(google_cloud_run_v2_service.app[0].template[0].containers[0].args, "https://rangle-example-stg-media.web.app")
    error_message = "Keep request billing, bounded concurrency and direct Hosting media URLs."
  }
  assert {
    condition     = google_cloud_run_v2_service.app[0].template[0].volumes[0].gcs[0].read_only && google_cloud_run_v2_service_iam_member.stop_run[0].name == google_cloud_run_v2_service.app[0].name
    error_message = "Keep a read-only catalog and scope shutdown permission to the owned service."
  }
  assert {
    condition     = google_cloud_run_v2_service.app[0].traffic[0].type == "TRAFFIC_TARGET_ALLOCATION_TYPE_LATEST" && google_cloud_run_v2_service.app[0].traffic[0].percent == 100
    error_message = "Initialize one revision at 100%; subsequent image/traffic changes belong to the release workflow."
  }
}

run "optional_project_budget" {
  command = plan
  variables {
    budget = {
      billing_account_id = "000000-000000-000000"
      amount             = 30000
      start_date         = "2026-11-08"
      end_date           = "2026-11-15"
    }
  }
  assert {
    condition     = google_billing_budget.presentation[0].ownership_scope == "ALL_USERS" && google_billing_budget.presentation[0].budget_filter[0].projects == toset(["projects/123456789012"]) && google_billing_budget.presentation[0].amount[0].specified_amount[0].currency_code == "JPY"
    error_message = "Our optional budget must be project-scoped and use verified account currency."
  }
}
