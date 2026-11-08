resource "google_compute_backend_bucket" "media" {
  project          = var.project_id
  name             = "${local.prefix}-media"
  bucket_name      = google_storage_bucket.private["media"].name
  enable_cdn       = true
  compression_mode = "DISABLED"
  cdn_policy {
    # Private origin access uses the HTTPS load-balancer cache-fill identity.
    # Everything here is public, immutable media; never place private inputs here.
    cache_mode = "FORCE_CACHE_ALL"
    # FORCE_CACHE_ALL uses default_ttl; the API omits max_ttl in this mode.
    default_ttl        = 86400
    client_ttl         = 86400
    negative_caching   = false
    serve_while_stale  = 0
    request_coalescing = true
    cache_key_policy {
      # Only these parameters fragment the key. The publisher must not use
      # query strings for authorization, revisions, or content selection.
      # Cloud CDN handles the origin's Vary: Origin; Origin is forbidden here.
      query_string_whitelist = ["generation"]
    }
  }
}

resource "google_storage_bucket_iam_member" "cache_fill" {
  bucket = google_storage_bucket.private["media"].name
  role   = "roles/storage.objectViewer"
  member = "serviceAccount:service-${var.project_number}@https-lb.iam.gserviceaccount.com"
  # Google creates this identity after the project's first backend bucket.
  depends_on = [google_compute_backend_bucket.media]
}

resource "google_compute_global_address" "edge" {
  project    = var.project_id
  name       = "${local.prefix}-edge"
  ip_version = "IPV4"
  depends_on = [google_project_service.required]
}

resource "google_compute_managed_ssl_certificate" "edge" {
  for_each = var.base_domain == null ? {} : { media = local.media_host, app = local.app_host }
  project  = var.project_id
  name     = "${local.prefix}-${each.key}-${substr(sha256(each.value), 0, 8)}"
  managed {
    domains = [each.value]
  }
  lifecycle { create_before_destroy = true }
  depends_on = [google_project_service.required]
}

resource "google_compute_ssl_policy" "edge" {
  count           = var.base_domain == null ? 0 : 1
  project         = var.project_id
  name            = "${local.prefix}-tls"
  profile         = "MODERN"
  min_tls_version = "TLS_1_2"
  depends_on      = [google_project_service.required]
}

resource "google_compute_url_map" "https" {
  count   = var.base_domain == null ? 0 : 1
  project = var.project_id
  name    = "${local.prefix}-https"
  # Unknown hosts get a canonical-host redirect, never an app fallback.
  default_url_redirect {
    host_redirect          = local.media_host
    https_redirect         = false
    strip_query            = false
    redirect_response_code = "MOVED_PERMANENTLY_DEFAULT"
  }
  host_rule {
    hosts        = [local.media_host]
    path_matcher = "media"
  }
  path_matcher {
    name            = "media"
    default_service = google_compute_backend_bucket.media.id
  }
  dynamic "host_rule" {
    for_each = local.app_enabled ? [1] : []
    content {
      hosts        = [local.app_host]
      path_matcher = "app"
    }
  }
  dynamic "path_matcher" {
    for_each = local.app_enabled ? [1] : []
    content {
      name            = "app"
      default_service = google_compute_backend_service.app[0].id
    }
  }
  depends_on = [google_storage_bucket_iam_member.cache_fill]
}

resource "google_compute_target_https_proxy" "edge" {
  count            = var.base_domain == null ? 0 : 1
  project          = var.project_id
  name             = "${local.prefix}-https"
  url_map          = google_compute_url_map.https[0].id
  ssl_certificates = [for cert in google_compute_managed_ssl_certificate.edge : cert.id]
  ssl_policy       = google_compute_ssl_policy.edge[0].id
}

resource "google_compute_global_forwarding_rule" "https" {
  count                 = var.base_domain == null ? 0 : 1
  project               = var.project_id
  name                  = "${local.prefix}-https"
  load_balancing_scheme = "EXTERNAL_MANAGED"
  ip_protocol           = "TCP"
  port_range            = "443"
  ip_address            = google_compute_global_address.edge.address
  target                = google_compute_target_https_proxy.edge[0].id
}

resource "google_compute_url_map" "http_redirect" {
  count   = var.base_domain == null ? 0 : 1
  project = var.project_id
  name    = "${local.prefix}-redirect"
  default_url_redirect {
    https_redirect         = true
    strip_query            = false
    redirect_response_code = "MOVED_PERMANENTLY_DEFAULT"
  }
  depends_on = [google_project_service.required]
}

resource "google_compute_target_http_proxy" "redirect" {
  count   = var.base_domain == null ? 0 : 1
  project = var.project_id
  name    = "${local.prefix}-redirect"
  url_map = google_compute_url_map.http_redirect[0].id
}

resource "google_compute_global_forwarding_rule" "http" {
  count                 = var.base_domain == null ? 0 : 1
  project               = var.project_id
  name                  = "${local.prefix}-http"
  load_balancing_scheme = "EXTERNAL_MANAGED"
  ip_protocol           = "TCP"
  port_range            = "80"
  ip_address            = google_compute_global_address.edge.address
  target                = google_compute_target_http_proxy.redirect[0].id
}

resource "google_compute_region_network_endpoint_group" "app" {
  count                 = local.app_enabled ? 1 : 0
  project               = var.project_id
  name                  = "${local.prefix}-app"
  region                = var.region
  network_endpoint_type = "SERVERLESS"
  cloud_run { service = google_cloud_run_v2_service.app[0].name }
}

resource "google_compute_backend_service" "app" {
  count                 = local.app_enabled ? 1 : 0
  project               = var.project_id
  name                  = "${local.prefix}-app"
  protocol              = "HTTP"
  load_balancing_scheme = "EXTERNAL_MANAGED"
  enable_cdn            = false
  backend { group = google_compute_region_network_endpoint_group.app[0].id }
  # Serverless NEGs do not use a health_check or configurable backend timeout.
  log_config {
    enable      = true
    sample_rate = 0.1
  }
}
