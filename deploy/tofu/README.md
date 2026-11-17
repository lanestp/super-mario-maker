# Running the server on AWS

This makes everything a small private server needs, with [OpenTofu](https://opentofu.org) or Terraform:

- an EC2 instance that runs the server image, with the database in SQLite
- a volume of its own for the database, with a snapshot taken every day
- a private S3 bucket for course files, which keeps earlier copies of changed files for 30 days. Only the server reaches it: games upload and download through the server
- a registry for the server image
- a fixed address, and a network of its own

The instance holds no stored keys. It reaches S3 and the registry with its own role, and is reached with Session Manager, so there is no SSH port.

## Before you start

**Give it a `domain_name`.** Without one the account API is plain HTTP: a console sends a hash of its password to it, and anyone on the path can read and reuse it. With a name in a Route 53 zone of the account, the server fetches its own certificate from Let's Encrypt and serves everything over TLS on port 443.

**Old TLS is on by default with a domain**, as a Wii U speaks nothing newer. Current clients are still given current encryption. Set `legacy_tls = false` if only emulators will connect.

**This has only been tried with the Cemu emulator.** Everything a console talks to over HTTP, the account API and course files alike, is on the one port of the server. A real Wii U also only trusts Nintendo's own certificate authorities, so it needs a patch that makes it accept this server's certificate and points it here. That has not been tried.

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

## Bringing in courses from the archive

The server has an import service on the instance's loopback address. `smm-import-archive` picks courses from a public index of the archive of Nintendo's servers, fetches each course file from the Wayback Machine, and sends it to the service, which checks it and writes it. It runs in a container of its own that is removed when it finishes, and keeps nothing on disk. It waits between requests to the archive, so thousands of courses take many hours.

```bash
aws ssm start-session --target "$(tofu output -raw instance_id)"
sudo systemd-run --unit smm-import --collect \
	docker run --rm --name smm-import --network host --env-file /etc/smm/environment \
	"$(sudo docker inspect --format '{{.Config.Image}}' smm)" ./smm-import-archive -count 10000
sudo journalctl -u smm-import -f      # to follow it
sudo systemctl stop smm-import        # to stop it early
```

Courses keep their IDs from Nintendo's servers. Who holds a course's record, and their Mii, come from the archive, but they are given a made-up player ID, as the archive does not have theirs. Who cleared a course first is not archived, so the record holder stands in.

## Settings

| Name                      | What it is                                          | Default     |
| ------------------------- | --------------------------------------------------- | ----------- |
| `allowed_cidrs`           | Networks that may reach the server                  | none, required |
| `region`                  | AWS region                                          | `us-east-1` |
| `domain_name`             | Name to serve under over TLS, in a Route 53 zone of the account | plain HTTP |
| `files_without_tls`       | Serve course files over plain HTTP on port 80, for emulators | `true`      |
| `legacy_tls`              | Also accept TLS 1.0 and 1.1, for a Wii U            | `true`      |
| `invite_code`             | Code players give on the join page at `/join`       | no page     |
| `subnet_id`               | A public subnet to use, when the account has no room for another network | a network is made |
| `name`                    | Name given to everything that is made               | `smm`       |
| `instance_type`           | An arm64 instance type                              | `t4g.micro` |
| `data_volume_size`        | Size in GB of the database volume                   | `4`         |
| `snapshot_retention_days` | How many daily snapshots are kept                   | `7`         |
| `image_tag`               | Tag of the server image to run                      | `latest`    |

## What is kept when things change

Changing a setting makes a new instance. The database volume and the address are kept, so the server comes back with its data at the same address.

The database volume can not be destroyed by `tofu destroy` without first removing `prevent_destroy` from it in `storage.tf`. This is on purpose.

The file `courses/900000.bin` in the bucket is a one byte placeholder for the event course metadata the server needs to start. If you have the real file, put it there. It is not overwritten afterwards.
