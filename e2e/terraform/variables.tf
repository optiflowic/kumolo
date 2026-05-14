variable "kumolo_endpoint" {
  description = "Base URL for kumolo (overrides via TF_VAR_kumolo_endpoint or -var flag)"
  type        = string
  default     = "http://localhost:5566"
}
