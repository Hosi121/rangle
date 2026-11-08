output "endpoints" {
  value = {
    app   = var.application == null ? null : "https://${var.config.app_domain}"
    media = "https://${var.config.media_domain}"
  }
}

output "dns_a_record" {
  value = {
    address = google_compute_global_address.edge.address
    names   = var.application == null ? [var.config.media_domain] : [var.config.app_domain, var.config.media_domain]
  }
}

output "buckets" {
  value = {
    media   = google_storage_bucket.media.name
    catalog = google_storage_bucket.private["catalog"].name
    inputs  = google_storage_bucket.private["inputs"].name
  }
}

output "release" {
  value = var.application == null ? null : {
    image_digest   = var.application.image_digest
    catalog_object = "snapshots/${var.application.catalog_sha256}.json"
    catalog_sha256 = var.application.catalog_sha256
  }
}
