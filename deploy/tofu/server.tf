# What the instance may do: read and write course files, pull the server
# image, and be reached through Session Manager. It holds no stored keys
resource "aws_iam_role" "server" {
  name = "${var.name}-server"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { Service = "ec2.amazonaws.com" }
      Action    = "sts:AssumeRole"
    }]
  })
}

resource "aws_iam_role_policy" "server" {
  name = "${var.name}-server"
  role = aws_iam_role.server.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid      = "CourseFiles"
        Effect   = "Allow"
        Action   = ["s3:GetObject", "s3:PutObject", "s3:DeleteObject"]
        Resource = "${aws_s3_bucket.courses.arn}/*"
      },
      {
        Sid      = "CourseBucket"
        Effect   = "Allow"
        Action   = ["s3:ListBucket", "s3:GetBucketLocation"]
        Resource = aws_s3_bucket.courses.arn
      },
      {
        Sid      = "PullImageLogin"
        Effect   = "Allow"
        Action   = "ecr:GetAuthorizationToken"
        Resource = "*"
      },
      {
        Sid      = "PullImage"
        Effect   = "Allow"
        Action   = ["ecr:BatchCheckLayerAvailability", "ecr:GetDownloadUrlForLayer", "ecr:BatchGetImage"]
        Resource = aws_ecr_repository.server.arn
      },
    ]
  })
}

resource "aws_iam_role_policy_attachment" "session_manager" {
  role       = aws_iam_role.server.name
  policy_arn = "arn:aws:iam::aws:policy/AmazonSSMManagedInstanceCore"
}

resource "aws_iam_instance_profile" "server" {
  name = "${var.name}-server"
  role = aws_iam_role.server.name
}

# The key the server's own admin API asks for. The API is not reachable
# from outside the instance
resource "random_password" "grpc_api_key" {
  length  = 40
  special = false
}

data "aws_ssm_parameter" "ami" {
  name = "/aws/service/ami-amazon-linux-latest/al2023-ami-kernel-default-arm64"
}

locals {
  image = "${aws_ecr_repository.server.repository_url}:${var.image_tag}"

  server_environment = {
    PN_SMM_DATABASE                   = "sqlite"
    PN_SMM_SQLITE_PATH                = "/data/smm.db"
    PN_SMM_ACCOUNTS                   = "local"
    PN_SMM_ACCOUNT_HTTP_PORT          = var.account_http_port
    PN_SMM_AUTHENTICATION_SERVER_PORT = var.authentication_port
    PN_SMM_SECURE_SERVER_HOST         = aws_eip.server.public_ip
    PN_SMM_SECURE_SERVER_PORT         = var.secure_port
    PN_SMM_GRPC_SERVER_PORT           = 50051
    PN_SMM_GRPC_API_KEY               = random_password.grpc_api_key.result
    PN_SMM_CONFIG_S3_ENDPOINT         = "s3.${var.region}.amazonaws.com"
    PN_SMM_CONFIG_S3_REGION           = var.region
    PN_SMM_CONFIG_S3_SECURE           = "true"
    PN_SMM_CONFIG_S3_BUCKET           = aws_s3_bucket.courses.id
    PN_SMM_CONFIG_S3_KEY_BASE         = var.s3_key_base
  }
}

resource "aws_instance" "server" {
  ami                    = data.aws_ssm_parameter.ami.value
  instance_type          = var.instance_type
  subnet_id              = aws_subnet.public.id
  vpc_security_group_ids = [aws_security_group.server.id]
  iam_instance_profile   = aws_iam_instance_profile.server.name

  # A public address is needed to start up, before the fixed address is
  # attached
  associate_public_ip_address = true

  # A change to the settings below makes a new instance. The database
  # volume and the address are kept
  user_data_replace_on_change = true

  user_data = templatefile("${path.module}/user_data.sh.tftpl", {
    region       = var.region
    registry     = split("/", aws_ecr_repository.server.repository_url)[0]
    image        = local.image
    data_volume  = replace(aws_ebs_volume.data.id, "-", "")
    environment  = local.server_environment
    service_name = var.name
  })

  metadata_options {
    http_tokens = "required"
    # The server runs in a container on the host's network, so the
    # instance's role is one hop away
    http_put_response_hop_limit = 1
  }

  root_block_device {
    volume_type = "gp3"
    volume_size = 8
    encrypted   = true
  }

  tags = { Name = var.name }

  lifecycle {
    # A newer system image alone is not a reason to replace the instance
    ignore_changes = [ami]
  }

  depends_on = [aws_s3_object.event_course_metadata]
}
