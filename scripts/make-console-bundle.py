#!/usr/bin/env python3
"""Makes what a Wii U needs to play Super Mario Maker on the server, and
nothing else there.

The console stays signed in to the network it uses, such as Pretendo, for
everything else. This creates an account on the server with the console's
player ID on that network, builds a plugin that sends only Super Mario
Maker here, and writes a zip to unpack onto the console's SD card.

The plugin holds the player's password for this server, so send the zip
only to them.

Usage: scripts/make-console-bundle.py <username> <player ID>

  username   a name for the player on this server, 6 to 16 characters
  player ID  the console's player ID (PID) on the network it is signed in to

It reads where the server is from deploy/tofu, and needs Docker running,
the aws command, and AWS credentials that may run a command on the
server's instance.
"""

import os
import re
import shutil
import subprocess
import sys
import tempfile
import zipfile

from smm_server import ROOT, create_console_account, tofu_output

PLUGIN_SOURCE = os.path.join(ROOT, "console-plugin")
BUILD_IMAGE = "smm-wiiu-build"
PLUGIN_PATH = "wiiu/environments/aroma/plugins/smm-private-server.wps"

README = """Playing Super Mario Maker on the private server with a Wii U
==========================================================

Your console keeps using the network it is signed in to, such as
Pretendo, for everything else. Only Super Mario Maker is sent to the
private server.

You need a Wii U running Aroma, signed in to the network account with
player ID {pid}. On any other account on the console, Super Mario Maker
plays as before.

1. Copy the wiiu folder in this zip onto the root of your SD card, so the
   plugin ends up at sd:/{plugin_path}

2. Start the console and Super Mario Maker, and go into Course World.

To stop playing on the private server, delete smm-private-server.wps from
the SD card.

The plugin holds your password for the private server. Do not pass it on.
"""


def build_plugin(host, port, pid, password, output_directory):
	if subprocess.run(["docker", "image", "inspect", BUILD_IMAGE], capture_output=True).returncode != 0:
		print("Making the image that builds the plugin. This takes a few minutes the first time")
		subprocess.run(
			["docker", "build", "--platform", "linux/amd64", "-t", BUILD_IMAGE, PLUGIN_SOURCE],
			check=True, capture_output=True,
		)

	# * Built in a copy, so that no player's password is left in the repository
	work = os.path.join(output_directory, "plugin")
	shutil.copytree(PLUGIN_SOURCE, work, ignore=shutil.ignore_patterns("build", "*.wps", "*.elf", "*.map"))

	result = subprocess.run(
		[
			"docker", "run", "--rm", "--platform", "linux/amd64", "-v", work + ":/app", BUILD_IMAGE,
			"make", "SMM_HOST=" + host, "SMM_PORT=%d" % port, "SMM_PID=%d" % pid, "SMM_NEX_PASSWORD=" + password,
		],
		capture_output=True, text=True,
	)
	if result.returncode != 0:
		sys.exit("The plugin could not be built:\n" + result.stdout[-2000:] + result.stderr[-2000:])

	return open(os.path.join(work, "smm-private-server.wps"), "rb").read()


def main():
	if len(sys.argv) != 3:
		sys.exit(__doc__)

	username = sys.argv[1]

	if not re.fullmatch(r"[A-Za-z0-9._-]{6,16}", username):
		sys.exit("A username is 6 to 16 letters, digits, dots, dashes and underscores")

	if not re.fullmatch(r"[0-9]{1,10}", sys.argv[2]) or not 0 < int(sys.argv[2]) < 2**32:
		sys.exit("The player ID is a number")

	pid = int(sys.argv[2])
	host = tofu_output("server_address")
	port = int(tofu_output("game_server_port"))

	# * Made before the account, so that a broken build does not leave
	# * an account behind
	if subprocess.run(["docker", "info"], capture_output=True).returncode != 0:
		sys.exit("Docker is not running. It is needed to build the plugin")

	password = create_console_account(username, pid)

	out_directory = os.path.join(ROOT, "local-data", "bundles")
	os.makedirs(out_directory, exist_ok=True)
	path = os.path.join(out_directory, username + "-wiiu.zip")

	with tempfile.TemporaryDirectory() as work:
		plugin = build_plugin(host, port, pid, password, work)

		with zipfile.ZipFile(path, "w", zipfile.ZIP_DEFLATED) as bundle:
			bundle.writestr("README.txt", README.format(pid=pid, plugin_path=PLUGIN_PATH))
			bundle.writestr(PLUGIN_PATH, plugin)

	os.chmod(path, 0o600)
	print("Created account %s for the console with player ID %d" % (username, pid))
	print("Wrote " + path)
	print("Send that file to the player, and only to them")


if __name__ == "__main__":
	main()
