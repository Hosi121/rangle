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
    image_digest      = "asia-northeast1-docker.pkg.dev/rangle-example/rangle-stg/server@sha256:0000000000000000000000000000000000000000000000000000000000000000"
    catalog_sha256    = "1111111111111111111111111111111111111111111111111111111111111111"
    community_enabled = true
  }
}

run "community_only_backup_shutdown_is_known_at_plan" {
  command = plan
  override_resource {
    target          = google_service_account.guard
    override_during = plan
    values          = { email = "rangle-stg-guard@rangle-example.iam.gserviceaccount.com" }
  }
  assert {
    condition     = google_project_iam_custom_role.stop_backup_schedules[0].permissions == toset(["datastore.backupSchedules.list", "datastore.backupSchedules.delete"]) && google_project_iam_member.stop_backup_schedules[0].member == "serviceAccount:rangle-stg-guard@rangle-example.iam.gserviceaccount.com" && length(google_project_iam_member.stop_backup_schedules[0].condition) == 0
    error_message = "Only guard receives the two project-level schedule permissions; no backup/data/database delete or unsupported resource-name condition."
  }
  assert {
    condition     = one([for step in yamldecode(google_workflows_workflow.guard.source_contents).cleanup_owned_backup_schedules.steps : step.stop_future_backups if can(step.stop_future_backups)]).for.in == ["projects/rangle-example/databases/rangle-stg"]
    error_message = "List schedules only under the fixed enabled community DB, without server-generated schedule names in the template."
  }
  assert {
    condition     = index([for step in yamldecode(google_workflows_workflow.guard.source_contents).main.steps : keys(step)[0]], "cancel_owned_generation") < index([for step in yamldecode(google_workflows_workflow.guard.source_contents).main.steps : keys(step)[0]], "stop_future_backup_schedules") && index([for step in yamldecode(google_workflows_workflow.guard.source_contents).main.steps : keys(step)[0]], "stop_future_backup_schedules") < index([for step in yamldecode(google_workflows_workflow.guard.source_contents).main.steps : keys(step)[0]], "check_shutdown") && can(one([for step in yamldecode(google_workflows_workflow.guard.source_contents).main.steps : step.stop_future_backup_schedules if can(step.stop_future_backup_schedules)]).except.steps[0].record_backup_schedule_error)
    error_message = "Core service/GPU stops precede schedule cleanup, whose failures enter the shared failure receipt before acknowledgment/self-pause."
  }
  assert {
    condition     = alltrue([for expression in regexall("\\$\\{[^\\n]*\\}", google_workflows_workflow.guard.source_contents) : length(expression) <= 400]) && !strcontains(google_workflows_workflow.guard.source_contents, "/backups/") && strcontains(google_workflows_workflow.guard.source_contents, "sys.now() + 60")
    error_message = "Keep compiler-sized expressions and bounded cleanup, without existing-backup deletion paths."
  }
}

run "generation_shutdown_stops_both_schedules_and_preserves_recovery_data" {
  command = plan
  variables {
    application = {
      image_digest             = "asia-northeast1-docker.pkg.dev/rangle-example/rangle-stg/server@sha256:0000000000000000000000000000000000000000000000000000000000000000"
      catalog_sha256           = "1111111111111111111111111111111111111111111111111111111111111111"
      community_enabled        = true
      generation_config_sha256 = "2222222222222222222222222222222222222222222222222222222222222222"
    }
  }
  assert {
    condition     = one([for step in yamldecode(google_workflows_workflow.guard.source_contents).cleanup_owned_backup_schedules.steps : step.stop_future_backups if can(step.stop_future_backups)]).for.in == ["projects/rangle-example/databases/rangle-stg", "projects/rangle-example/databases/rangle-stg-generation"]
    error_message = "Generation adds exactly the owned generation DB to schedule cleanup."
  }
  assert {
    condition     = google_firestore_backup_schedule.community_daily[0].retention == "604800s" && google_firestore_backup_schedule.generation_daily[0].retention == "604800s" && google_firestore_database.community[0].point_in_time_recovery_enablement == "POINT_IN_TIME_RECOVERY_ENABLED" && google_firestore_database.generation[0].point_in_time_recovery_enablement == "POINT_IN_TIME_RECOVERY_ENABLED" && google_firestore_database.community[0].deletion_policy == "PREVENT" && google_firestore_database.generation[0].deletion_policy == "PREVENT"
    error_message = "Shutdown removes future schedules, preserving seven-day retention, PITR and both protected databases."
  }
}

run "disabled_foundation_has_no_schedule_permissions_or_targets" {
  command = plan
  variables {
    application = null
    base_domain = null
  }
  assert {
    condition     = length(google_project_iam_custom_role.stop_backup_schedules) == 0 && length(google_project_iam_member.stop_backup_schedules) == 0 && one([for step in yamldecode(google_workflows_workflow.guard.source_contents).cleanup_owned_backup_schedules.steps : step.stop_future_backups if can(step.stop_future_backups)]).for.in == []
    error_message = "The private foundation performs no backup schedule calls and gains no schedule-delete permissions."
  }
}
