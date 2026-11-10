provider "google" {
  project               = var.project_id
  region                = var.region
  user_project_override = true
  billing_project       = var.project_id
}
module "presentation" {
  source                     = "../../modules/presentation"
  project_id                 = var.project_id
  project_number             = var.project_number
  region                     = var.region
  application                = var.application
  generation_sync_enabled    = var.generation_sync_enabled
  live_streaming_enabled     = var.live_streaming_enabled
  deadline                   = var.deadline
  budget_currency            = var.budget_currency
  budget_stop_amount         = var.budget_stop_amount
  budget_display_name        = var.budget_display_name
  budget                     = var.budget
  create_artifact_repository = var.create_artifact_repository
  providers                  = { google = google }
  base_domain                = var.base_domain
}
output "deployment" { value = module.presentation.deployment }
output "budget_guard" { value = module.presentation.budget_guard }
output "observability" { value = module.presentation.observability }
