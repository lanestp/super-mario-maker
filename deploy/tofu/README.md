# Running the server on AWS

This makes everything a small private server needs, with [OpenTofu](https://opentofu.org) or Terraform:

- an EC2 instance that runs the server image, with the database in SQLite
- a volume of its own for the database, with a snapshot taken every day
- a private S3 bucket for course files, which keeps earlier copies of changed files for 30 days
- a registry for the server image
- a fixed address, and a network of its own

The instance holds no stored keys. It reaches S3 and the registry with its own role, and is reached with Session Manager, so there is no SSH port.

## Before you start

**The account API has no TLS yet.** A console sends a hash of its password to it, and over plain HTTP anyone on the path can read and reuse it. `allowed_cidrs` has no default for this reason: list only networks you trust.

**This has only been tried with the Cemu emulator.** A real Wii U only speaks old versions of TLS, both to the account API and to S3, and nothing here serves those yet. It also needs a patch that points it at this server.

**It costs money** for as long as it exists: an instance, a public address, two small volumes and what is stored.

## Making it

```bash
cd deploy/tofu
cp terraform.tfvars.example terraform.tfvars   # then put your own address in it
tofu init
tofu apply
```

Then build the server image and push it. The instance keeps trying to fetch it until it is there:

```bash
scripts/push-image.sh "$(tofu -chdir=deploy/tofu output -raw image_repository)"
```

## Making an account

```bash
aws ssm start-session --target "$(tofu output -raw instance_id)"
sudo docker exec -it smm ./smm-account create <username>
```

## Settings

| Name                      | What it is                                          | Default     |
| ------------------------- | --------------------------------------------------- | ----------- |
| `allowed_cidrs`           | Networks that may reach the server                  | none, required |
| `region`                  | AWS region                                          | `us-east-1` |
| `name`                    | Name given to everything that is made               | `smm`       |
| `instance_type`           | An arm64 instance type                              | `t4g.micro` |
| `data_volume_size`        | Size in GB of the database volume                   | `4`         |
| `snapshot_retention_days` | How many daily snapshots are kept                   | `7`         |
| `image_tag`               | Tag of the server image to run                      | `latest`    |

## What is kept when things change

Changing a setting makes a new instance. The database volume and the address are kept, so the server comes back with its data at the same address.

The database volume can not be destroyed by `tofu destroy` without first removing `prevent_destroy` from it in `storage.tf`. This is on purpose.

The file `courses/900000.bin` in the bucket is a one byte placeholder for the event course metadata the server needs to start. If you have the real file, put it there. It is not overwritten afterwards.
