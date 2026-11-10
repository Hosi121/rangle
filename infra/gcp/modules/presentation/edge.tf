resource "google_compute_backend_bucket" "media" {
  project              = var.project_id
  name                 = "${local.prefix}-media"
  bucket_name          = google_storage_bucket.private["media"].name
  enable_cdn           = true
  compression_mode     = "DISABLED"
  edge_security_policy = var.base_domain == null ? null : google_compute_security_policy.media[0].self_link
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
      # Public media uses one wildcard CORS policy for every Origin. Origin
      # cannot be configured as a backend-bucket cache-key request header.
      query_string_whitelist = ["generation"]
    }
  }
}

# Mutable discovery metadata has its own cache policy; model URLs remain immutable.
resource "google_compute_backend_bucket" "live_index" {
  count                = var.live_streaming_enabled ? 1 : 0
  project              = var.project_id
  name                 = "${local.prefix}-live-index"
  bucket_name          = google_storage_bucket.private["media"].name
  enable_cdn           = true
  compression_mode     = "DISABLED"
  edge_security_policy = google_compute_security_policy.media[0].self_link
  cdn_policy {
    cache_mode         = "FORCE_CACHE_ALL"
    default_ttl        = 1
    client_ttl         = 1
    negative_caching   = false
    serve_while_stale  = 0
    request_coalescing = true
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
    # CDN-generated 304 responses also need CORS headers. Replace the origin's
    # values so cached and uncached responses expose one consistent public policy.
    header_action {
      response_headers_to_add {
        header_name  = "Access-Control-Allow-Origin"
        header_value = "*"
        replace      = true
      }
      response_headers_to_add {
        header_name  = "Access-Control-Expose-Headers"
        header_value = "Accept-Ranges, Age, Cache-Control, Content-Length, Content-Range, Content-Type, Date, ETag, Last-Modified"
        replace      = true
      }
    }
    dynamic "path_rule" {
      for_each = var.live_streaming_enabled ? [1] : []
      content {
        paths   = ["/live-index/*"]
        service = google_compute_backend_bucket.live_index[0].id
      }
    }
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
      # Routing selector, not an authentication secret. Candidate requests use
      # the same application authorization and public LB as production traffic.
      route_rules {
        priority = 10
        service  = google_compute_backend_service.candidate[0].id
        match_rules {
          prefix_match = "/"
          header_matches {
            header_name = "X-Rangle-Candidate"
            exact_match = "ci-candidate"
          }
        }
      }
    }
  }
  # Google rejects URL map updates if these routing tests fail. Keep mutable
  # discovery off the day-long media cache and immutable payloads off the
  # one-second discovery cache.
  dynamic "test" {
    for_each = var.live_streaming_enabled ? {
      "/live-index/example.json"                         = google_compute_backend_bucket.live_index[0].id
      "/live/example/000001-aaaaaaaaaaaaaaaa/scene.json" = google_compute_backend_bucket.media.id
      "/live/example/000001-aaaaaaaaaaaaaaaa/model.rgs"  = google_compute_backend_bucket.media.id
    } : {}
    content {
      host    = local.media_host
      path    = test.key
      service = test.value
    }
  }
  # Google validates both the ordinary and candidate routes when applying the
  # URL map, including isolation from the media host and the OIDC sync path.
  dynamic "test" {
    for_each = local.app_enabled ? {
      public = {
        host    = local.app_host, path = "/api/v1/clips",
        service = google_compute_backend_service.app[0].id, selector = null
      }
      candidate = {
        host    = local.app_host, path = "/api/v1/clips",
        service = google_compute_backend_service.candidate[0].id, selector = "ci-candidate"
      }
      invalid_selector = {
        host    = local.app_host, path = "/health",
        service = google_compute_backend_service.app[0].id, selector = "another-tag"
      }
      scheduler = {
        host    = local.app_host, path = "/internal/generation/sync",
        service = google_compute_backend_service.app[0].id, selector = null
      }
      media = {
        host    = local.media_host, path = "/live/example/000001-aaaaaaaaaaaaaaaa/model.rgs",
        service = google_compute_backend_bucket.media.id, selector = "ci-candidate"
      }
    } : {}
    content {
      host    = test.value.host
      path    = test.value.path
      service = test.value.service
      dynamic "headers" {
        for_each = test.value.selector == null ? [] : [test.value.selector]
        content {
          name  = "X-Rangle-Candidate"
          value = headers.value
        }
      }
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
  security_policy       = google_compute_security_policy.app[0].self_link
  backend { group = google_compute_region_network_endpoint_group.app[0].id }
  # Serverless NEGs do not use a health_check or configurable backend timeout.
  log_config {
    enable      = true
    sample_rate = 0.1
  }
}

# A stable, CI-owned tag lets the serialized deploy workflow probe an unpromoted
# revision without network-admin permissions, extra DNS, or a second service.
resource "google_compute_region_network_endpoint_group" "candidate" {
  count                 = local.app_enabled ? 1 : 0
  project               = var.project_id
  name                  = "${local.prefix}-candidate"
  region                = var.region
  network_endpoint_type = "SERVERLESS"
  cloud_run {
    service = google_cloud_run_v2_service.app[0].name
    tag     = "ci-candidate"
  }
}

resource "google_compute_backend_service" "candidate" {
  count                 = local.app_enabled ? 1 : 0
  project               = var.project_id
  name                  = "${local.prefix}-candidate"
  protocol              = "HTTP"
  load_balancing_scheme = "EXTERNAL_MANAGED"
  enable_cdn            = false
  security_policy       = google_compute_security_policy.app[0].self_link
  backend { group = google_compute_region_network_endpoint_group.candidate[0].id }
  log_config {
    enable      = true
    sample_rate = 0.1
  }
}
