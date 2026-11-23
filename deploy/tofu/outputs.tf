output "server_address" {
  description = "The address consoles and emulators connect to"
  value       = aws_eip.server.public_ip
}

output "account_api_url" {
  description = "What to give an emulator as its account server"
  value       = local.base_url
}

output "bucket" {
  description = "The bucket course files are kept in"
  value       = aws_s3_bucket.courses.id
}

output "image_repository" {
  description = "Where to push the server image"
  value       = aws_ecr_repository.server.repository_url
}

output "instance_id" {
  description = "The instance, for Session Manager"
  value       = aws_instance.server.id
}

output "connect_command" {
  description = "Opens a shell on the instance"
  value       = "aws ssm start-session --region ${var.region} --target ${aws_instance.server.id}"
}

output "join_page_url" {
  description = "Where players make an account, when the join page is served"
  value       = var.join_page ? "${local.base_url}/join" : null
}

output "game_server_port" {
  description = "UDP port games log in to the game server on"
  value       = var.authentication_port
}
