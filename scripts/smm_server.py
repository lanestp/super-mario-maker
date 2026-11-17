"""Helpers for the scripts that work on the server deployed with deploy/tofu."""

import os
import shutil
import subprocess
import sys
import time

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
TOFU = os.path.join(ROOT, "deploy", "tofu")


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


def create_console_account(username, pid):
	"""Makes the account a console signed in to another network plays with. Returns its NEX password."""
	import re

	instance = tofu_output("instance_id")
	region = re.search(r"--region (\S+)", tofu_output("connect_command")).group(1)
	status, output = on_server(instance, region, "docker exec smm ./smm-account console %s %d 2>&1" % (username, pid))

	match = re.search(r"^nex_password=(\S+)$", output, re.MULTILINE)
	if status != "Success" or not match:
		sys.exit("The account could not be created: " + output.strip())

	return match.group(1)
