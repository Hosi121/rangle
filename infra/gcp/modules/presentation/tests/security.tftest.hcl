mock_provider "google" {
  mock_data "google_artifact_registry_repository" { defaults = { format = "DOCKER" } }
}
variables {
  project_id             = "rangle-example"
  project_number         = "123456789012"
  region                 = "asia-northeast1"
  base_domain            = "presentation.example.com"
  live_streaming_enabled = true
  budget_currency        = "JPY"
  budget_stop_amount     = 15000
  application = {
    image_digest      = "asia-northeast1-docker.pkg.dev/rangle-example/rangle-stg/server@sha256:0000000000000000000000000000000000000000000000000000000000000000"
    catalog_sha256    = "1111111111111111111111111111111111111111111111111111111111111111"
    community_enabled = true
  }
}

run "all_public_backends_enforce_the_correct_policy" {
  command = plan
  override_resource {
    target          = google_compute_security_policy.app[0]
    override_during = plan
    values          = { self_link = "https://www.googleapis.com/compute/v1/projects/rangle-example/global/securityPolicies/rangle-stg-app-security" }
  }
  override_resource {
    target          = google_compute_security_policy.media[0]
    override_during = plan
    values          = { self_link = "https://www.googleapis.com/compute/v1/projects/rangle-example/global/securityPolicies/rangle-stg-media-security" }
  }
  assert {
    condition     = google_compute_security_policy.app[0].type == "CLOUD_ARMOR" && google_compute_backend_service.app[0].security_policy == google_compute_security_policy.app[0].self_link && google_compute_backend_service.candidate[0].security_policy == google_compute_security_policy.app[0].self_link
    error_message = "Both ordinary and header-selected candidate traffic must enforce the same backend security policy."
  }
  assert {
    condition     = google_compute_security_policy.media[0].type == "CLOUD_ARMOR_EDGE" && google_compute_backend_bucket.media.edge_security_policy == google_compute_security_policy.media[0].self_link && google_compute_backend_bucket.live_index[0].edge_security_policy == google_compute_security_policy.media[0].self_link
    error_message = "Both immutable media and mutable live-index cache backends must enforce the supported edge policy."
  }
  assert {
    condition = { for rule in google_compute_security_policy.app[0].rule : tostring(rule.priority) => {
      action = rule.action
      key    = rule.rate_limit_options[0].enforce_on_key
      count  = rule.rate_limit_options[0].rate_limit_threshold[0].count
      period = rule.rate_limit_options[0].rate_limit_threshold[0].interval_sec
      exceed = rule.rate_limit_options[0].exceed_action
      } if rule.action == "throttle" } == {
      "1000" = { action = "throttle", key = "IP", count = 30, period = 60, exceed = "deny(429)" }
      "1100" = { action = "throttle", key = "IP", count = 120, period = 60, exceed = "deny(429)" }
      "1200" = { action = "throttle", key = "IP", count = 600, period = 60, exceed = "deny(429)" }
    }
    error_message = "Use separate per-peer auth/write/read limits, never a shared global limit or a spoofable forwarded-IP key."
  }
  assert {
    condition     = one([for rule in google_compute_security_policy.app[0].rule : rule.match[0].expr[0].expression if rule.priority == 1000]) == "request.method == 'POST' && request.path.urlDecode().matches('^/api/v1/(?:auth/(?:login|register)|publisher-auth/login)$')" && one([for rule in google_compute_security_policy.app[0].rule : rule.match[0].expr[0].expression if rule.priority == 1100]) == "request.path.urlDecode().startsWith('/api/') && request.method.matches('^(?:POST|PUT|DELETE)$')" && one([for rule in google_compute_security_policy.app[0].rule : rule.match[0].expr[0].expression if rule.priority == 1200]) == "request.path.urlDecode().startsWith('/api/') && request.method.matches('^(?:GET|HEAD)$')"
    error_message = "Auth must match its strict limit before ordinary mutations; public reads need their own polling limit."
  }
  assert {
    condition     = one([for rule in google_compute_security_policy.app[0].rule : rule.match[0].expr[0].expression if rule.priority == 200]) == "request.method.matches('^(?:GET|HEAD)$') && request.path.urlDecode().startsWith('/api/') && evaluatePreconfiguredWaf('protocolattack-v33-stable', {'sensitivity': 1})" && one([for rule in google_compute_security_policy.app[0].rule : rule.action if rule.priority == 200]) == "deny(403)" && alltrue([for rule in google_compute_security_policy.app[0].rule : rule.priority > 200 if rule.action == "throttle"])
    error_message = "Run scoped protocol WAF before throttle allow actions, with no inspection of password/comment writes."
  }
  assert {
    condition     = one([for rule in google_compute_security_policy.app[0].rule : rule.action if rule.priority == 2147483647]) == "allow" && one([for rule in google_compute_security_policy.app[0].rule : rule.match[0].config[0].src_ip_ranges if rule.priority == 2147483647]) == toset(["*"])
    error_message = "Preserve worldwide public browsing, CORS preflight, app assets, and authenticated scheduler sync."
  }
  assert {
    condition     = length(google_compute_security_policy.media[0].rule) == 2 && one([for rule in google_compute_security_policy.media[0].rule : rule.match[0].expr[0].expression if rule.priority == 1000]) == "request.method.matches('^(?:GET|HEAD|OPTIONS)$') && request.path.matches('^/(?:assets/|manifests/|generated/|live/|live-index/|previews/|ATTRIBUTION[.]txt$)')" && one([for rule in google_compute_security_policy.media[0].rule : rule.action if rule.priority == 2147483647]) == "deny(403)" && alltrue([for rule in google_compute_security_policy.media[0].rule : length(rule.rate_limit_options) == 0])
    error_message = "Allow public read paths and CORS with Range untouched, reject root/unapproved paths, and do not configure unsupported edge rate limiting."
  }
  assert {
    condition = alltrue([
      for path in ["/assets/clip/revision/full/model.rgs", "/manifests/clip/revision.json", "/generated/job/scene.json", "/live/session/model.rgs", "/live-index/session.json", "/previews/preset.webm", "/ATTRIBUTION.txt"] :
      can(regex(regex("request.path.matches\\('([^']+)'\\)", one([for rule in google_compute_security_policy.media[0].rule : rule.match[0].expr[0].expression if rule.priority == 1000]))[0], path))
      ]) && alltrue([
      for path in ["/", "/catalog/index.json", "/inputs/private.npz", "/.env", "/ATTRIBUTION.txt.bak", "/ATTRIBUTION.txt/extra", "/previews-private/secret.webm"] :
      !can(regex(regex("request.path.matches\\('([^']+)'\\)", one([for rule in google_compute_security_policy.media[0].rule : rule.match[0].expr[0].expression if rule.priority == 1000]))[0], path))
    ])
    error_message = "The media path regex must preserve current published URLs, including root attribution, without allowing listing or neighboring/private namespaces."
  }
  assert {
    condition     = google_compute_backend_bucket.media.enable_cdn && google_compute_backend_bucket.media.cdn_policy[0].default_ttl == 86400 && google_compute_backend_bucket.live_index[0].cdn_policy[0].default_ttl == 1 && google_cloud_run_v2_service.app[0].scaling[0].min_instance_count == 0 && google_cloud_run_v2_service.app[0].scaling[0].max_instance_count == 2 && var.deadline == "2026-11-14T15:00:00Z"
    error_message = "Security policy attachments must preserve media caching, bounded capacity, and the approved shutdown deadline."
  }
}

run "community_can_recover_without_replacing_the_database" {
  command = plan
  assert {
    condition     = google_firestore_database.community[0].name == "rangle-stg" && google_firestore_database.community[0].location_id == "asia-northeast1" && google_firestore_database.community[0].point_in_time_recovery_enablement == "POINT_IN_TIME_RECOVERY_ENABLED" && google_firestore_database.community[0].delete_protection_state == "DELETE_PROTECTION_ENABLED" && google_firestore_database.community[0].deletion_policy == "PREVENT"
    error_message = "Enable PITR in-place on the existing protected Tokyo database."
  }
  assert {
    condition     = length(google_firestore_backup_schedule.community_daily) == 1 && google_firestore_backup_schedule.community_daily[0].database == "rangle-stg" && google_firestore_backup_schedule.community_daily[0].retention == "604800s" && length(google_firestore_backup_schedule.community_daily[0].daily_recurrence) == 1 && length(google_firestore_backup_schedule.community_daily[0].weekly_recurrence) == 0
    error_message = "Back up only the existing named community DB daily with an explicit seven-day retention."
  }
  assert {
    condition = { for service, config in google_project_iam_audit_config.security : service => toset([for log in config.audit_log_config : log.log_type]) } == {
      "storage.googleapis.com"   = toset(["ADMIN_READ", "DATA_WRITE"])
      "iam.googleapis.com"       = toset(["ADMIN_READ", "DATA_READ"])
      "datastore.googleapis.com" = toset(["ADMIN_READ", "DATA_WRITE"])
    }
    error_message = "Audit Storage/Firestore writes/admin reads and IAM credentials via IAM; avoid public data-read volume and allServices."
  }
  assert {
    condition     = alltrue(flatten([for config in google_project_iam_audit_config.security : [for log in config.audit_log_config : length(coalesce(log.exempted_members, toset([]))) == 0]]))
    error_message = "Security audit logs must not silently exempt runtime or deploy identities."
  }
}

run "no_policies_or_database_backups_for_an_unpublished_foundation" {
  command = plan
  variables {
    base_domain            = null
    application            = null
    live_streaming_enabled = false
  }
  assert {
    condition     = length(google_compute_security_policy.app) == 0 && length(google_compute_security_policy.media) == 0 && google_compute_backend_bucket.media.edge_security_policy == null && length(google_firestore_backup_schedule.community_daily) == 0
    error_message = "An unpublished foundation must not add idle Armor policies or a database backup schedule."
  }
  assert {
    condition     = toset(keys(google_project_iam_audit_config.security)) == toset(["storage.googleapis.com", "iam.googleapis.com"])
    error_message = "Keep selected storage and credential audit coverage even when the application/community is disabled."
  }
}

run "generation_still_audits_firestore_writes" {
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
    condition     = toset([for log in google_project_iam_audit_config.security["datastore.googleapis.com"].audit_log_config : log.log_type]) == toset(["ADMIN_READ", "DATA_WRITE"]) && length(google_firestore_backup_schedule.generation_daily) == 1
    error_message = "The isolated generation database needs service-wide Firestore audit coverage and recovery."
  }
}

run "guard_has_only_the_required_cleanup_permissions" {
  command = plan
  override_resource {
    target          = google_service_account.guard
    override_during = plan
    values          = { email = "rangle-stg-guard@rangle-example.iam.gserviceaccount.com" }
  }
  assert {
    condition     = google_project_iam_custom_role.detach_service_security.permissions == toset(["compute.backendServices.get", "compute.backendServices.setSecurityPolicy"]) && google_project_iam_member.detach_service_security.member == "serviceAccount:${google_service_account.guard.email}" && google_project_iam_member.detach_service_security.condition[0].expression == "resource.name in ['projects/rangle-example/global/backendServices/rangle-stg-app', 'projects/rangle-example/global/backendServices/rangle-stg-candidate']"
    error_message = "Grant only guard's fixed app/candidate get/detach permissions with supported backend service IAM name conditions."
  }
  assert {
    condition     = google_project_iam_custom_role.cleanup_security.permissions == toset(["compute.backendBuckets.get", "compute.backendBuckets.setSecurityPolicy", "compute.securityPolicies.get", "compute.securityPolicies.delete"]) && google_project_iam_member.cleanup_security.member == "serviceAccount:${google_service_account.guard.email}" && length(google_project_iam_member.cleanup_security.condition) == 0
    error_message = "Bucket/policy cleanup needs four explicit project-wide permissions for guard only, without unsupported IAM name-condition claims or policy create/update/use."
  }
  assert {
    condition     = index([for step in yamldecode(google_workflows_workflow.guard.source_contents).main.steps : keys(step)[0]], "delete_owned_edge") < index([for step in yamldecode(google_workflows_workflow.guard.source_contents).main.steps : keys(step)[0]], "cleanup_owned_security") && index([for step in yamldecode(google_workflows_workflow.guard.source_contents).main.steps : keys(step)[0]], "cleanup_owned_security") < index([for step in yamldecode(google_workflows_workflow.guard.source_contents).main.steps : keys(step)[0]], "stop_owned_service")
    error_message = "Attempt ingress shutdown before Armor cleanup, and continue to public Run shutdown after cleanup."
  }
}
