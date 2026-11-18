# Super Mario Maker private server

A server for running your own Super Mario Maker (Wii U) world: Course World, 100 Mario, uploading and playing courses, makers, stars and records, for a group of friends instead of the whole internet.

It is a fork of [Pretendo Network's Super Mario Maker server](https://github.com/PretendoNetwork/super-mario-maker), and keeps everything that makes it work with the real game. On top of it, it adds what a small private server needs to stand on its own:

- **Its own accounts and account server**, so no Pretendo or Nintendo account server is needed. Players join on a web page with an invite code and download a ready-made setup.
- **SQLite or Postgres**, chosen at start. A private server needs no database server.
- **Course files that never leave the server unchecked.** Games upload to and download from the server itself, which keeps them in a private S3 bucket. Everything a game reaches over HTTP is on one port, served over TLS with a certificate fetched from Let's Encrypt, old TLS included for the console.
- **100 Mario and Course World by difficulty**, picking courses by how often attempts at them fail, as the game asks.
- **Archived courses**, brought in from the archive of Nintendo's servers through an import service that checks each one, keeping their original IDs, makers, Miis, numbers and records.
- **A Wii U plugin** that sends Super Mario Maker, and only Super Mario Maker, to the server, while the console stays on Pretendo for everything else.
- **One command to run it on AWS**, with [OpenTofu](deploy/tofu).
- **Tests**, including one that logs in and plays over the real game protocol with an independent client.

## Where it stands

| | |
| --- | --- |
| Cemu emulator | Tried with the game: Course World, 100 Mario, playing, uploading, makers, stars, records, over TLS against a server on AWS |
| Real Wii U | Built but **not tried** on a console: the plugin, old TLS, and the certificate games check files against |
| Mii faces in Cemu | Not drawn, as Cemu needs the Mii files of a real Wii U. Names show |
| Miiverse | Not served, so an upload ends with error 115-1101 after the course has been stored |
| Courses made outside the game | Packing them is built (`coursefile`), but courses packed this way have not loaded in the game yet. Archived courses, which are the game's own files, do |

## Getting started

| To | See |
| --- | --- |
| Try everything on one machine | [Trying it on one machine](#trying-it-on-one-machine) |
| Run a server others can join | [deploy/tofu](deploy/tofu) |
| Let players join | `PN_SMM_INVITE_CODE`, then send them to `/join` on the server |
| Bring in archived courses | [Bringing in courses from the archive](deploy/tofu/README.md#bringing-in-courses-from-the-archive) |
| Play on a Wii U | [console-plugin](console-plugin) |

Nothing from Nintendo is included. Players need their own copy of the game, and Cemu needs files from a real Wii U for some things, such as Mii faces. What this project hands players in place of a console's files are blank files and dummy certificates made for them.

## License

The server is under the [GNU Affero General Public License v3.0](LICENSE), as the Pretendo server it is a fork of is, and as the Pretendo libraries it is built on are. If you run a changed version for others, the AGPL asks you to offer them its source.

These parts were written for this fork, use nothing under the AGPL, and are also under the [MIT license](https://opensource.org/license/mit), so that they can be used anywhere:

| Part | What it is |
| --- | --- |
| [ash0](ash0) | The ASH0 compression course files use, both ways |
| [coursefile](coursefile) | Reading and writing course files, and the meta binary a game needs with them |
| [mii](mii) | Renaming a Mii and its checksum |
| [console-plugin](console-plugin) | The Wii U plugin |
| [deploy](deploy) | Running the server on AWS |

## Thanks

To [Pretendo Network](https://pretendo.network) and everyone who built the server this is a fork of and the libraries it stands on, to the [Cemu](https://cemu.info) project, whose source showed how a game reaches its servers, to [Archive Team](https://wiki.archiveteam.org) and the Internet Archive for saving Nintendo's servers before they closed, and to [kinnay's NintendoClients](https://github.com/kinnay/NintendoClients), which the end to end tests use.

## Compiling

### Setup
Install [Go](https://go.dev/doc/install) and [git](https://git-scm.com/downloads), then clone and enter the repository

```bash
$ git clone https://github.com/lanestp/super-mario-maker
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

## Course files
With `PN_SMM_FILE_BASE_URL` set, games upload and download course files through the server, on the account API's port, and the S3 bucket behind it is only ever reached by the server. Without it, games are sent to S3 itself, as the Pretendo server does. A Wii U only speaks TLS 1.0 and 1.1 with RSA ciphers, which nearly no S3 provider still offers, so the bucket would then need a proxy in front of it that does

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

The Postgres tests need nothing installed or running either. The first one in a package starts a Postgres of its own, from binaries downloaded once (about 30 MB, kept in `~/.embedded-postgres-go` and unpacked into your cache folder), and stops it when the package's tests finish. To run them against a Postgres server of your own instead, set `PN_SMM_TEST_POSTGRES_URI` to one the tests may create and drop databases on, for example

```bash
$ PN_SMM_TEST_POSTGRES_URI="postgres://postgres:postgres@127.0.0.1:5432/postgres?sslmode=disable" go test ./... -p 1
```

To see how much of the code the tests run, including the server the end to end tests start, run `scripts/coverage.sh` with the same settings. It prints each package's coverage, worst first, and writes `build/coverage.html`

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
| `PN_SMM_FILE_HTTP_PORT`             | Port to also serve course files on without TLS, and nothing else. For emulators without a real console's certificate files, which can not reach files over TLS. Point `PN_SMM_FILE_BASE_URL` at it | No |
| `PN_SMM_FILE_ROOT_CA`               | Root certificate, as a PEM or DER file, that games are told to check this server against for files. A game trusts only this, not its own list. Needed when `PN_SMM_FILE_BASE_URL` is HTTPS: for a Let's Encrypt certificate it is their root, not the server's own certificate | No |
| `PN_SMM_FILE_URL_SECRET`            | Secret that signs the links to files           | No (A new one is made at each start) |
| `PN_SMM_INVITE_CODE`                | When set, serves a page at `/join` on the account API's port where players with this code make an account and download what they need to play | No (No page) |
| `PN_SMM_ACCOUNT_PUBLIC_URL`         | How players' Cemu reaches the account API, such as `https://smm.example.com`. Needed for the join page | With `PN_SMM_INVITE_CODE` |
| `PN_SMM_CONSOLE_PLUGIN`             | The built Wii U plugin, which the join page hands to Wii U players. The server image has it at `/home/go/app/smm-private-server.wps` | No (Only Cemu is offered) |
| `PN_SMM_IMPORT_TOKEN`               | When set, serves the import service, which `smm-import-archive` sends archived courses to, with this as its token | No |
| `PN_SMM_IMPORT_ADDRESS`             | Loopback address to serve the import service on | No (`127.0.0.1:8384`) |
| `PN_SMM_ACCOUNT_HTTP_TLS_DOMAIN`    | Name games reach this server by. A certificate for it is fetched from Let's Encrypt and kept up to date. The server must be reachable by that name on port 443 | No |
| `PN_SMM_ACCOUNT_HTTP_TLS_CACHE`     | Directory fetched certificates are kept in     | No (Fetched again at each start)    |
| `PN_SMM_ACCOUNT_HTTP_TLS_LEGACY`    | `true` to also accept TLS 1.0 and 1.1 and their ciphers, which is all a Wii U speaks. Weaker than what is used today | No (Refused) |
| `PN_SMM_ACCOUNT_HTTP_TLS_CERT`      | Certificate to serve the account API over HTTPS | No (Served over HTTP)              |
| `PN_SMM_ACCOUNT_HTTP_TLS_KEY`       | Private key for the certificate above          | No                                  |
| `PN_SMM_GAME_SERVER_IDS`            | Game server IDs the account API answers for    | No (Defaults to the Super Mario Maker IDs) |
| `PN_SMM_ACCOUNT_GRPC_HOST`          | Host name for your account server gRPC service | Yes, when using `grpc` accounts     |
| `PN_SMM_ACCOUNT_GRPC_PORT`          | Port for your account server gRPC service      | Yes, when using `grpc` accounts     |
| `PN_SMM_ACCOUNT_GRPC_API_KEY`       | API key for your account server gRPC service   | No (Assumed to be an open gRPC API) |
