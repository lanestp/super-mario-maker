#!/usr/bin/env bash
# Builds the server image for the EC2 instance and pushes it to the
# registry made by deploy/tofu. The instance picks it up the next time
# its service starts.
#
# Usage: scripts/push-image.sh <repository url> [tag]
#   repository url  the image_repository output of deploy/tofu
#   tag             defaults to latest
#
# Needs Docker running, and AWS credentials that may push to the registry
set -euo pipefail

repository="${1:?usage: scripts/push-image.sh <repository url> [tag]}"
tag="${2:-latest}"

registry="${repository%%/*}"
# * A registry address looks like <account>.dkr.ecr.<region>.amazonaws.com
region="$(echo "$registry" | cut -d. -f4)"

aws ecr get-login-password --region "$region" | docker login --username AWS --password-stdin "$registry"

# * The instance is arm64
docker build --platform linux/arm64 -t "$repository:$tag" .
docker push "$repository:$tag"

echo "Pushed $repository:$tag"
