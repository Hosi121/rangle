mock_provider "google" {
  mock_data "google_artifact_registry_repository" { defaults = { format = "DOCKER" } }
}
variables {
  project_id         = "rangle-example"
  project_number     = "123456789012"
  region             = "asia-northeast1"
  budget_currency    = "JPY"
  budget_stop_amount = 15000
}
run "domain_pending_private_foundation" {
  command = plan
  assert {
    condition     = length(google_cloud_run_v2_service.app) == 0 && length(google_compute_target_https_proxy.edge) == 0 && length(google_compute_global_forwarding_rule.https) == 0 && length(google_compute_managed_ssl_certificate.edge) == 0 && output.deployment.app_origin == null && output.deployment.media_origin == null
    error_message = "No invented domain, certificate, route, or dummy application may be published while the domain is pending."
  }
  assert {
    condition     = alltrue([for bucket in google_storage_bucket.private : bucket.public_access_prevention == "enforced"]) && !google_storage_bucket.private["catalog"].versioning[0].enabled && google_storage_bucket_iam_member.cache_fill.member == "serviceAccount:service-123456789012@https-lb.iam.gserviceaccount.com"
    error_message = "Private buckets and the correct private CDN cache-fill identity must be preserved."
  }
  assert {
    condition     = google_compute_backend_bucket.media.enable_cdn && !google_compute_backend_bucket.media.cdn_policy[0].negative_caching && google_compute_backend_bucket.media.compression_mode == "DISABLED" && contains(google_storage_bucket.private["media"].cors[0].origin, "*")
    error_message = "Anonymous public media must retain CDN/CORS without payload transformation or stale missing-object cache."
  }
  assert {
    condition = google_storage_bucket.private["media"].cors[0].method == tolist(["GET", "HEAD"]) && alltrue([
      for header in ["If-None-Match", "Range", "ETag", "Content-Range", "Content-Length"] : contains(google_storage_bucket.private["media"].cors[0].response_header, header)
    ]) && length(google_storage_bucket.private["catalog"].cors) == 0 && length(google_storage_bucket.private["inputs"].cors) == 0
    error_message = "Only public media may allow browser conditional/range requests; private inputs and catalogs must not inherit CORS."
  }
  assert {
    condition     = google_pubsub_topic_iam_member.billing_publisher.member == "serviceAccount:billing-budget-alert@system.gserviceaccount.com" && length(google_artifact_registry_repository_iam_member.image_writer.condition) == 0 && length(google_artifact_registry_repository.images) == 0
    error_message = "Keep the live-verified Billing publisher and existing scoped Artifact Registry policy."
  }
  assert {
    condition     = contains(keys(yamldecode(google_workflows_workflow.guard.source_contents)), "delete_compute_resource") && !strcontains(google_workflows_workflow.guard.source_contents, "firebasehosting") && strcontains(google_workflows_workflow.guard.source_contents, "observed_cost >= 15000") && strcontains(google_workflows_workflow.guard.source_contents, "global/addresses/rangle-stg-edge")
    error_message = "Budget/deadline shutdown must remove owned Compute ingress without Firebase operations."
  }
}
run "configured_domain_media_and_budget" {
  command = plan
  variables {
    base_domain = "presentation.example.com"
    budget      = { billing_account_id = "000000-000000-000000", amount = 30000, start_date = "2026-11-08", end_date = "2026-11-15" }
  }
  assert {
    condition     = length(google_compute_global_forwarding_rule.https) == 1 && length(google_compute_global_forwarding_rule.http) == 1 && length(google_cloud_run_v2_service.app) == 0 && output.deployment.media_origin == "https://media.presentation.example.com" && length(output.deployment.dns_records) == 2
    error_message = "The supplied domain enables edge DNS/HTTPS, while an absent verified image/catalog leaves the app uncreated."
  }
  assert {
    condition     = !google_compute_url_map.https[0].default_url_redirect[0].https_redirect && google_compute_url_map.http_redirect[0].default_url_redirect[0].https_redirect && google_compute_ssl_policy.edge[0].min_tls_version == "TLS_1_2"
    error_message = "HTTPS must retain its scheme; only HTTP redirects to HTTPS with TLS 1.2 minimum."
  }
  assert {
    condition = { for header in one([for matcher in google_compute_url_map.https[0].path_matcher : matcher if matcher.name == "media"]).header_action[0].response_headers_to_add : header.header_name => { value = header.header_value, replace = header.replace } } == {
      Access-Control-Allow-Origin   = { value = "*", replace = true }
      Access-Control-Expose-Headers = { value = "Accept-Ranges, Age, Cache-Control, Content-Length, Content-Range, Content-Type, Date, ETag, Last-Modified", replace = true }
    }
    error_message = "CDN-generated responses need one public CORS policy with the headers used by browser and Worker readers."
  }
  assert {
    condition     = google_billing_budget.presentation[0].budget_filter[0].projects == toset(["projects/123456789012"]) && google_billing_budget.presentation[0].amount[0].specified_amount[0].currency_code == "JPY"
    error_message = "Keep the optional budget project-scoped in the verified currency."
  }
}
run "configured_domain_bounded_app" {
  command = plan
  variables {
    base_domain = "presentation.example.com"
    application = {
      image_digest   = "asia-northeast1-docker.pkg.dev/rangle-example/rangle-stg/server@sha256:0000000000000000000000000000000000000000000000000000000000000000"
      catalog_sha256 = "1111111111111111111111111111111111111111111111111111111111111111"
    }
  }
  assert {
    condition     = google_cloud_run_v2_service.app[0].ingress == "INGRESS_TRAFFIC_INTERNAL_LOAD_BALANCER" && !google_cloud_run_v2_service.app[0].default_uri_disabled && google_cloud_run_v2_service.app[0].scaling[0].max_instance_count == 2 && google_cloud_run_v2_service.app[0].scaling[0].min_instance_count == 0 && google_cloud_run_v2_service.app[0].template[0].max_instance_request_concurrency == 8
    error_message = "Public app requests must enter through the LB; keep provisional min0/max2 and concurrency8."
  }
  assert {
    condition     = google_cloud_run_v2_service.app[0].template[0].scaling[0].min_instance_count == 0 && google_cloud_run_v2_service.app[0].template[0].scaling[0].max_instance_count == 2 && google_cloud_run_v2_service.app[0].template[0].containers[0].resources[0].limits == tomap({ cpu = "1", memory = "512Mi" }) && google_cloud_run_v2_service.app[0].template[0].containers[0].resources[0].cpu_idle && !google_cloud_run_v2_service.app[0].template[0].containers[0].resources[0].startup_cpu_boost && google_cloud_run_v2_service.app[0].template[0].timeout == "30s"
    error_message = "Bound tagged app revisions too, retaining request billing, 1 CPU/512MiB, and the 30s timeout."
  }
  assert {
    condition     = google_cloud_run_v2_service.app[0].template[0].volumes[0].gcs[0].read_only && contains(google_cloud_run_v2_service.app[0].template[0].containers[0].args, "https://media.presentation.example.com") && google_cloud_run_v2_service.app[0].template[0].containers[0].startup_probe[0].http_get[0].path == "/healthz"
    error_message = "The app must pin readonly catalog, advertise the chosen media origin and verify its HTTP startup probe."
  }
  assert {
    condition     = !google_compute_backend_service.app[0].enable_cdn && google_compute_backend_service.app[0].health_checks == null && length(google_compute_url_map.https[0].host_rule) == 2 && google_compute_region_network_endpoint_group.app[0].network_endpoint_type == "SERVERLESS"
    error_message = "Route the uncached application via a serverless NEG, which does not use Compute health checks."
  }
  assert {
    condition     = google_compute_region_network_endpoint_group.candidate[0].cloud_run[0].tag == "ci-candidate" && !google_compute_backend_service.candidate[0].enable_cdn && google_compute_backend_service.candidate[0].health_checks == null
    error_message = "Probe the reserved candidate tag through an uncached serverless backend."
  }
  assert {
    condition     = one([for matcher in google_compute_url_map.https[0].path_matcher : matcher if matcher.name == "app"]).route_rules[0].match_rules[0].header_matches[0].header_name == "X-Rangle-Candidate" && one([for matcher in google_compute_url_map.https[0].path_matcher : matcher if matcher.name == "app"]).route_rules[0].match_rules[0].header_matches[0].exact_match == "ci-candidate"
    error_message = "Candidate probes must use the app host and the explicit routing selector."
  }
  assert {
    condition     = length(one([for matcher in google_compute_url_map.https[0].path_matcher : matcher if matcher.name == "app"]).header_action) == 0
    error_message = "The public media CORS override must not affect app, authenticated API, or candidate responses."
  }
  assert {
    condition     = strcontains(google_service_account_iam_member.github_deployer.condition[0].expression, "2026-11-14T15:00:00Z") && strcontains(google_iam_workload_identity_pool_provider.github.attribute_condition, "assertion.repository_id == '1448803636'") && strcontains(google_project_iam_member.stop_forwarding_rules.condition[0].expression, "global/forwardingRules/rangle-stg-https")
    error_message = "Preserve exact WIF/expiry and restrict forwarding-rule mutation to owned names."
  }
}
