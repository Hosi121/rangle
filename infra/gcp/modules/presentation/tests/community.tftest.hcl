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
    image_digest   = "asia-northeast1-docker.pkg.dev/rangle-example/rangle-stg/server@sha256:0000000000000000000000000000000000000000000000000000000000000000"
    catalog_sha256 = "1111111111111111111111111111111111111111111111111111111111111111"
  }
}
run "existing_application_keeps_community_disabled" {
  command = plan
  assert {
    condition     = length(google_project_service.community) == 0 && length(google_firestore_database.community) == 0 && length(google_firestore_field.sessions_expiry) == 0 && length(google_firestore_field.staff_sessions_expiry) == 0 && length(google_project_iam_member.community_runtime) == 0 && output.deployment.community_storage == null
    error_message = "Existing application inputs must create no community resources or data grants."
  }
  assert {
    condition     = !contains(google_cloud_run_v2_service.app[0].template[0].containers[0].args, "-firestore-project") && !contains(google_cloud_run_v2_service.app[0].template[0].containers[0].args, "-firestore-database")
    error_message = "An existing server image must not receive new Firestore flags unless explicitly enabled."
  }
}
run "enabled_community_uses_only_named_tokyo_database" {
  command = plan
  variables {
    application = {
      image_digest      = "asia-northeast1-docker.pkg.dev/rangle-example/rangle-stg/server@sha256:0000000000000000000000000000000000000000000000000000000000000000"
      catalog_sha256    = "1111111111111111111111111111111111111111111111111111111111111111"
      community_enabled = true
    }
  }
  assert {
    condition     = google_firestore_database.community[0].name == "rangle-stg" && google_firestore_database.community[0].location_id == "asia-northeast1" && google_firestore_database.community[0].type == "FIRESTORE_NATIVE" && google_firestore_database.community[0].database_edition == "STANDARD"
    error_message = "Provision a dedicated named Native Standard database in Tokyo, preserving the default database."
  }
  assert {
    condition     = google_firestore_database.community[0].delete_protection_state == "DELETE_PROTECTION_ENABLED" && google_firestore_database.community[0].deletion_policy == "PREVENT" && google_firestore_database.community[0].point_in_time_recovery_enablement == "POINT_IN_TIME_RECOVERY_ENABLED"
    error_message = "Retain private account data behind deletion protection and enable point-in-time recovery."
  }
  assert {
    condition     = google_project_iam_member.community_runtime[0].condition[0].expression == "resource.name == 'projects/rangle-example/databases/rangle-stg' && request.time < timestamp('2026-11-14T15:00:00Z')" && google_project_iam_custom_role.community[0].permissions == toset(["datastore.databases.get", "datastore.entities.get", "datastore.entities.list", "datastore.entities.create", "datastore.entities.update", "datastore.entities.delete"])
    error_message = "Runtime access must use exactly document CRUD/query/transaction permissions, restricted to this database and deadline."
  }
  assert {
    condition     = google_firestore_field.sessions_expiry[0].database == "rangle-stg" && google_firestore_field.sessions_expiry[0].collection == "sessions" && google_firestore_field.sessions_expiry[0].field == "expires_at" && length(google_firestore_field.sessions_expiry[0].ttl_config) == 1 && length(coalesce(google_firestore_field.sessions_expiry[0].index_config[0].indexes, [])) == 0
    error_message = "Enable TTL on sessions.expires_at without a single-field timestamp index."
  }
  assert {
    condition     = google_firestore_field.staff_sessions_expiry[0].database == "rangle-stg" && google_firestore_field.staff_sessions_expiry[0].collection == "staff_sessions" && google_firestore_field.staff_sessions_expiry[0].field == "expires_at" && length(google_firestore_field.staff_sessions_expiry[0].ttl_config) == 1 && length(coalesce(google_firestore_field.staff_sessions_expiry[0].index_config[0].indexes, [])) == 0
    error_message = "Staff sessions must have separate TTL cleanup without a timestamp index."
  }
  assert {
    condition     = contains(google_cloud_run_v2_service.app[0].template[0].containers[0].args, "-firestore-project") && contains(google_cloud_run_v2_service.app[0].template[0].containers[0].args, "rangle-example") && contains(google_cloud_run_v2_service.app[0].template[0].containers[0].args, "-firestore-database") && contains(google_cloud_run_v2_service.app[0].template[0].containers[0].args, "rangle-stg")
    error_message = "The server must receive the exact project and named database when community is enabled."
  }
}
