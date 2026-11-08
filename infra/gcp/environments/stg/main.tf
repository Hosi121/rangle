provider "google" {
  project                     = var.config.project_id
  region                      = var.config.region
  impersonate_service_account = var.terraform_service_account
}

module "platform" {
  source                   = "../../modules/platform"
  environment              = "stg"
  deployer_service_account = var.terraform_service_account
  config                   = var.config
  application              = var.application
}

output "endpoints" { value = module.platform.endpoints }
output "dns_a_record" { value = module.platform.dns_a_record }
output "buckets" { value = module.platform.buckets }
output "release" { value = module.platform.release }
