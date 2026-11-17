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

variable "domain_name" {
  description = <<-EOT
    Name to serve the account API and course files under over TLS, such as
    smm.example.com. It must be in a Route 53 zone of this account. A
    certificate is fetched for it, and the API moves to port 443. Left
    out, the API is served over plain HTTP on account_http_port
  EOT
  type        = string
  default     = null
}

variable "legacy_tls" {
  description = "Also accept TLS 1.0 and 1.1, which is all a Wii U speaks. Only used with domain_name"
  type        = bool
  default     = true
}

variable "files_without_tls" {
  description = <<-EOT
    Serve course files over plain HTTP on port 80, and logins over TLS.
    Needed for emulators without a real console's certificate files, which
    can not reach files over TLS at all. Turn off when only real consoles
    connect. Only used with domain_name
  EOT
  type        = bool
  default     = true
}

variable "root_ca_file" {
  description = <<-EOT
    Root certificate games are told to check the server against, as a file
    in this directory. It must be the root the server's certificate leads
    up to within two steps. The default is the Let's Encrypt root for
    certificates with an RSA key, which is the kind a game is given. If
    Let's Encrypt moves to another root, games will refuse the server
    until this is changed to match
  EOT
  type        = string
  default     = "certificates/lets-encrypt-root-yr.pem"
}

variable "invite_code" {
  description = <<-EOT
    Code players give on the join page at /join to make an account. Left
    out, there is no join page. Kept in the state, so treat the state as
    secret
  EOT
  type        = string
  default     = null
  sensitive   = true
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
