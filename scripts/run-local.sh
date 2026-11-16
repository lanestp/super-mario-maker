#!/bin/sh
# Runs the whole server on this machine, for trying it out with an
# emulator on the same machine. Uses SQLite, accounts kept in the
# server's own database, and a stand-in S3 server with no access control.
# Nothing here is suitable for a server other people can reach.
#
#   scripts/run-local.sh
#
# Everything is kept in ./local-data. Make an account first with
#
#   PN_SMM_DATABASE=sqlite PN_SMM_SQLITE_PATH=local-data/smm.db build/smm-account create <username>
set -eu

cd "$(dirname "$0")/.."

mkdir -p build local-data

go build -o build/super-mario-maker .
go build -o build/smm-account ./cmd/smm-account
go build -o build/smm-local-s3 ./cmd/smm-local-s3

build/smm-local-s3 -addr 127.0.0.1:9000 -dir local-data/s3 -bucket smm -key-base courses &
s3_pid=$!
trap 'kill "$s3_pid" 2>/dev/null || true' EXIT INT TERM

# Wait for the stand-in S3 server to accept connections
tries=0
until nc -z 127.0.0.1 9000 2>/dev/null; do
	tries=$((tries + 1))
	if [ "$tries" -gt 50 ]; then
		echo "the local S3 stand-in did not start" >&2
		exit 1
	fi
	sleep 0.1
done

export PN_SMM_DATABASE=sqlite
export PN_SMM_SQLITE_PATH=local-data/smm.db
export PN_SMM_ACCOUNTS=local
export PN_SMM_ACCOUNT_HTTP_PORT=8383
export PN_SMM_AUTHENTICATION_SERVER_PORT=6004
export PN_SMM_SECURE_SERVER_HOST=127.0.0.1
export PN_SMM_SECURE_SERVER_PORT=6005
export PN_SMM_GRPC_SERVER_PORT=50051
export PN_SMM_GRPC_API_KEY="${PN_SMM_GRPC_API_KEY:-local-only}"
export PN_SMM_CONFIG_S3_ENDPOINT=127.0.0.1:9000
export PN_SMM_CONFIG_S3_ACCESS_KEY=local
export PN_SMM_CONFIG_S3_ACCESS_SECRET=local-secret
export PN_SMM_CONFIG_S3_SECURE=false
export PN_SMM_CONFIG_S3_BUCKET=smm
export PN_SMM_CONFIG_S3_KEY_BASE=courses

build/super-mario-maker
