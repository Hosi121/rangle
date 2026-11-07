# Mock provider only: no ADC, GCP API, state backend, or real resource creation.
mock_provider "google" {}

variables {
  environment              = "stg"
  deployer_service_account = "terraform-stg@rangle-example-stg.iam.gserviceaccount.com"
  config = {
    project_id                = "rangle-example-stg"
    project_number            = "123456789012"
    region                    = "us-central1"
    name_prefix               = "rangle"
    app_domain                = "stg.app.example.com"
    media_domain              = "stg.media.example.com"
    media_bucket_name         = "rangle-example-stg-published"
    catalog_bucket_name       = "rangle-example-stg-catalog"
    input_bucket_name         = "rangle-example-stg-inputs"
    publisher_service_account = "publisher-stg@rangle-example-stg.iam.gserviceaccount.com"
    media_cache_ttl_seconds   = 86400
  }
}

run "storage_before_catalog_publication" {
  command = plan
  assert {
    condition     = length(google_cloud_run_v2_service.app) == 0 && output.endpoints.app == null
    error_message = "An empty catalog store must not start a dummy application."
  }
  assert {
    condition     = google_storage_bucket.media.public_access_prevention == "enforced" && alltrue([for bucket in google_storage_bucket.private : bucket.public_access_prevention == "enforced"])
    error_message = "Every origin and private bucket must reject public IAM grants."
  }
  assert {
    condition     = !google_storage_bucket.private["catalog"].versioning[0].enabled
    error_message = "The FUSE-mounted catalog must not enable Object Versioning."
  }
  assert {
    condition     = google_service_account_iam_member.runtime_deployer.role == "roles/iam.serviceAccountUser" && google_service_account_iam_member.runtime_deployer.member == "serviceAccount:terraform-stg@rangle-example-stg.iam.gserviceaccount.com" && google_storage_bucket_iam_member.runtime_catalog.bucket == google_storage_bucket.private["catalog"].name
    error_message = "Bind deployment actAs to the environment execution identity and runtime reads to the catalog bucket."
  }
  assert {
    condition     = google_storage_bucket_iam_member.cache_fill.member == "serviceAccount:service-123456789012@https-lb.iam.gserviceaccount.com" && google_storage_bucket_iam_member.cache_fill.role == "roles/storage.objectViewer"
    error_message = "Unsigned public playback from a private origin requires the HTTPS-LB cache-fill identity."
  }
  assert {
    condition     = !google_compute_backend_bucket.media.cdn_policy[0].negative_caching && google_compute_backend_bucket.media.compression_mode == "DISABLED"
    error_message = "Missing revisions must not become negative cached and payloads must not be dynamically compressed."
  }
  assert {
    condition     = google_compute_backend_bucket.media.cdn_policy[0].cache_mode == "FORCE_CACHE_ALL" && length(google_compute_url_map.https.host_rule) == 1
    error_message = "The media-only stage must expose only its explicitly public media host."
  }
  assert {
    condition     = !google_compute_url_map.https.default_url_redirect[0].https_redirect && google_compute_url_map.http_redirect.default_url_redirect[0].https_redirect
    error_message = "Only the TargetHttpProxy URL map can enable https_redirect; the HTTPS map must retain its scheme."
  }
}

run "application_after_verified_publication" {
  command = plan
  variables {
    application = {
      image_digest            = "us-central1-docker.pkg.dev/rangle-example-stg/release/server@sha256:0000000000000000000000000000000000000000000000000000000000000000"
      catalog_sha256          = "1111111111111111111111111111111111111111111111111111111111111111"
      ui_root                 = "/app/web"
      cpu                     = 1
      memory                  = "512Mi"
      concurrency             = 16
      min_instances           = 0
      max_instances           = 2
      request_timeout_seconds = 30
    }
  }
  assert {
    condition     = google_cloud_run_v2_service.app[0].ingress == "INGRESS_TRAFFIC_INTERNAL_LOAD_BALANCER" && google_cloud_run_v2_service.app[0].default_uri_disabled
    error_message = "Public app requests must use the load balancer rather than a direct run.app origin."
  }
  assert {
    condition     = google_cloud_run_v2_service.app[0].template[0].volumes[0].gcs[0].read_only && contains(google_cloud_run_v2_service.app[0].template[0].containers[0].args, "/mnt/catalog/snapshots/1111111111111111111111111111111111111111111111111111111111111111.json")
    error_message = "Pin a specific catalog path on a read-only mount, with no mutable latest alias."
  }
  assert {
    condition     = contains(google_cloud_run_v2_service.app[0].template[0].containers[0].args, "https://stg.media.example.com") && !contains(google_cloud_run_v2_service.app[0].template[0].containers[0].args, "-asset-root")
    error_message = "Go must advertise external media URLs instead of proxying asset bodies."
  }
  assert {
    condition     = google_cloud_run_v2_service.app[0].scaling[0].max_instance_count == 2 && !google_compute_backend_service.app[0].enable_cdn && length(google_compute_url_map.https.host_rule) == 2
    error_message = "Keep bounded app scaling and separate non-CDN application routing."
  }
}

run "reject_shared_public_and_private_bucket" {
  command = plan
  variables {
    config = {
      project_id                = "rangle-example-stg"
      project_number            = "123456789012"
      region                    = "us-central1"
      name_prefix               = "rangle"
      app_domain                = "stg.app.example.com"
      media_domain              = "stg.media.example.com"
      media_bucket_name         = "same-example-bucket"
      catalog_bucket_name       = "same-example-bucket"
      input_bucket_name         = "rangle-example-stg-inputs"
      publisher_service_account = "publisher-stg@rangle-example-stg.iam.gserviceaccount.com"
      media_cache_ttl_seconds   = 86400
    }
  }
  expect_failures = [var.config]
}

run "reject_mutable_image_tag" {
  command = plan
  variables {
    application = {
      image_digest            = "us-central1-docker.pkg.dev/rangle-example-stg/release/server:latest"
      catalog_sha256          = "1111111111111111111111111111111111111111111111111111111111111111"
      ui_root                 = "/app/web"
      cpu                     = 1
      memory                  = "512Mi"
      concurrency             = 16
      min_instances           = 0
      max_instances           = 2
      request_timeout_seconds = 30
    }
  }
  expect_failures = [var.application]
}
