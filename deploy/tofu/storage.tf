data "aws_caller_identity" "current" {}

# Course files. Games upload to and download from the bucket directly,
# with links the server signs, so the bucket itself stays private
resource "aws_s3_bucket" "courses" {
  bucket = "${var.name}-courses-${data.aws_caller_identity.current.account_id}-${var.region}"
}

resource "aws_s3_bucket_public_access_block" "courses" {
  bucket = aws_s3_bucket.courses.id

  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

resource "aws_s3_bucket_ownership_controls" "courses" {
  bucket = aws_s3_bucket.courses.id

  rule {
    object_ownership = "BucketOwnerEnforced"
  }
}

resource "aws_s3_bucket_server_side_encryption_configuration" "courses" {
  bucket = aws_s3_bucket.courses.id

  rule {
    apply_server_side_encryption_by_default {
      sse_algorithm = "AES256"
    }
  }
}

# Keeps earlier copies of a file that is overwritten or deleted, which is
# the backup of course files
resource "aws_s3_bucket_versioning" "courses" {
  bucket = aws_s3_bucket.courses.id

  versioning_configuration {
    status = "Enabled"
  }
}

resource "aws_s3_bucket_lifecycle_configuration" "courses" {
  bucket = aws_s3_bucket.courses.id

  rule {
    id     = "expire-old-copies"
    status = "Enabled"

    filter {}

    noncurrent_version_expiration {
      noncurrent_days = 30
    }

    abort_incomplete_multipart_upload {
      days_after_initiation = 7
    }
  }

  depends_on = [aws_s3_bucket_versioning.courses]
}

resource "aws_s3_bucket_policy" "courses" {
  bucket = aws_s3_bucket.courses.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Sid       = "DenyWithoutTLS"
      Effect    = "Deny"
      Principal = "*"
      Action    = "s3:*"
      Resource  = [aws_s3_bucket.courses.arn, "${aws_s3_bucket.courses.arn}/*"]
      Condition = { Bool = { "aws:SecureTransport" = "false" } }
    }]
  })

  depends_on = [aws_s3_bucket_public_access_block.courses]
}

# The server refuses to start without the event course metadata file. This
# is a one byte placeholder and not real event course data. Put the real
# file in its place when there is one: it is not overwritten afterwards
resource "aws_s3_object" "event_course_metadata" {
  bucket         = aws_s3_bucket.courses.id
  key            = "${var.s3_key_base}/900000.bin"
  content_base64 = "AA=="
  content_type   = "application/octet-stream"

  lifecycle {
    ignore_changes = [content_base64, etag, content_type, metadata]
  }
}

# The server image
resource "aws_ecr_repository" "server" {
  name                 = var.name
  image_tag_mutability = "MUTABLE"

  image_scanning_configuration {
    scan_on_push = true
  }
}

resource "aws_ecr_lifecycle_policy" "server" {
  repository = aws_ecr_repository.server.name

  policy = jsonencode({
    rules = [{
      rulePriority = 1
      description  = "Keep the last 5 images"
      selection = {
        tagStatus   = "any"
        countType   = "imageCountMoreThan"
        countNumber = 5
      }
      action = { type = "expire" }
    }]
  })
}

# The database is kept on a volume of its own, so that replacing the
# instance does not lose it
resource "aws_ebs_volume" "data" {
  availability_zone = aws_subnet.public.availability_zone
  size              = var.data_volume_size
  type              = "gp3"
  encrypted         = true

  tags = {
    Name   = "${var.name}-data"
    Backup = var.name
  }

  lifecycle {
    prevent_destroy = true
  }
}

resource "aws_volume_attachment" "data" {
  device_name = "/dev/sdf"
  volume_id   = aws_ebs_volume.data.id
  instance_id = aws_instance.server.id

  # Lets the instance be replaced without the volume being torn off a
  # running machine first
  stop_instance_before_detaching = true
}

# A snapshot of the database volume every day
resource "aws_iam_role" "snapshots" {
  name = "${var.name}-snapshots"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { Service = "dlm.amazonaws.com" }
      Action    = "sts:AssumeRole"
    }]
  })
}

resource "aws_iam_role_policy_attachment" "snapshots" {
  role       = aws_iam_role.snapshots.name
  policy_arn = "arn:aws:iam::aws:policy/service-role/AWSDataLifecycleManagerServiceRole"
}

resource "aws_dlm_lifecycle_policy" "data" {
  description        = "Daily snapshots of the ${var.name} database volume"
  execution_role_arn = aws_iam_role.snapshots.arn
  state              = "ENABLED"

  policy_details {
    resource_types = ["VOLUME"]
    target_tags    = { Backup = var.name }

    schedule {
      name = "daily"

      create_rule {
        interval      = 24
        interval_unit = "HOURS"
        times         = ["08:00"]
      }

      retain_rule {
        count = var.snapshot_retention_days
      }

      copy_tags = true
    }
  }
}
