mock_provider "google" {}

variables {
  project_id                = "rangle-example-state"
  region                    = "us-central1"
  bootstrap_service_account = "infra-admin@rangle-example-state.iam.gserviceaccount.com"
  environments = {
    stg = {
      state_bucket_name         = "rangle-example-state-stg"
      terraform_service_account = "terraform-stg@rangle-example-stg.iam.gserviceaccount.com"
    }
    main = {
      state_bucket_name         = "rangle-example-state-main"
      terraform_service_account = "terraform-main@rangle-example-main.iam.gserviceaccount.com"
    }
  }
}

run "isolated_private_state" {
  command = plan
  assert {
    condition     = alltrue([for bucket in google_storage_bucket.state : bucket.public_access_prevention == "enforced" && bucket.versioning[0].enabled && !bucket.force_destroy])
    error_message = "Each environment state needs a protected private versioned bucket."
  }
  assert {
    condition     = google_storage_bucket_iam_member.state_runner["stg"].member != google_storage_bucket_iam_member.state_runner["main"].member && output.backend_configuration.stg.prefix != output.backend_configuration.main.prefix
    error_message = "stg/main must not share an execution identity or backend prefix."
  }
}

run "reject_shared_execution_identity" {
  command = plan
  variables {
    environments = {
      stg = {
        state_bucket_name         = "rangle-example-state-stg"
        terraform_service_account = "same@rangle-example-state.iam.gserviceaccount.com"
      }
      main = {
        state_bucket_name         = "rangle-example-state-main"
        terraform_service_account = "same@rangle-example-state.iam.gserviceaccount.com"
      }
    }
  }
  expect_failures = [var.environments]
}
