resource "google_compute_backend_bucket" "media" {
  project          = var.config.project_id
  name             = "${local.name}-media"
  bucket_name      = google_storage_bucket.media.name
  enable_cdn       = true
  compression_mode = "DISABLED"
  cdn_policy {
    # Private origin access uses the HTTPS load-balancer cache-fill identity.
    # Everything here is public, immutable media; never place private inputs here.
    cache_mode         = "FORCE_CACHE_ALL"
    default_ttl        = var.config.media_cache_ttl_seconds
    max_ttl            = var.config.media_cache_ttl_seconds
    client_ttl         = var.config.media_cache_ttl_seconds
    negative_caching   = false
    serve_while_stale  = 0
    request_coalescing = true
    cache_key_policy {
      # Only these parameters fragment the key. The publisher must not use
      # query strings for authorization, revisions, or content selection.
      query_string_whitelist = ["generation"]
      include_http_headers   = ["Origin"]
    }
  }
}

resource "google_storage_bucket_iam_member" "cache_fill" {
  bucket = google_storage_bucket.media.name
  role   = "roles/storage.objectViewer"
  member = "serviceAccount:service-${var.config.project_number}@https-lb.iam.gserviceaccount.com"
  # Google creates this identity after the project's first backend bucket.
  depends_on = [google_compute_backend_bucket.media]
}

resource "google_compute_global_address" "edge" {
  project    = var.config.project_id
  name       = "${local.name}-edge"
  ip_version = "IPV4"
  depends_on = [google_project_service.required]
}

resource "google_compute_managed_ssl_certificate" "edge" {
  for_each = merge(
    { media = var.config.media_domain },
    var.application == null ? {} : { app = var.config.app_domain }
  )
  project = var.config.project_id
  name    = "${local.name}-${each.key}-${substr(sha256(each.value), 0, 8)}"
  managed {
    domains = [each.value]
  }
  lifecycle { create_before_destroy = true }
  depends_on = [google_project_service.required]
}

resource "google_compute_ssl_policy" "edge" {
  project         = var.config.project_id
  name            = "${local.name}-tls"
  profile         = "MODERN"
  min_tls_version = "TLS_1_2"
  depends_on      = [google_project_service.required]
}

resource "google_compute_url_map" "https" {
  project = var.config.project_id
  name    = "${local.name}-https"
  # Unknown hosts get a canonical-host redirect, never an app fallback.
  default_url_redirect {
    host_redirect          = var.config.media_domain
    https_redirect         = false
    strip_query            = false
    redirect_response_code = "MOVED_PERMANENTLY_DEFAULT"
  }
  host_rule {
    hosts        = [var.config.media_domain]
    path_matcher = "media"
  }
  path_matcher {
    name            = "media"
    default_service = google_compute_backend_bucket.media.id
  }
  dynamic "host_rule" {
    for_each = var.application == null ? [] : [1]
    content {
      hosts        = [var.config.app_domain]
      path_matcher = "app"
    }
  }
  dynamic "path_matcher" {
    for_each = var.application == null ? [] : [1]
    content {
      name            = "app"
      default_service = google_compute_backend_service.app[0].id
    }
  }
  depends_on = [google_storage_bucket_iam_member.cache_fill]
}

resource "google_compute_target_https_proxy" "edge" {
  project          = var.config.project_id
  name             = "${local.name}-https"
  url_map          = google_compute_url_map.https.id
  ssl_certificates = [for cert in google_compute_managed_ssl_certificate.edge : cert.id]
  ssl_policy       = google_compute_ssl_policy.edge.id
}

resource "google_compute_global_forwarding_rule" "https" {
  project               = var.config.project_id
  name                  = "${local.name}-https"
  load_balancing_scheme = "EXTERNAL_MANAGED"
  ip_protocol           = "TCP"
  port_range            = "443"
  ip_address            = google_compute_global_address.edge.address
  target                = google_compute_target_https_proxy.edge.id
}

resource "google_compute_url_map" "http_redirect" {
  project = var.config.project_id
  name    = "${local.name}-redirect"
  default_url_redirect {
    https_redirect         = true
    strip_query            = false
    redirect_response_code = "MOVED_PERMANENTLY_DEFAULT"
  }
  depends_on = [google_project_service.required]
}

resource "google_compute_target_http_proxy" "redirect" {
  project = var.config.project_id
  name    = "${local.name}-redirect"
  url_map = google_compute_url_map.http_redirect.id
}

resource "google_compute_global_forwarding_rule" "http" {
  project               = var.config.project_id
  name                  = "${local.name}-http"
  load_balancing_scheme = "EXTERNAL_MANAGED"
  ip_protocol           = "TCP"
  port_range            = "80"
  ip_address            = google_compute_global_address.edge.address
  target                = google_compute_target_http_proxy.redirect.id
}
