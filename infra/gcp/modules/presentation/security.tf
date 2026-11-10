# Service-level policies use Cloud Armor Standard pay-as-you-go. Do not create
# hierarchical policies or Enterprise subscriptions for this bounded deployment.
# matches() forbids capture groups: use (?:...) for regex alternations, as in
# Google's Cloud Armor host-matching example. Mock plans cannot compile CEL.
resource "google_compute_security_policy" "app" {
  count       = local.app_enabled ? 1 : 0
  project     = var.project_id
  name        = "${local.prefix}-app-security"
  description = "Public app methods, read-only protocol WAF, and per-peer API limits."
  type        = "CLOUD_ARMOR"

  rule {
    priority    = 100
    action      = "deny(403)"
    description = "Reject methods outside the app's HTTP contract."
    match {
      expr { expression = "!request.method.matches('^(?:GET|HEAD|OPTIONS|POST|PUT|DELETE)$')" }
    }
  }

  # Inspect read-only API requests, before the rate rules' allow action. Never
  # inspect comments or credentials: harmless text and passwords may contain
  # script-like strings. JSON writes keep the application's validation/escaping.
  rule {
    priority    = 200
    action      = "deny(403)"
    description = "Sensitivity-one protocol attack signatures on API reads only."
    match {
      expr { expression = "request.method.matches('^(?:GET|HEAD)$') && request.path.urlDecode().startsWith('/api/') && evaluatePreconfiguredWaf('protocolattack-v33-stable', {'sensitivity': 1})" }
    }
  }

  rule {
    priority    = 1000
    action      = "throttle"
    description = "Limit password verification and account creation per peer IP."
    match {
      expr { expression = "request.method == 'POST' && request.path.urlDecode().matches('^/api/v1/(?:auth/(?:login|register)|publisher-auth/login)$')" }
    }
    rate_limit_options {
      conform_action = "allow"
      exceed_action  = "deny(429)"
      # IP is the connecting peer. XFF_IP/USER_IP trust client-supplied headers.
      enforce_on_key = "IP"
      rate_limit_threshold {
        count        = 30
        interval_sec = 60
      }
    }
  }

  rule {
    priority    = 1100
    action      = "throttle"
    description = "Limit remaining API mutations per peer IP."
    match {
      expr { expression = "request.path.urlDecode().startsWith('/api/') && request.method.matches('^(?:POST|PUT|DELETE)$')" }
    }
    rate_limit_options {
      conform_action = "allow"
      exceed_action  = "deny(429)"
      enforce_on_key = "IP"
      rate_limit_threshold {
        count        = 120
        interval_sec = 60
      }
    }
  }

  rule {
    priority    = 1200
    action      = "throttle"
    description = "Bound public API polling per peer IP without a global quota."
    match {
      expr { expression = "request.path.urlDecode().startsWith('/api/') && request.method.matches('^(?:GET|HEAD)$')" }
    }
    rate_limit_options {
      conform_action = "allow"
      exceed_action  = "deny(429)"
      enforce_on_key = "IP"
      rate_limit_threshold {
        count        = 600
        interval_sec = 60
      }
    }
  }

  rule {
    priority    = 2147483647
    action      = "allow"
    description = "Keep worldwide browsing, static assets, and managed sync."
    match {
      versioned_expr = "SRC_IPS_V1"
      config { src_ip_ranges = ["*"] }
    }
  }

  depends_on = [google_project_service.required]
}

# Backend buckets support edge policies, not backend WAF/rate limiting. Filter
# before cache lookup; never rely on Referer/Origin or client-forwarded IPs.
resource "google_compute_security_policy" "media" {
  count       = var.base_domain == null ? 0 : 1
  project     = var.project_id
  name        = "${local.prefix}-media-security"
  description = "Public read-only media namespaces; no origin bucket listing."
  type        = "CLOUD_ARMOR_EDGE"

  rule {
    priority    = 1000
    action      = "allow"
    description = "Allow public immutable media and mutable live indexes with CORS."
    match {
      expr { expression = "request.method.matches('^(?:GET|HEAD|OPTIONS)$') && request.path.matches('^/(?:assets/|manifests/|generated/|live/|live-index/|previews/|ATTRIBUTION[.]txt$)')" }
    }
  }

  rule {
    priority    = 2147483647
    action      = "deny(403)"
    description = "Deny other methods and non-public object namespaces."
    match {
      versioned_expr = "SRC_IPS_V1"
      config { src_ip_ranges = ["*"] }
    }
  }

  depends_on = [google_project_service.required]
}

locals {
  # Audit configs are project-wide, not per database/bucket. Avoid object-read
  # and public Firestore-read volume; retain authenticated write/admin evidence.
  # IAM Credentials requires enabling IAM API audit logs, not an independent
  # iamcredentials.googleapis.com config (see docs/security-hardening.md).
  security_audit_services = merge({
    "storage.googleapis.com" = ["ADMIN_READ", "DATA_WRITE"]
    "iam.googleapis.com"     = ["ADMIN_READ", "DATA_READ"]
    }, (local.community_enabled || local.generation_enabled) ? {
    "datastore.googleapis.com" = ["ADMIN_READ", "DATA_WRITE"]
  } : {})
}

# Authoritative only for each selected service's audit config. This resource
# preserves role bindings and audit configs for unrelated services.
resource "google_project_iam_audit_config" "security" {
  for_each = local.security_audit_services
  project  = var.project_id
  service  = each.key
  dynamic "audit_log_config" {
    for_each = toset(each.value)
    content { log_type = audit_log_config.value }
  }
}
