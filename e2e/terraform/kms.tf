resource "aws_kms_key" "main" {
  description             = "kumolo-tf-verify"
  deletion_window_in_days = 7
  enable_key_rotation     = true
}

resource "aws_kms_alias" "main" {
  name          = "alias/kumolo-tf-verify"
  target_key_id = aws_kms_key.main.key_id
}

resource "aws_kms_key" "disabled" {
  description             = "kumolo-tf-verify-disabled"
  deletion_window_in_days = 7
  is_enabled              = false
}
