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
run "old_images_receive_no_live_flag" {
  command = plan
  assert {
    condition     = !contains(google_cloud_run_v2_service.app[0].template[0].containers[0].args, "-live-index")
    error_message = "Existing images must not receive live flags without explicit enablement."
  }
  assert {
    condition     = length(google_compute_backend_bucket.live_index) == 0
    error_message = "Live metadata routing must remain opt-in."
  }
}
run "live_uses_existing_private_index_without_changing_capacity" {
  command = plan
  variables { live_streaming_enabled = true }
  assert {
    condition     = contains(google_cloud_run_v2_service.app[0].template[0].containers[0].args, "-live-index") && contains(google_cloud_run_v2_service.app[0].template[0].containers[0].args, "gs://rangle-stg-123456789012-catalog/live/index.json")
    error_message = "Live metadata must use the fixed private index object."
  }
  assert {
    condition     = google_cloud_run_v2_service.app[0].scaling[0].max_instance_count == 1 && google_cloud_run_v2_service.app[0].template[0].max_instance_request_concurrency == 40 && google_storage_bucket_iam_member.catalog_reader.role == "roles/storage.objectViewer"
    error_message = "Enabling live must preserve capacity limits and the existing read-only storage grant."
  }
  assert {
    condition     = google_compute_backend_bucket.live_index[0].cdn_policy[0].default_ttl == 1 && google_compute_backend_bucket.live_index[0].cdn_policy[0].client_ttl == 1 && google_compute_backend_bucket.media.cdn_policy[0].default_ttl == 86400 && contains(flatten([for matcher in google_compute_url_map.https[0].path_matcher : [for rule in matcher.path_rule : rule.paths]]), "/live-index/*")
    error_message = "Mutable live indexes need a distinct one-second CDN route; model caching must remain unchanged."
  }
}
run "live_cannot_enable_without_application" {
  command = plan
  variables {
    application            = null
    live_streaming_enabled = true
  }
  expect_failures = [var.live_streaming_enabled]
}
