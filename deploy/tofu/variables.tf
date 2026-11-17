variable "region" {
  description = "AWS region to build in"
  type        = string
  default     = "us-east-1"
}

variable "name" {
  description = "Name given to everything that is made"
  type        = string
  default     = "smm"
}

variable "allowed_cidrs" {
  description = <<-EOT
    Networks that may reach the server, such as ["203.0.113.7/32"] for one
    home connection. There is no default on purpose: the account API is
    served over plain HTTP for now, so a password sent to it can be read by
    anyone on the path. Only list networks you trust until it has TLS
  EOT
  type        = list(string)
}

variable "subnet_id" {
  description = <<-EOT
    A public subnet to put the server in, such as one of the account's
    default subnets. Left out, a network is made for the server alone. An
    account can only hold a few networks, so give this when that fails
    with VpcLimitExceeded
  EOT
  type        = string
  default     = null
}

variable "instance_type" {
  description = "EC2 instance type. Must be an arm64 type, as the image is built for arm64"
  type        = string
  default     = "t4g.micro"
}

variable "data_volume_size" {
  description = "Size in GB of the volume the database is kept on"
  type        = number
  default     = 4
}

variable "snapshot_retention_days" {
  description = "How many daily snapshots of the database volume are kept"
  type        = number
  default     = 7
}

variable "image_tag" {
  description = "Tag of the server image in the registry to run"
  type        = string
  default     = "latest"
}

variable "authentication_port" {
  description = "UDP port of the authentication server"
  type        = number
  default     = 6004
}

variable "secure_port" {
  description = "UDP port of the secure server"
  type        = number
  default     = 6005
}

variable "account_http_port" {
  description = "TCP port of the account API"
  type        = number
  default     = 8383
}

variable "s3_key_base" {
  description = "Key prefix course files are kept under in the bucket"
  type        = string
  default     = "courses"
}
