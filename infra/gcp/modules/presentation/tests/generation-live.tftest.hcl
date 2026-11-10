mock_provider "google" {
  mock_data "google_artifact_registry_repository" { defaults = { format = "DOCKER" } }
}
variables {
  project_id             = "rangle-example"
  project_number         = "123456789012"
  region                 = "asia-northeast1"
  base_domain            = "presentation.example.com"
  budget_currency        = "JPY"
  budget_stop_amount     = 15000
  live_streaming_enabled = true
  application = {
    image_digest             = "asia-northeast1-docker.pkg.dev/rangle-example/rangle-stg/server@sha256:0000000000000000000000000000000000000000000000000000000000000000"
    catalog_sha256           = "1111111111111111111111111111111111111111111111111111111111111111"
    community_enabled        = true
    generation_config_sha256 = "2222222222222222222222222222222222222222222222222222222222222222"
  }
}

run "existing_generation_and_live_viewer_gain_no_writer" {
  command = plan
  assert {
    condition     = length(google_service_account.live_trainer) == 0 && length(google_service_account_iam_member.live_trainer_act_as) == 0 && length(google_storage_bucket_iam_member.live_trainer_input) == 0 && length(google_storage_bucket_iam_member.live_trainer_immutable) == 0 && length(google_storage_bucket_iam_member.live_trainer_pointer) == 0 && length(google_project_iam_custom_role.live_immutable) == 0 && length(google_project_iam_custom_role.live_pointer) == 0 && output.deployment.generation.live == null
    error_message = "Live viewing and offline generation must not implicitly grant cloud live write or actAs permissions."
  }
}

run "enabled_live_identity_has_only_scoped_deadline_grants" {
  command = plan
  variables {
    application = {
      image_digest             = "asia-northeast1-docker.pkg.dev/rangle-example/rangle-stg/server@sha256:0000000000000000000000000000000000000000000000000000000000000000"
      catalog_sha256           = "1111111111111111111111111111111111111111111111111111111111111111"
      community_enabled        = true
      generation_config_sha256 = "2222222222222222222222222222222222222222222222222222222222222222"
      generation_live_enabled  = true
    }
  }
  assert {
    condition     = google_service_account.live_trainer[0].account_id == "rangle-stg-live-trainer" && google_service_account_iam_member.live_trainer_act_as[0].role == "roles/iam.serviceAccountUser" && google_service_account_iam_member.live_trainer_act_as[0].condition[0].expression == "request.time < timestamp('2026-11-14T15:00:00Z')"
    error_message = "Only the runtime may act as the dedicated live worker before the fixed deadline."
  }
  assert {
    condition     = google_project_iam_custom_role.trainer_input[0].permissions == toset(["storage.objects.get"]) && google_storage_bucket_iam_member.live_trainer_input[0].bucket == "rangle-stg-123456789012-inputs" && google_storage_bucket_iam_member.live_trainer_input[0].condition[0].expression == "resource.name.startsWith('projects/_/buckets/rangle-stg-123456789012-inputs/objects/datasets/live/') && request.time < timestamp('2026-11-14T15:00:00Z')"
    error_message = "Live input access must be exact-object get within datasets/live, with no list or bucket metadata read."
  }
  assert {
    condition     = google_project_iam_custom_role.live_immutable[0].permissions == toset(["storage.objects.get", "storage.objects.create"]) && length(google_storage_bucket_iam_member.live_trainer_immutable) == 2 && google_storage_bucket_iam_member.live_trainer_immutable["media"].condition[0].expression == "resource.name.startsWith('projects/_/buckets/rangle-stg-123456789012-media/objects/live/') && request.time < timestamp('2026-11-14T15:00:00Z')" && google_storage_bucket_iam_member.live_trainer_immutable["catalog"].condition[0].expression == "resource.name.startsWith('projects/_/buckets/rangle-stg-123456789012-catalog/objects/live/snapshots/') && request.time < timestamp('2026-11-14T15:00:00Z')"
    error_message = "Models, scenes and private index archives must be append-only, restricted to their exact live prefixes."
  }
  assert {
    condition     = google_project_iam_custom_role.live_pointer[0].permissions == toset(["storage.objects.get", "storage.objects.create", "storage.objects.delete"]) && length(google_storage_bucket_iam_member.live_trainer_pointer) == 2 && google_storage_bucket_iam_member.live_trainer_pointer["media"].condition[0].expression == "resource.name.startsWith('projects/_/buckets/rangle-stg-123456789012-media/objects/live-index/') && request.time < timestamp('2026-11-14T15:00:00Z')" && google_storage_bucket_iam_member.live_trainer_pointer["catalog"].condition[0].expression == "resource.name == 'projects/_/buckets/rangle-stg-123456789012-catalog/objects/live/index.json' && request.time < timestamp('2026-11-14T15:00:00Z')"
    error_message = "GCS replacement permission must be confined to mutable session indexes and the single private live index."
  }
  assert {
    condition     = google_storage_bucket_iam_member.trainer_output[0].role == "roles/storage.objectCreator" && google_storage_bucket_iam_member.trainer_output[0].condition[0].expression == "resource.name.startsWith('projects/_/buckets/rangle-stg-123456789012-media/objects/generated/') && request.time < timestamp('2026-11-14T15:00:00Z')" && google_project_iam_custom_role.generation_guard[0].permissions == toset(["aiplatform.customJobs.list", "aiplatform.customJobs.cancel"]) && google_cloud_run_v2_service.app[0].scaling[0].max_instance_count == 2
    error_message = "Live identity provisioning must preserve offline training, cancellation permissions and the provisional bounded app capacity."
  }
  assert {
    condition     = output.deployment.generation.live.input_prefix == "gs://rangle-stg-123456789012-inputs/datasets/live/" && output.deployment.generation.live.output_prefix == "gs://rangle-stg-123456789012-media/live/" && output.deployment.generation.live.catalog_index == "gs://rangle-stg-123456789012-catalog/live/index.json"
    error_message = "Deployment output must identify the fixed live input, media and metadata locations."
  }
}

run "live_worker_requires_generation_config" {
  command = plan
  variables {
    application = {
      image_digest            = "asia-northeast1-docker.pkg.dev/rangle-example/rangle-stg/server@sha256:0000000000000000000000000000000000000000000000000000000000000000"
      catalog_sha256          = "1111111111111111111111111111111111111111111111111111111111111111"
      generation_live_enabled = true
    }
  }
  expect_failures = [var.application]
}

run "live_worker_requires_live_metadata" {
  command = plan
  variables {
    live_streaming_enabled = false
    application = {
      image_digest             = "asia-northeast1-docker.pkg.dev/rangle-example/rangle-stg/server@sha256:0000000000000000000000000000000000000000000000000000000000000000"
      catalog_sha256           = "1111111111111111111111111111111111111111111111111111111111111111"
      community_enabled        = true
      generation_config_sha256 = "2222222222222222222222222222222222222222222222222222222222222222"
      generation_live_enabled  = true
    }
  }
  expect_failures = [var.live_streaming_enabled]
}

run "live_worker_requires_configured_domain" {
  command = plan
  variables {
    base_domain = null
    application = {
      image_digest             = "asia-northeast1-docker.pkg.dev/rangle-example/rangle-stg/server@sha256:0000000000000000000000000000000000000000000000000000000000000000"
      catalog_sha256           = "1111111111111111111111111111111111111111111111111111111111111111"
      community_enabled        = true
      generation_config_sha256 = "2222222222222222222222222222222222222222222222222222222222222222"
      generation_live_enabled  = true
    }
  }
  expect_failures = [var.application]
}
