"""Plays the part of a console for the e2e tests.

Logs in to the game server over the real game protocol, uploads a course,
reads it back and rates it, then prints what it saw as JSON. Settings come
from environment variables, which the Go test sets:

  SMM_HOST, SMM_PORT            the authentication server
  SMM_PID, SMM_NEX_PASSWORD     the account to log in with
"""

import json
import logging
import os
import sys
import urllib.request
import uuid

import anyio

from nintendo.nex import authentication, backend, datastore_smm, settings

# Super Mario Maker's access key and the NEX version it uses
ACCESS_KEY = "9f2b4678"
NEX_VERSION = 30803

COURSE_DATA_TYPE = 6


def upload(info, data):
    """Uploads data the way a console does, as a form with the given fields."""
    boundary = uuid.uuid4().hex
    body = b""

    for field in info.form:
        body += (
            f"--{boundary}\r\n"
            f'Content-Disposition: form-data; name="{field.key}"\r\n\r\n'
            f"{field.value}\r\n"
        ).encode()

    body += (
        f"--{boundary}\r\n"
        'Content-Disposition: form-data; name="file"; filename="course"\r\n'
        "Content-Type: application/octet-stream\r\n\r\n"
    ).encode() + data + f"\r\n--{boundary}--\r\n".encode()

    request = urllib.request.Request(info.url, data=body, method="POST")
    request.add_header("Content-Type", f"multipart/form-data; boundary={boundary}")

    with urllib.request.urlopen(request, timeout=10) as response:
        return response.status


def download(url):
    with urllib.request.urlopen(url, timeout=10) as response:
        return response.read()


async def main():
    host = os.environ["SMM_HOST"]
    port = int(os.environ["SMM_PORT"])
    pid = os.environ["SMM_PID"]
    password = os.environ["SMM_NEX_PASSWORD"]

    course = bytes(range(256)) * 16
    result = {"pid": int(pid)}

    s = settings.default()
    s.configure(ACCESS_KEY, NEX_VERSION)

    # The server reads and writes structures with a version header, as
    # this version of NEX does. The client does not turn that on by itself
    s["nex.struct_header"] = 1

    # nex-go v2.1.2 signs its reply to the first packet before copying the
    # client's minor version and supported functions into it, so the reply
    # only has a valid signature when the client sends 0 for both
    s["prudp.minor_version"] = 0
    s["prudp.supported_functions"] = 0

    async with backend.connect(s, host, port) as be:
        # A console logs in with the token the account server gave it. The
        # game server does not check the token, but refuses a login without one
        auth_info = authentication.AuthenticationInfo()
        auth_info.token = os.environ.get("SMM_NEX_TOKEN", "e2e-token")
        auth_info.ngs_version = 3
        auth_info.token_type = 1
        auth_info.server_version = 0

        async with be.login(pid, password, auth_info) as client:
            store = datastore_smm.DataStoreClientSMM(client)

            result["application_config_length"] = len(await store.get_application_config(0))

            param = datastore_smm.DataStorePreparePostParam()
            param.size = len(course)
            param.name = "E2E Course"
            param.data_type = COURSE_DATA_TYPE
            param.meta_binary = b"\x01\x02\x03\x04"
            param.permission.permission = 0
            param.delete_permission.permission = 3
            param.flag = 256
            param.period = 90
            param.tags = ["e2e"]
            param.extra_data = []

            for slot in range(2):
                rating = datastore_smm.DataStoreRatingInitParamWithSlot()
                rating.slot = slot
                rating.param.flag = 0
                rating.param.internal_flag = 0
                rating.param.lock_type = 0
                rating.param.initial_value = 0
                rating.param.range_min = 0
                rating.param.range_max = 100
                rating.param.period_hour = 0
                rating.param.period_duration = 0
                param.rating_init_param.append(rating)

            post_info = await store.prepare_post_object(param)
            result["data_id"] = post_info.data_id
            result["upload_url"] = post_info.url
            result["upload_status"] = upload(post_info, course)

            complete = datastore_smm.DataStoreCompletePostParam()
            complete.data_id = post_info.data_id
            complete.success = True
            await store.complete_post_object(complete)

            get_meta = datastore_smm.DataStoreGetMetaParam()
            get_meta.data_id = post_info.data_id
            get_meta.result_option = 0xFF
            meta = await store.get_meta(get_meta)

            result["meta"] = {
                "data_id": meta.data_id,
                "owner_id": meta.owner_id,
                "size": meta.size,
                "name": meta.name,
                "data_type": meta.data_type,
                "meta_binary": meta.meta_binary.hex(),
                "flag": meta.flag,
                "period": meta.period,
                "tags": meta.tags,
                "ratings": len(meta.ratings),
            }

            prepare_get = datastore_smm.DataStorePrepareGetParam()
            prepare_get.data_id = post_info.data_id
            get_info = await store.prepare_get_object(prepare_get)

            result["download_url"] = get_info.url
            result["download_size"] = get_info.size
            result["download_matches"] = download(get_info.url) == course

            target = datastore_smm.DataStoreRatingTarget()
            target.data_id = post_info.data_id
            target.slot = 0

            rate = datastore_smm.DataStoreRateObjectParam()
            rate.rating_value = 5
            rate.access_password = 0

            await store.rate_object(target, rate, True)
            rating = await store.rate_object(target, rate, True)

            result["rating_total"] = rating.total_value
            result["rating_count"] = rating.count

    print(json.dumps(result))


if __name__ == "__main__":
    if os.environ.get("SMM_DEBUG"):
        logging.basicConfig(level=logging.DEBUG)

    try:
        anyio.run(main)
    except Exception as error:
        print(f"client failed: {type(error).__name__}: {error}", file=sys.stderr)
        raise
