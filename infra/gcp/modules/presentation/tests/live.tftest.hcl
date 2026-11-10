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
  # Stable, distinct IDs let the mock plan distinguish the two cache policies.
  override_resource {
    target          = google_compute_backend_bucket.media
    override_during = plan
    values          = { id = "projects/rangle-example/global/backendBuckets/media" }
  }
  override_resource {
    target          = google_compute_backend_bucket.live_index[0]
    override_during = plan
    values          = { id = "projects/rangle-example/global/backendBuckets/live-index" }
  }
  assert {
    condition     = contains(google_cloud_run_v2_service.app[0].template[0].containers[0].args, "-live-index") && contains(google_cloud_run_v2_service.app[0].template[0].containers[0].args, "gs://rangle-stg-123456789012-catalog/live/index.json")
    error_message = "Live metadata must use the fixed private index object."
  }
  assert {
    condition     = google_cloud_run_v2_service.app[0].scaling[0].max_instance_count == 2 && google_cloud_run_v2_service.app[0].template[0].max_instance_request_concurrency == 8 && google_storage_bucket_iam_member.catalog_reader.role == "roles/storage.objectViewer"
    error_message = "Enabling live must preserve capacity limits and the existing read-only storage grant."
  }
  assert {
    condition     = google_compute_backend_bucket.live_index[0].cdn_policy[0].cache_mode == "FORCE_CACHE_ALL" && google_compute_backend_bucket.live_index[0].cdn_policy[0].default_ttl == 1 && google_compute_backend_bucket.live_index[0].cdn_policy[0].client_ttl == 1 && !google_compute_backend_bucket.live_index[0].cdn_policy[0].negative_caching && google_compute_backend_bucket.live_index[0].cdn_policy[0].serve_while_stale == 0 && google_compute_backend_bucket.live_index[0].cdn_policy[0].request_coalescing
    error_message = "Private mutable indexes must cache for one second, coalesce fills, and never serve stale or negatively cached discoveries."
  }
  assert {
    condition = one([
      for matcher in google_compute_url_map.https[0].path_matcher :
      matcher.default_service == google_compute_backend_bucket.media.id &&
      one([for rule in matcher.path_rule : rule.service if contains(rule.paths, "/live-index/*")]) == google_compute_backend_bucket.live_index[0].id
      if matcher.name == "media"
    ]) && google_compute_backend_bucket.media.cdn_policy[0].default_ttl == 86400
    error_message = "The mutable index path must select the one-second backend; immutable live payloads retain the day-long media default."
  }
  assert {
    condition     = one([for header in one([for matcher in google_compute_url_map.https[0].path_matcher : matcher if matcher.name == "media"]).header_action[0].response_headers_to_add : header.header_value == "*" && header.replace if header.header_name == "Access-Control-Allow-Origin"])
    error_message = "The shared media matcher must also add CORS to conditional live-index and immutable live responses."
  }
  assert {
    condition = { for test in google_compute_url_map.https[0].test : test.path => test.service if test.host == "media.presentation.example.com" && length(test.headers) == 0 } == {
      "/live-index/example.json"                         = google_compute_backend_bucket.live_index[0].id
      "/live/example/000001-aaaaaaaaaaaaaaaa/scene.json" = google_compute_backend_bucket.media.id
      "/live/example/000001-aaaaaaaaaaaaaaaa/model.rgs"  = google_compute_backend_bucket.media.id
    }
    error_message = "Deployments must carry native URL map tests for the actual public index, scene, and model path shapes."
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
