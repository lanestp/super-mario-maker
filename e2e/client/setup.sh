#!/bin/sh
# Makes a Python environment for the game protocol client the e2e tests
# use, in e2e/client/.venv
#
#   e2e/client/setup.sh [python]
#
# python is the Python 3.12+ to use, python3 when not given. Afterwards
# run the tests with
#
#   PN_SMM_E2E_PYTHON=e2e/client/.venv/bin/python go test ./e2e/
set -eu

python="${1:-python3}"
here="$(cd "$(dirname "$0")" && pwd)"

"$python" -c 'import sys; sys.exit(0 if sys.version_info >= (3, 12) else 1)' || {
	echo "Python 3.12 or newer is needed, $python is $("$python" --version 2>&1)" >&2
	exit 1
}

"$python" -m venv "$here/.venv"

"$here/.venv/bin/pip" install --quiet --no-deps -r "$here/requirements.txt"
"$here/.venv/bin/pip" install --quiet pycryptodome anyio multidict pyopenssl netifaces-plus

"$here/.venv/bin/python" -c 'from nintendo.nex import backend, datastore_smm' 

echo "Ready. Run the tests with PN_SMM_E2E_PYTHON=$here/.venv/bin/python"
