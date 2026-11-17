output "server_address" {
  description = "The address consoles and emulators connect to"
  value       = aws_eip.server.public_ip
}

output "account_api_url" {
  description = "What to give an emulator as its account server"
  value       = "http://${aws_eip.server.public_ip}:${var.account_http_port}"
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
