locals {
  # Fixed owned database names keep the workflow source known during mock plans.
  # Backup schedule IDs are server-assigned and must be discovered at shutdown.
  backup_database_paths = concat(
    local.community_enabled ? ["projects/${var.project_id}/databases/${local.prefix}"] : [],
    local.generation_enabled ? ["projects/${var.project_id}/databases/${local.prefix}-generation"] : []
  )
}

resource "google_project_iam_custom_role" "stop_backup_schedules" {
  count       = length(local.backup_database_paths) > 0 ? 1 : 0
  project     = var.project_id
  role_id     = "rangleStgStopBackupSchedules"
  title       = "Stop future presentation database backups"
  permissions = ["datastore.backupSchedules.list", "datastore.backupSchedules.delete"]
  depends_on  = [google_project_service.community]
}

# BackupSchedule is absent from the documented IAM resource-attribute types.
# Do not claim database/name conditions protect schedule deletion. This explicit
# project-level grant is guard-only; workflow helpers restrict the exact DBs,
# schedule resource names, daily recurrence, and seven-day retention instead.
# https://docs.cloud.google.com/iam/docs/conditions-resource-attributes
resource "google_project_iam_member" "stop_backup_schedules" {
  count   = length(local.backup_database_paths) > 0 ? 1 : 0
  project = var.project_id
  role    = google_project_iam_custom_role.stop_backup_schedules[0].name
  member  = "serviceAccount:${google_service_account.guard.email}"
}
