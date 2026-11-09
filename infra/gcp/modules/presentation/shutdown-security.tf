# backendServices support resource.name conditions. Detach uses an empty
# securityPolicy reference, so no permission to attach/use another policy is
# granted. See the REST setSecurityPolicy permission and request-body reference.
resource "google_project_iam_custom_role" "detach_service_security" {
  project     = var.project_id
  role_id     = "rangleStgDetachServiceSecurity"
  title       = "Detach owned presentation backend service policies"
  permissions = ["compute.backendServices.get", "compute.backendServices.setSecurityPolicy"]
  depends_on  = [google_project_service.required]
}
resource "google_project_iam_member" "detach_service_security" {
  project = var.project_id
  role    = google_project_iam_custom_role.detach_service_security.name
  member  = "serviceAccount:${google_service_account.guard.email}"
  condition {
    title      = "only_presentation_backend_services"
    expression = "resource.name in ['projects/${var.project_id}/global/backendServices/${local.prefix}-app', 'projects/${var.project_id}/global/backendServices/${local.prefix}-candidate']"
  }
}

# Backend buckets and service-level security policies are absent from Google's
# resource.name support table. These four permissions are project-wide and go
# only to the guard. Workflow target lists provide the narrower code boundary.
# No policy create/update/use, bucket delete, or unrelated identity grants.
resource "google_project_iam_custom_role" "cleanup_security" {
  project = var.project_id
  role_id = "rangleStgCleanupSecurity"
  title   = "Detach bucket policies and delete presentation Armor policies"
  permissions = [
    "compute.backendBuckets.get", "compute.backendBuckets.setSecurityPolicy",
    "compute.securityPolicies.get", "compute.securityPolicies.delete"
  ]
  depends_on = [google_project_service.required]
}
resource "google_project_iam_member" "cleanup_security" {
  project = var.project_id
  role    = google_project_iam_custom_role.cleanup_security.name
  member  = "serviceAccount:${google_service_account.guard.email}"
}
