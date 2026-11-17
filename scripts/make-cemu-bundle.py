#!/usr/bin/env python3
"""Makes everything a player needs to join the server with the Cemu emulator.

It creates an account for them on the server, and writes a zip that they
unpack into Cemu's folder. The zip holds

  - an account file that logs in as them,
  - the settings that point Cemu at the server,
  - blank stand-ins for the console files Cemu wants before it goes online.

Nothing in the zip comes from a real console. The certificates are dummies
made here: Cemu only needs them to be certificates, and the server is
checked against the root certificate the server itself hands the game.

The zip lets whoever has it play as that player, so send it only to them.

Usage: scripts/make-cemu-bundle.py <username> [output directory]

It reads where the server is from deploy/tofu, and needs the aws and
openssl commands, with AWS credentials that may run a command on the
server's instance.
"""

import hashlib
import os
import re
import secrets
import shutil
import struct
import subprocess
import sys
import tempfile
import time
import urllib.parse
import urllib.request
import zipfile

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
TOFU = os.path.join(ROOT, "deploy", "tofu")

# * The Mii Cemu gives a new account: 96 bytes with every part of the face
# * set the same way. It is renamed for each player below
PLACEHOLDER_MII = "010001100000d73e030034330100010001000100010001000100640065006600610075006c0074000000000000000100010001000100010001000106010001000100010001000100010001000100010001000100010001000100010001000100"

# * The certificates of authorities a console trusts, by the file names
# * Cemu looks for. Each gets a dummy certificate
SERVER_CERTIFICATES = """
ADDTRUST_EXT_CA_ROOT BALTIMORE_CYBERTRUST_ROOT_CA CACERT_NINTENDO_CA_G2 CACERT_NINTENDO_CA_G3
CACERT_NINTENDO_CA CACERT_NINTENDO_CLASS2_CA_G2 CACERT_NINTENDO_CLASS2_CA_G3
CACERT_NINTENDO_CLASS2_CA COMODO_CA CYBERTRUST_GLOBAL_ROOT_CA DIGICERT_ASSURED_ID_ROOT_CA_G2
DIGICERT_ASSURED_ID_ROOT_CA DIGICERT_GLOBAL_ROOT_CA_G2 DIGICERT_GLOBAL_ROOT_CA
DIGICERT_HIGH_ASSURANCE_EV_ROOT_CA ENTRUST_CA_2048 ENTRUST_ROOT_CA_G2 ENTRUST_ROOT_CA
ENTRUST_SECURE_SERVER_CA EQUIFAX_SECURE_CA GEOTRUST_GLOBAL_CA GEOTRUST_GLOBAL_CA2
GEOTRUST_PRIMARY_CA_G3 GEOTRUST_PRIMARY_CA GLOBALSIGN_ROOT_CA_R2 GLOBALSIGN_ROOT_CA_R3
GLOBALSIGN_ROOT_CA GTE_CYBERTRUST_GLOBAL_ROOT THAWTE_PREMIUM_SERVER_CA
THAWTE_PRIMARY_ROOT_CA_G3 THAWTE_PRIMARY_ROOT_CA UTN_DATACORP_SGC_CA
UTN_USERFIRST_HARDWARE_CA VERISIGN_CLASS3_PUBLIC_PRIMARY_CA_G2
VERISIGN_CLASS3_PUBLIC_PRIMARY_CA_G3 VERISIGN_CLASS3_PUBLIC_PRIMARY_CA_G5
VERISIGN_CLASS3_PUBLIC_PRIMARY_CA VERISIGN_UNIVERSAL_ROOT_CA VERIZON_GLOBAL_ROOT_CA
""".split()

# * A console's own certificates and keys. These can not be made up, as
# * the keys are locked with a key only that console has. They are left
# * as one byte files, which is enough for this server
CLIENT_CERTIFICATES = [
	name + suffix
	for name in ("WIIU_ACCOUNT_1", "WIIU_COMMON_1", "WIIU_OLIVE_1", "WIIU_VINO_1", "WIIU_WOOD_1")
	for suffix in ("_CERT.der", "_RSA_KEY.aes")
]

CERTIFICATE_FOLDER = "mlc01/sys/title/0005001b/10054000/content"
ACCOUNT_FILE = "mlc01/usr/save/system/act/80000001/account.dat"


def run(*command):
	return subprocess.run(command, check=True, capture_output=True, text=True).stdout.strip()


def tofu_output(name):
	tool = shutil.which("tofu") or shutil.which("terraform")
	if not tool:
		sys.exit("Neither tofu nor terraform was found")
	return run(tool, "-chdir=" + TOFU, "output", "-raw", name)


def on_server(instance, region, command):
	"""Runs a command on the server's instance and returns what it printed."""
	command_id = run(
		"aws", "ssm", "send-command", "--region", region, "--instance-ids", instance,
		"--document-name", "AWS-RunShellScript", "--parameters", "commands=[%s]" % quote_for_ssm(command),
		"--query", "Command.CommandId", "--output", "text",
	)

	for _ in range(30):
		time.sleep(2)
		status = run(
			"aws", "ssm", "get-command-invocation", "--region", region, "--command-id", command_id,
			"--instance-id", instance, "--query", "Status", "--output", "text",
		)
		if status in ("Success", "Failed", "Cancelled", "TimedOut"):
			break

	output = run(
		"aws", "ssm", "get-command-invocation", "--region", region, "--command-id", command_id,
		"--instance-id", instance, "--query", "[StandardOutputContent,StandardErrorContent]", "--output", "text",
	)
	return status, output


def quote_for_ssm(command):
	return '"' + command.replace("\\", "\\\\").replace('"', '\\"') + '"'


def mii_checksum(data):
	"""The checksum kept in the last two bytes of a Mii."""
	checksum = 0
	for value in bytes(data) + b"\x00\x00":
		for bit in range(7, -1, -1):
			carry = checksum & 0x8000
			checksum = ((checksum << 1) | ((value >> bit) & 1)) & 0xFFFF
			if carry:
				checksum ^= 0x1021
	return checksum


def mii_for(name, pid):
	"""The placeholder Mii, renamed and given an ID of its own."""
	mii = bytearray.fromhex(PLACEHOLDER_MII)
	mii[0x1A:0x1A + 20] = name[:10].encode("utf-16-le").ljust(20, b"\x00")
	mii[8:12] = struct.pack("<I", pid)
	mii[94:96] = struct.pack(">H", mii_checksum(mii[:94]))
	return bytes(mii)


def password_hash(pid, password):
	"""What a console sends in place of a password."""
	return hashlib.sha256(struct.pack("<I", pid) + bytes.fromhex("02654346") + password.encode()).hexdigest()


def account_file(username, pid, password):
	uuid = secrets.token_hex(16)
	mii_name = username[:10].encode("utf-16-be").ljust(22, b"\x00").hex()
	lines = [
		"AccountInstance_20120705",
		"PersistentId=80000001",
		"TransferableIdBase=2000004" + uuid[-8:],
		"Uuid=" + uuid,
		"MiiData=" + mii_for(username, pid).hex(),
		"MiiName=" + mii_name,
		"AccountId=" + username,
		"BirthYear=0",
		"BirthMonth=0",
		"BirthDay=0",
		"Gender=0",
		"EmailAddress=",
		"Country=0",
		"SimpleAddressId=0",
		"PrincipalId=%x" % pid,
		"IsPasswordCacheEnabled=1",
		"AccountPasswordCache=" + password_hash(pid, password),
	]
	return "\n".join(lines) + "\n"


def network_services(url):
	unserved = "\n".join(
		"\t\t<%s>%s/unserved/%s</%s>" % (name, url, name, name)
		for name in ("ecs", "nus", "ias", "ccsu", "ccs", "idbe", "boss", "tagaya", "olv")
	)
	return (
		'<?xml version="1.0" encoding="UTF-8"?>\n<content>\n\t<networkname>SMM</networkname>\n'
		# Cemu can only check the account server against a real console's
		# certificates, which a player does not have. The connection is
		# still encrypted
		"\t<disablesslverification>1</disablesslverification>\n\t<urls>\n"
		"\t\t<act>%s</act>\n%s\n\t</urls>\n</content>\n" % (url, unserved)
	)


def dummy_certificates(directory):
	"""Makes a self signed certificate for each name. None can vouch for anything."""
	key = os.path.join(directory, "key.pem")
	made = {}
	for name in SERVER_CERTIFICATES:
		der = os.path.join(directory, name + ".der")
		subprocess.run(
			[
				"openssl", "req", "-x509", "-newkey", "rsa:2048", "-nodes", "-keyout", key, "-outform", "DER",
				"-out", der, "-days", "3650", "-subj", "/O=Placeholder, not a real authority/CN=Placeholder for " + name,
			],
			check=True, capture_output=True,
		)
		made[name + ".der"] = open(der, "rb").read()
	os.remove(key)
	return made


def login_works(url, username, pid, password):
	body = urllib.parse.urlencode({
		"grant_type": "password", "user_id": username,
		"password": password_hash(pid, password), "password_type": "hash",
	}).encode()
	request = urllib.request.Request(url + "/v1/api/oauth20/access_token/generate", data=body, method="POST")
	try:
		return b"<token>" in urllib.request.urlopen(request, timeout=20).read()
	except OSError:
		return False


README = """Joining the Super Mario Maker server with Cemu
===============================================

You need Cemu 2 and your own copy of Super Mario Maker for Wii U, installed
in Cemu with its update.

1. Start Cemu once, so that it makes its folders, and close it again.

2. Open Cemu's data folder. This is the folder that holds settings.xml and
   a folder named mlc01. In Cemu, File > Open Cemu folder takes you there.

3. Copy everything from this zip into that folder, so that the mlc01
   folder here merges with the one there. Replace files when asked.

4. Start Cemu. Open Options > General settings > Account. Pick the account
   named {username}, and set its network service to Custom. Close the
   settings.

5. Start the game and go into Course World.

Good to know

- Makers show without a face. Cemu can not draw Mii faces without files
  from a real Wii U.
- Uploading a course ends with error 115-1101. The course has been
  uploaded all the same: the error is the game failing to post to
  Miiverse, which does not exist here.
- This zip lets whoever has it play as you. Do not pass it on.

Nothing in this zip comes from a real console. otp.bin and seeprom.bin are
blank, and the certificates are dummies that were made for this zip.
"""


def main():
	if len(sys.argv) < 2:
		sys.exit(__doc__)

	username = sys.argv[1]
	out_directory = sys.argv[2] if len(sys.argv) > 2 else os.path.join(ROOT, "local-data", "bundles")

	if not re.fullmatch(r"[A-Za-z0-9._-]{6,16}", username):
		sys.exit("A username is 6 to 16 letters, digits, dots, dashes and underscores")

	url = tofu_output("account_api_url")
	instance = tofu_output("instance_id")
	region = re.search(r"--region (\S+)", tofu_output("connect_command")).group(1)

	# * The player never types this. It only lives on as a hash, in the
	# * account file and on the server
	password = secrets.token_urlsafe(9)[:12]

	status, output = on_server(
		instance, region,
		"docker exec -e SMM_ACCOUNT_PASSWORD=%s smm ./smm-account create %s 2>&1" % (password, username),
	)
	match = re.search(r"PID (\d+)", output)
	if status != "Success" or not match:
		sys.exit("The account could not be created: " + output.strip())

	pid = int(match.group(1))

	if not login_works(url, username, pid, password):
		sys.exit("The account was created, but logging in as it failed. No zip was written")

	os.makedirs(out_directory, exist_ok=True)
	path = os.path.join(out_directory, username + "-cemu.zip")

	with tempfile.TemporaryDirectory() as work, zipfile.ZipFile(path, "w", zipfile.ZIP_DEFLATED) as bundle:
		bundle.writestr("README.txt", README.format(username=username))
		bundle.writestr("network_services.xml", network_services(url))
		bundle.writestr("otp.bin", bytes(1024))
		bundle.writestr("seeprom.bin", bytes(512))
		bundle.writestr(ACCOUNT_FILE, account_file(username, pid, password))

		for name, data in dummy_certificates(work).items():
			bundle.writestr(CERTIFICATE_FOLDER + "/scerts/" + name, data)

		for name in CLIENT_CERTIFICATES:
			bundle.writestr(CERTIFICATE_FOLDER + "/ccerts/" + name, b"\x00")

	os.chmod(path, 0o600)
	print("Created account %s with player ID %d" % (username, pid))
	print("Wrote " + path)
	print("Send that file to the player, and only to them")


if __name__ == "__main__":
	main()
