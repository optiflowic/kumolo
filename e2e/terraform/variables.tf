variable "kumolo_endpoint" {
  description = "Base URL for kumolo (overrides via TF_VAR_kumolo_endpoint or -var flag)"
  type        = string
  default     = "http://localhost:5566"
}

variable "admin_user_enabled" {
  description = "Enabled state for aws_cognito_user.admin (toggled on the second apply to exercise AdminEnableUser/AdminDisableUser)"
  type        = bool
  default     = true
}

variable "admin_given_name" {
  description = "given_name attribute for aws_cognito_user.admin (changed on the second apply to exercise AdminUpdateUserAttributes)"
  type        = string
  default     = "Original"
}
