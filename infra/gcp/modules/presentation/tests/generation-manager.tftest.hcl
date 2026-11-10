mock_provider "google" {
  mock_data "google_artifact_registry_repository" { defaults = { format = "DOCKER" } }
}
override_resource {
  target          = google_service_account.runtime
  override_during = plan
  values = {
    email = "rangle-stg-runtime@rangle-example.iam.gserviceaccount.com"
    name  = "projects/rangle-example/serviceAccounts/rangle-stg-runtime@rangle-example.iam.gserviceaccount.com"
  }
}
override_resource {
  target          = google_service_account.generation_manager[0]
  override_during = plan
  values = {
    email = "rangle-stg-generation@rangle-example.iam.gserviceaccount.com"
    name  = "projects/rangle-example/serviceAccounts/rangle-stg-generation@rangle-example.iam.gserviceaccount.com"
  }
}
override_resource {
  target          = google_service_account.generation_sync[0]
  override_during = plan
  values = {
    email = "rangle-stg-generation-sync@rangle-example.iam.gserviceaccount.com"
    name  = "projects/rangle-example/serviceAccounts/rangle-stg-generation-sync@rangle-example.iam.gserviceaccount.com"
  }
}
variables {
  project_id              = "rangle-example"
  project_number          = "123456789012"
  region                  = "asia-northeast1"
  base_domain             = "presentation.example.com"
  budget_currency         = "JPY"
  budget_stop_amount      = 15000
  generation_sync_enabled = true
  application = {
    image_digest             = "asia-northeast1-docker.pkg.dev/rangle-example/rangle-stg/server@sha256:0000000000000000000000000000000000000000000000000000000000000000"
    catalog_sha256           = "1111111111111111111111111111111111111111111111111111111111111111"
    community_enabled        = true
    generation_config_sha256 = "2222222222222222222222222222222222222222222222222222222222222222"
  }
}
run "public_process_has_no_vertex_trainer_or_generation_database_capability" {
  command = plan
  assert {
    condition     = google_project_iam_member.generation_runtime[0].member == "serviceAccount:${google_service_account.generation_manager[0].email}" && google_service_account_iam_member.trainer_act_as[0].member == "serviceAccount:${google_service_account.generation_manager[0].email}" && google_project_iam_member.generation_data[0].member == "serviceAccount:${google_service_account.generation_manager[0].email}" && google_project_iam_member.community_runtime[0].member == "serviceAccount:${google_service_account.runtime.email}"
    error_message = "Vertex, trainer actAs and the admission ledger must belong only to the isolated manager identity."
  }
  assert {
    condition     = google_firestore_database.generation[0].name == "rangle-stg-generation" && google_project_iam_member.generation_data[0].condition[0].expression == "resource.name == 'projects/rangle-example/databases/rangle-stg-generation' && request.time < timestamp('2026-11-14T15:00:00Z')" && google_project_iam_member.community_runtime[0].condition[0].expression == "resource.name == 'projects/rangle-example/databases/rangle-stg' && request.time < timestamp('2026-11-14T15:00:00Z')"
    error_message = "Account and generation database permissions must remain disjoint and deadline-bound."
  }
  assert {
    condition     = !google_cloud_run_v2_service.generation[0].invoker_iam_disabled && google_cloud_run_v2_service_iam_member.generation_runtime_invoker[0].role == "roles/run.invoker" && google_cloud_run_v2_service_iam_member.generation_runtime_invoker[0].member == "serviceAccount:${google_service_account.runtime.email}" && google_cloud_run_v2_service_iam_member.generation_sync_invoker[0].member == "serviceAccount:${google_service_account.generation_sync[0].email}"
    error_message = "Manager requires Cloud Run IAM and only explicitly scoped callers, without an anonymous invoker."
  }
  assert {
    condition     = google_cloud_run_v2_service.generation[0].template[0].volumes[0].gcs[0].read_only && google_cloud_run_v2_service.generation[0].scaling[0].max_instance_count == 1 && google_cloud_run_v2_service.generation[0].scaling[0].min_instance_count == 0 && contains(google_cloud_run_v2_service.generation[0].template[0].containers[0].args, "-generation-manager") && !contains(google_cloud_run_v2_service.generation[0].template[0].containers[0].args, "-firestore-project") && !contains(google_cloud_run_v2_service.generation[0].template[0].containers[0].args, "-ui-root")
    error_message = "Keep a finite private manager process with readonly configuration and no community database or UI."
  }
  assert {
    condition     = google_firestore_database.generation[0].point_in_time_recovery_enablement == "POINT_IN_TIME_RECOVERY_ENABLED" && google_firestore_database.generation[0].delete_protection_state == "DELETE_PROTECTION_ENABLED" && google_firestore_backup_schedule.generation_daily[0].retention == "604800s"
    error_message = "Generation counters and publication history need protected persistent storage and seven-day recovery."
  }
  assert {
    condition     = google_project_iam_custom_role.generation_output_list[0].permissions == toset(["storage.objects.list"]) && google_project_iam_custom_role.generation_output_delete[0].permissions == toset(["storage.objects.delete"]) && google_project_iam_custom_role.generation_cache_cleanup[0].permissions == toset(["compute.urlMaps.invalidateCache", "compute.globalOperations.get"])
    error_message = "Cleanup must not acquire object restore/create, bucket administration or load balancer mutation permissions."
  }
  assert {
    condition     = google_storage_bucket_iam_member.generation_output_delete[0].member == "serviceAccount:${google_service_account.generation_manager[0].email}" && google_storage_bucket_iam_member.generation_output_delete[0].bucket == google_storage_bucket.private["media"].name && strcontains(google_storage_bucket_iam_member.generation_output_delete[0].condition[0].expression, "/objects/generated/") && strcontains(google_storage_bucket_iam_member.generation_output_delete[0].condition[0].expression, "/objects/live/") && strcontains(google_storage_bucket_iam_member.generation_output_delete[0].condition[0].expression, "request.time < timestamp('2026-11-14T15:00:00Z')") && contains(google_cloud_run_v2_service.generation[0].template[0].containers[0].args, "-generation-cleanup-url-map")
    error_message = "Only the manager may delete derived generated/live output prefixes, before the existing deadline."
  }
  assert {
    condition     = google_logging_log_view.generation_progress[0].name == "generation-progress" && google_logging_log_view.generation_progress[0].filter == "source(\"projects/rangle-example\") AND resource.type=\"ml_job\" AND log_id(\"workerpool0-0\")" && google_logging_log_view_iam_member.generation_progress[0].role == "roles/logging.viewAccessor" && google_logging_log_view_iam_member.generation_progress[0].member == "serviceAccount:${google_service_account.generation_manager[0].email}" && google_logging_log_view_iam_member.generation_progress[0].condition[0].expression == "request.time < timestamp('2026-11-14T15:00:00Z')"
    error_message = "Only the private manager may read the dedicated worker log view, within the presentation deadline."
  }
}
