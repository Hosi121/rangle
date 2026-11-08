resource "google_iam_workload_identity_pool" "github" {
  project                   = var.project_id
  workload_identity_pool_id = "rangle-stg-github"
  display_name              = "rangle stg deployment only"
  depends_on                = [google_project_service.required]
}
resource "google_iam_workload_identity_pool_provider" "github" {
  project                            = var.project_id
  workload_identity_pool_id          = google_iam_workload_identity_pool.github.workload_identity_pool_id
  workload_identity_pool_provider_id = "deploy-stg"
  attribute_mapping = {
    "google.subject"                = "assertion.sub"
    "attribute.repository_id"       = "assertion.repository_id"
    "attribute.repository_owner_id" = "assertion.repository_owner_id"
    "attribute.ref"                 = "assertion.ref"
    "attribute.workflow_ref"        = "assertion.workflow_ref"
    "attribute.event_name"          = "assertion.event_name"
  }
  attribute_condition = join(" && ", [
    "assertion.repository_id == '1448803636'",
    "assertion.repository_owner_id == '128687243'",
    "assertion.ref == 'refs/heads/stg'",
    "assertion.workflow_ref == 'Hosi121/rangle/.github/workflows/deploy-stg.yml@refs/heads/stg'",
    "assertion.event_name in ['push', 'workflow_dispatch']"
  ])
  oidc { issuer_uri = "https://token.actions.githubusercontent.com" }
}
resource "google_service_account_iam_member" "github_deployer" {
  service_account_id = google_service_account.deployer.name
  role               = "roles/iam.workloadIdentityUser"
  member             = "principalSet://iam.googleapis.com/projects/${var.project_number}/locations/global/workloadIdentityPools/${google_iam_workload_identity_pool.github.workload_identity_pool_id}/attribute.repository_id/1448803636"
  condition {
    title      = "presentation_not_expired"
    expression = "request.time < timestamp('${var.deadline}')"
  }
}
