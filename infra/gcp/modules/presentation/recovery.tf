# Backups remain in Tokyo and expire seven days after creation. They survive
# source database deletion. Restore to a new named DB; do not overwrite live data.
resource "google_firestore_backup_schedule" "community_daily" {
  count     = local.community_enabled ? 1 : 0
  project   = var.project_id
  database  = google_firestore_database.community[0].name
  retention = "604800s"
  daily_recurrence {}
}
