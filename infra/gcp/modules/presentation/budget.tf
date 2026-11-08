resource "google_billing_budget" "presentation" {
  count           = var.budget == null ? 0 : 1
  billing_account = var.budget.billing_account_id
  display_name    = var.budget_display_name
  # The API returns omitted scope for its ALL_USERS default. Setting ALL_USERS
  # explicitly causes perpetual drift; unspecified has the same documented scope.
  budget_filter {
    projects               = ["projects/${var.project_number}"]
    credit_types_treatment = "EXCLUDE_ALL_CREDITS"
    custom_period {
      start_date {
        year  = tonumber(split("-", var.budget.start_date)[0])
        month = tonumber(split("-", var.budget.start_date)[1])
        day   = tonumber(split("-", var.budget.start_date)[2])
      }
      end_date {
        year  = tonumber(split("-", var.budget.end_date)[0])
        month = tonumber(split("-", var.budget.end_date)[1])
        day   = tonumber(split("-", var.budget.end_date)[2])
      }
    }
  }
  amount {
    specified_amount {
      currency_code = var.budget_currency
      units         = tostring(var.budget.amount)
    }
  }
  dynamic "threshold_rules" {
    for_each = [0.25, 0.5, 0.75]
    content { threshold_percent = threshold_rules.value }
  }
  all_updates_rule {
    pubsub_topic                    = google_pubsub_topic.budget.id
    schema_version                  = "1.0"
    enable_project_level_recipients = true
    disable_default_iam_recipients  = true
  }
  depends_on = [google_pubsub_topic_iam_member.billing_publisher]
}
