mock_provider "google" {
  mock_data "google_artifact_registry_repository" { defaults = { format = "DOCKER" } }
}
variables {
  project_id         = "rangle-example"
  project_number     = "123456789012"
  region             = "asia-northeast1"
  base_domain        = "presentation.example.com"
  budget_currency    = "JPY"
  budget_stop_amount = 15000
  application = {
    image_digest      = "asia-northeast1-docker.pkg.dev/rangle-example/rangle-stg/server@sha256:0000000000000000000000000000000000000000000000000000000000000000"
    catalog_sha256    = "1111111111111111111111111111111111111111111111111111111111111111"
    community_enabled = true
  }
}
run "generation_is_disabled_for_existing_application" {
  command = plan
  assert {
    condition     = length(google_project_service.generation) == 0 && length(google_service_account.trainer) == 0 && length(google_project_iam_member.generation_runtime) == 0 && length(google_project_iam_member.generation_guard) == 0 && length(google_project_iam_member.generation_service_agent) == 0 && output.deployment.generation == null
    error_message = "An existing application must gain no Vertex API changes, trainer or generation permissions."
  }
  assert {
    condition     = !contains(google_cloud_run_v2_service.app[0].template[0].containers[0].args, "-generation-config") && !one([for step in yamldecode(google_workflows_workflow.guard.source_contents).main.steps : step.cancel_owned_generation if can(step.cancel_owned_generation)]).switch[0].condition && !one([for step in yamldecode(google_workflows_workflow.guard.source_contents).main.steps : step.stop_owned_generation_manager if can(step.stop_owned_generation_manager)]).switch[0].condition
    error_message = "Default-off generation must not pass a new CLI flag or call Vertex during shutdown."
  }
}
run "generation_uses_private_config_and_separate_trainer" {
  command = plan
  variables {
    application = {
      image_digest             = "asia-northeast1-docker.pkg.dev/rangle-example/rangle-stg/server@sha256:0000000000000000000000000000000000000000000000000000000000000000"
      catalog_sha256           = "1111111111111111111111111111111111111111111111111111111111111111"
      community_enabled        = true
      generation_config_sha256 = "2222222222222222222222222222222222222222222222222222222222222222"
    }
  }
  assert {
    condition     = google_service_account.trainer[0].account_id == "rangle-stg-trainer" && contains(google_cloud_run_v2_service.generation[0].template[0].containers[0].args, "/mnt/catalog/generation/2222222222222222222222222222222222222222222222222222222222222222.json") && !contains(google_cloud_run_v2_service.app[0].template[0].containers[0].args, "-generation-config") && contains(google_cloud_run_v2_service.app[0].template[0].containers[0].args, "-generation-manager-url") && google_service_account_iam_member.trainer_act_as[0].role == "roles/iam.serviceAccountUser"
    error_message = "Only the private manager loads the immutable generation config; public runtime calls its bounded API."
  }
  assert {
    condition     = length(google_project_iam_member.generation_service_agent) == 1 && google_project_iam_member.generation_service_agent[0].project == "rangle-example" && google_project_iam_member.generation_service_agent[0].role == "roles/aiplatform.serviceAgent" && google_project_iam_member.generation_service_agent[0].member == "serviceAccount:service-123456789012@gcp-sa-aiplatform.iam.gserviceaccount.com"
    error_message = "Grant the official service-agent role only to this project's Google-managed Vertex principal."
  }
  assert {
    condition     = google_project_iam_custom_role.generation_runtime[0].permissions == toset(["aiplatform.customJobs.create", "aiplatform.customJobs.get", "aiplatform.customJobs.list", "aiplatform.customJobs.cancel"]) && google_project_iam_custom_role.generation_guard[0].permissions == toset(["aiplatform.customJobs.list", "aiplatform.customJobs.cancel"]) && strcontains(google_project_iam_member.generation_runtime[0].condition[0].expression, "2026-11-14T15:00:00Z")
    error_message = "Separate deadline-bound job submission from the guard's two-operation cancellation role."
  }
  assert {
    condition     = google_project_iam_custom_role.trainer_input[0].permissions == toset(["storage.objects.get"]) && strcontains(google_storage_bucket_iam_member.trainer_input[0].condition[0].expression, "projects/_/buckets/rangle-stg-123456789012-inputs/objects/datasets/") && google_storage_bucket_iam_member.trainer_output[0].role == "roles/storage.objectCreator" && strcontains(google_storage_bucket_iam_member.trainer_output[0].condition[0].expression, "projects/_/buckets/rangle-stg-123456789012-media/objects/generated/")
    error_message = "Trainer input is exact-object get, and append-only output stays inside generated media."
  }
  assert {
    condition     = index([for step in yamldecode(google_workflows_workflow.guard.source_contents).main.steps : keys(step)[0]], "stop_owned_service") < index([for step in yamldecode(google_workflows_workflow.guard.source_contents).main.steps : keys(step)[0]], "cancel_owned_generation") && index([for step in yamldecode(google_workflows_workflow.guard.source_contents).main.steps : keys(step)[0]], "cancel_owned_generation") < index([for step in yamldecode(google_workflows_workflow.guard.source_contents).main.steps : keys(step)[0]], "check_shutdown")
    error_message = "Always attempt existing ingress/service shutdown before bounded GPU cancellation and final error handling."
  }
  assert {
    condition     = can(one([for step in yamldecode(google_workflows_workflow.guard.source_contents).main.steps : step.stop_owned_service if can(step.stop_owned_service)]).except.steps[0].record_run_error) && alltrue([for name in ["inspect_before_delete", "delete_owned_service", "wait_for_deletion_operation", "verify_service_absent"] : contains([for step in one([for main_step in yamldecode(google_workflows_workflow.guard.source_contents).main.steps : main_step.stop_owned_service if can(main_step.stop_owned_service)]).try.steps : keys(step)[0]], name)])
    error_message = "Every Cloud Run shutdown phase must share an exception handler so API and operation-name failures cannot bypass GPU cancellation or failure receipts."
  }
  assert {
    condition     = one([for step in yamldecode(google_workflows_workflow.guard.source_contents).main.steps : step.stop_owned_generation_manager if can(step.stop_owned_generation_manager)]).switch[0].condition && index([for step in yamldecode(google_workflows_workflow.guard.source_contents).main.steps : keys(step)[0]], "stop_owned_generation_manager") < index([for step in yamldecode(google_workflows_workflow.guard.source_contents).main.steps : keys(step)[0]], "stop_owned_service") && one([for step in yamldecode(google_workflows_workflow.guard.source_contents).main.steps : step.stop_owned_generation_manager if can(step.stop_owned_generation_manager)]).switch[0].steps[0].delete_manager.try.args.service_path == "projects/rangle-example/locations/asia-northeast1/services/rangle-stg-generation"
    error_message = "Stop only the fixed owned generation manager before the public Run phase, preserving later GPU cancellation."
  }
  assert {
    condition     = can(one([for step in yamldecode(google_workflows_workflow.guard.source_contents).main.steps : step.cancel_owned_generation if can(step.cancel_owned_generation)]).switch[0].steps[0].cancel_jobs.except.steps[0].record_generation_error) && strcontains(google_workflows_workflow.guard.source_contents, "guard/failures/")
    error_message = "GPU API failures must enter the shared shutdown error path and leave a private failure receipt."
  }
  assert {
    condition     = one([for step in yamldecode(google_workflows_workflow.guard.source_contents).scan_owned_generation_jobs.steps : step.list_jobs if can(step.list_jobs)]).args.query.filter == "labels.application=\"rangle\" AND labels.environment=\"presentation-stg\" AND labels.rangle_job:*" && strcontains(google_workflows_workflow.guard.source_contents, "^[0-9a-f]{32}$") && strcontains(google_workflows_workflow.guard.source_contents, "^projects/(rangle-example|123456789012)/locations/asia-northeast1/customJobs/") && strcontains(google_workflows_workflow.guard.source_contents, "nextPageToken") && strcontains(google_workflows_workflow.guard.source_contents, "sys.now() + 300")
    error_message = "Guard must paginate the exact environment, recheck owned names/labels, and stop polling within a finite window."
  }
  assert {
    condition     = alltrue([for expression in regexall("\\$\\{[^\\n]*\\}", google_workflows_workflow.guard.source_contents) : length(expression) <= 400]) && length(regexall("\\$\\{[^\\n]*\\}", google_workflows_workflow.guard.source_contents)) == length(regexall("\\$\\{", google_workflows_workflow.guard.source_contents))
    error_message = "Every rendered Workflow expression, including its delimiters, must fit the compiler's 400-character limit."
  }
  assert {
    condition     = one([for assignment in one([for step in yamldecode(google_workflows_workflow.guard.source_contents).scan_owned_generation_jobs.steps : step.initialize if can(step.initialize)]).assign : assignment.empty_labels if can(assignment.empty_labels)]) == {} && strcontains(google_workflows_workflow.guard.source_contents, "default(map.get(job, \"labels\"), empty_labels)")
    error_message = "Missing job labels must fall back to a native empty map, never the previous job's labels or a map literal inside an expression."
  }
}
run "reject_unversioned_generation_config" {
  command = plan
  variables {
    application = {
      image_digest             = "asia-northeast1-docker.pkg.dev/rangle-example/rangle-stg/server@sha256:0000000000000000000000000000000000000000000000000000000000000000"
      catalog_sha256           = "1111111111111111111111111111111111111111111111111111111111111111"
      generation_config_sha256 = "latest"
    }
  }
  expect_failures = [var.application]
}
