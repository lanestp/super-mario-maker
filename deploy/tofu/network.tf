# A small network of its own, so that nothing here touches the rest of the
# account. An account can only hold a few networks, so when subnet_id is
# given that subnet is used and no network is made

locals {
  make_network = var.subnet_id == null

  subnet_id         = local.make_network ? aws_subnet.public[0].id : var.subnet_id
  vpc_id            = local.make_network ? aws_vpc.this[0].id : data.aws_subnet.given[0].vpc_id
  availability_zone = local.make_network ? aws_subnet.public[0].availability_zone : data.aws_subnet.given[0].availability_zone
}

data "aws_subnet" "given" {
  count = local.make_network ? 0 : 1
  id    = var.subnet_id
}

resource "aws_vpc" "this" {
  count = local.make_network ? 1 : 0

  cidr_block           = "10.42.0.0/24"
  enable_dns_support   = true
  enable_dns_hostnames = true

  tags = { Name = var.name }
}

resource "aws_internet_gateway" "this" {
  count = local.make_network ? 1 : 0

  vpc_id = aws_vpc.this[0].id

  tags = { Name = var.name }
}

data "aws_availability_zones" "available" {
  state = "available"
}

resource "aws_subnet" "public" {
  count = local.make_network ? 1 : 0

  vpc_id            = aws_vpc.this[0].id
  cidr_block        = "10.42.0.0/26"
  availability_zone = data.aws_availability_zones.available.names[0]

  tags = { Name = "${var.name}-public" }
}

resource "aws_route_table" "public" {
  count = local.make_network ? 1 : 0

  vpc_id = aws_vpc.this[0].id

  route {
    cidr_block = "0.0.0.0/0"
    gateway_id = aws_internet_gateway.this[0].id
  }

  tags = { Name = "${var.name}-public" }
}

resource "aws_route_table_association" "public" {
  count = local.make_network ? 1 : 0

  subnet_id      = aws_subnet.public[0].id
  route_table_id = aws_route_table.public[0].id
}

# The game protocol runs over UDP, and the account API over TCP. Nothing
# else is open: there is no SSH, as the instance is reached through
# Session Manager, and the gRPC port is not reachable from outside
resource "aws_security_group" "server" {
  name        = "${var.name}-server"
  description = "Super Mario Maker server"
  vpc_id      = local.vpc_id

  ingress {
    description = "Authentication server"
    protocol    = "udp"
    from_port   = var.authentication_port
    to_port     = var.authentication_port
    cidr_blocks = var.allowed_cidrs
  }

  ingress {
    description = "Secure server"
    protocol    = "udp"
    from_port   = var.secure_port
    to_port     = var.secure_port
    cidr_blocks = var.allowed_cidrs
  }

  ingress {
    description = "Account API"
    protocol    = "tcp"
    from_port   = var.account_http_port
    to_port     = var.account_http_port
    cidr_blocks = var.allowed_cidrs
  }

  egress {
    description = "Anywhere, to reach S3, the image registry and updates"
    protocol    = "-1"
    from_port   = 0
    to_port     = 0
    cidr_blocks = ["0.0.0.0/0"]
  }

  tags = { Name = "${var.name}-server" }
}

# The address consoles are told to connect to. It is kept when the
# instance is replaced
resource "aws_eip" "server" {
  domain = "vpc"

  tags = { Name = var.name }
}

resource "aws_eip_association" "server" {
  allocation_id = aws_eip.server.id
  instance_id   = aws_instance.server.id
}
