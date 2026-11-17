# Super Mario Maker replacement server
Includes both the authentication and secure servers. Works on both WiiU and 3DS

## DataStore (S3)
This server requires an S3 compatible server to store user generated content. The WiiU and 3DS only support TLS versions 1.0 and 1.1 with RSA SSL ciphers, so an S3 server supporting these is required. The server must also support presigned `POST` URLs. This does not leave many options, as nearly all S3 cloud providers no longer support one of these 2 things. Even AWS, the creators of S3 and the provider Nintendo uses, is [dropping support for TLS versions below 1.2 in December 2023](https://aws.amazon.com/blogs/security/tls-1-2-required-for-aws-endpoints/)

For this reason, the recommended setup is using [MinIO](https://min.io/) to self host your S3 server and using [Cloudflare Tunnels](https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/) to act as your reverse proxy. Tunnels support TLS versions 1.0 and 1.1 with the required ciphers, essentially using it as a TLS proxy. This does come at some cost, and you now must manage data storage and security yourself through MinIO, but there are no other options at this time outside of self hosting

## Compiling

### Setup
Install [Go](https://go.dev/doc/install) and [git](https://git-scm.com/downloads), then clone and enter the repository

```bash
$ git clone https://github.com/PretendoNetwork/super-mario-maker
$ cd super-mario-maker
```

### Compiling using `go`
To compile using Go, `go build` to your desired location. The required modules are downloaded automatically, at the versions recorded in `go.mod`

```bash
$ go build -o build/super-mario-maker
```

The server is now built to `build/super-mario-maker`

When compiling with only Go, the authentication servers build string is not automatically set. This should not cause any issues with gameplay, but it means that the server build will not be visible in any packet dumps or logs a title may produce

To compile the servers with the authentication server build string, add `-ldflags "-X 'main.serverBuildString=BUILD_STRING_HERE'"` to the build command, or use `make` to compile the server

### Compiling using `make`
Compiling using `make` will read the local `.git` directory to create a dynamic authentication server build string, based on your repositories remote origin and current commit

Install `make` either through your systems package manager or the [official download](https://www.gnu.org/software/make/). We provide a `default` rule which compiles [using `go`](#compiling-using-go)

To build using `go`

```bash
$ make
```

The server is now built to `build/super-mario-maker`

## Database
The server can store its data in either Postgres or SQLite, chosen with `PN_SMM_DATABASE`

- `postgres` is the default, and is the right choice for a large server
- `sqlite` keeps everything in a single file and needs no database server, which suits a small private server

Both pass the same set of storage tests (`store/storetest`)

## Accounts
By default the server asks an account server for each player's NEX password over gRPC (`PN_SMM_ACCOUNTS=grpc`)

A small private server can keep its own accounts in its database instead (`PN_SMM_ACCOUNTS=local`), and then needs no account server. Accounts are managed with the `smm-account` command, which uses the same database settings as the server

```bash
$ go build -o build/smm-account ./cmd/smm-account
$ build/smm-account create <username>
$ build/smm-account password <username>
$ build/smm-account show <username>
```

With `PN_SMM_ACCOUNT_HTTP_PORT` set, the server also answers the account requests a console makes to log in and find the game server, so no separate account server is needed. `PN_SMM_SECURE_SERVER_HOST` must then be an IPv4 address, as that is what consoles are told to connect to. Accounts can not be created through this API, only with `smm-account`

The password is asked for, or read from the `SMM_ACCOUNT_PASSWORD` environment variable. Passwords are stored as a salted bcrypt hash of the hash a console sends, never as the password itself. Each account's NEX password is stored as it is, since the server needs it to talk to the console

## Trying it on one machine
`scripts/run-local.sh` builds and runs everything on this machine: the server with SQLite and its own accounts, the account API on port 8383, and a stand-in S3 server which keeps files in `local-data`. The stand-in has no access control and only listens on this machine. None of this is suitable for a server other people can reach

To point Cemu at it, make an account, then write a Cemu account file for it and tell Cemu where the account API is

```bash
$ export PN_SMM_DATABASE=sqlite PN_SMM_SQLITE_PATH=local-data/smm.db
$ build/smm-account create <username>
$ build/smm-account cemu <username> <Cemu folder>/mlc01/usr/save/system/act/80000001/account.dat
```

and put this in `network_services.xml` in Cemu's folder, then choose the custom network service for the account in Cemu's settings

```xml
<?xml version="1.0" encoding="UTF-8"?>
<content>
	<networkname>Local SMM</networkname>
	<disablesslverification>1</disablesslverification>
	<urls>
		<act>http://127.0.0.1:8383</act>
	</urls>
</content>
```

Cemu also wants `otp.bin`, `seeprom.bin` and the certificate files of a Wii U before it will go online, and a copy of the game. Course World, playing and uploading courses, and 100 Mario have been tried this way with the game in Cemu. Mii faces are not drawn without the Mii files of a Wii U, and Miiverse is not served, so an upload ends with error 115-1101 after the course itself has been stored

See [deploy/tofu](deploy/tofu) for running the server on AWS

## Testing

```bash
$ go test ./...
```

The tests in `e2e` build the server and run it with SQLite, its own accounts and a fake S3 server inside the test, so they need nothing installed either. Use `go test -short ./...` to skip them

One of them plays the part of a console, logging in over the real game protocol and uploading, downloading and rating a course. It uses a separate implementation of the protocol, written in Python, so that the server is not only checked against itself. It is skipped unless that client is set up

```bash
$ e2e/client/setup.sh
$ PN_SMM_E2E_PYTHON=e2e/client/.venv/bin/python go test ./e2e/
```

The SQLite tests need nothing else. The Postgres tests are skipped unless `PN_SMM_TEST_POSTGRES_URI` is set to a Postgres server the tests may create and drop databases on, for example

```bash
$ PN_SMM_TEST_POSTGRES_URI="postgres://postgres:postgres@127.0.0.1:5432/postgres?sslmode=disable" go test ./... -p 1
```

## Configuration
All configuration options are handled via environment variables

`.env` files are supported

| Name                                | Description                                    | Required                            |
| ----------------------------------- | ---------------------------------------------- | ----------------------------------- |
| `PN_SMM_DATABASE`                   | Database to use, `postgres` or `sqlite`        | No (Defaults to `postgres`)         |
| `PN_SMM_POSTGRES_URI`               | Fully qualified URI to your Postgres server    | Yes, when using `postgres`          |
| `PN_SMM_SQLITE_PATH`                | Path to the SQLite database file               | No (Defaults to `super-mario-maker.db`) |
| `PN_SMM_AUTHENTICATION_SERVER_PORT` | Port for the authentication server             | Yes                                 |
| `PN_SMM_SECURE_SERVER_HOST`         | Host name for the secure server                | Yes                                 |
| `PN_SMM_SECURE_SERVER_PORT`         | Port for the secure server                     | Yes                                 |
| `PN_SMM_CONFIG_S3_ENDPOINT`         | S3 server endpoint                             | Yes                                 |
| `PN_SMM_CONFIG_S3_ACCESS_KEY`       | S3 access key ID                               | No (Uses the machine's role, as on EC2) |
| `PN_SMM_CONFIG_S3_ACCESS_SECRET`    | S3 secret                                      | Yes, with an access key             |
| `PN_SMM_CONFIG_S3_REGION`           | Region of the S3 bucket                        | No (Looked up from the bucket)      |
| `PN_SMM_CONFIG_S3_SECURE`           | Whether to connect to the S3 server over HTTPS | No (Defaults to `true`)             |
| `PN_SMM_CONFIG_S3_BUCKET`           | S3 bucket                                      | Yes                                 |
| `PN_SMM_CONFIG_S3_KEY_BASE`         | S3 Key prefix                                  | Yes                                 |
| `PN_SMM_GRPC_SERVER_PORT`           | Port for the GRPC server                       | Yes                                 |
| `PN_SMM_GRPC_API_KEY`               | API key for your GRPC server                   | No (Assumed to be an open gRPC API) |
| `PN_SMM_ACCOUNTS`                   | Where accounts are kept, `grpc` or `local`     | No (Defaults to `grpc`)             |
| `PN_SMM_ACCOUNT_HTTP_PORT`          | Port to serve the account API on               | No (Not served when not set)        |
| `PN_SMM_FILE_BASE_URL`              | How games reach this server, such as `https://smm.example.com`. When set, games upload and download course files through the server on the account API's port, and are never sent to S3 | No (Games are sent to S3)           |
| `PN_SMM_FILE_ROOT_CA`               | Root certificate, as a PEM or DER file, that games are told to check this server against for files. A game trusts only this, not its own list. Needed when `PN_SMM_FILE_BASE_URL` is HTTPS: for a Let's Encrypt certificate it is their root, not the server's own certificate | No |
| `PN_SMM_FILE_URL_SECRET`            | Secret that signs the links to files           | No (A new one is made at each start) |
| `PN_SMM_ACCOUNT_HTTP_TLS_DOMAIN`    | Name games reach this server by. A certificate for it is fetched from Let's Encrypt and kept up to date. The server must be reachable by that name on port 443 | No |
| `PN_SMM_ACCOUNT_HTTP_TLS_CACHE`     | Directory fetched certificates are kept in     | No (Fetched again at each start)    |
| `PN_SMM_ACCOUNT_HTTP_TLS_LEGACY`    | `true` to also accept TLS 1.0 and 1.1 and their ciphers, which is all a Wii U speaks. Weaker than what is used today | No (Refused) |
| `PN_SMM_ACCOUNT_HTTP_TLS_CERT`      | Certificate to serve the account API over HTTPS | No (Served over HTTP)              |
| `PN_SMM_ACCOUNT_HTTP_TLS_KEY`       | Private key for the certificate above          | No                                  |
| `PN_SMM_GAME_SERVER_IDS`            | Game server IDs the account API answers for    | No (Defaults to the Super Mario Maker IDs) |
| `PN_SMM_ACCOUNT_GRPC_HOST`          | Host name for your account server gRPC service | Yes, when using `grpc` accounts     |
| `PN_SMM_ACCOUNT_GRPC_PORT`          | Port for your account server gRPC service      | Yes, when using `grpc` accounts     |
| `PN_SMM_ACCOUNT_GRPC_API_KEY`       | API key for your account server gRPC service   | No (Assumed to be an open gRPC API) |
