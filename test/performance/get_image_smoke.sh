#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)

BASE_URL=${BASE_URL:-http://localhost:9090}
GET_IMAGE_BUCKET=${GET_IMAGE_BUCKET:-codeflash-get-image-benchmark}
GET_IMAGE_OBJECT=${GET_IMAGE_OBJECT:-seeded/favicon.png}

hash_file() {
    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum "$1" | awk '{print $1}'
        return
    fi

    shasum -a 256 "$1" | awk '{print $1}'
}

bash "${SCRIPT_DIR}/seed_get_image_fixture.sh" >/dev/null

tmpdir=$(mktemp -d)
trap 'rm -rf "${tmpdir}"' EXIT

url="${BASE_URL%/}/${GET_IMAGE_BUCKET}/${GET_IMAGE_OBJECT}"

first_status=$(curl -sS -o "${tmpdir}/first.bin" -D "${tmpdir}/first.headers" -w '%{http_code}' "${url}")
second_status=$(curl -sS -o "${tmpdir}/second.bin" -D "${tmpdir}/second.headers" -w '%{http_code}' "${url}")

first_type=$(tr -d '\r' < "${tmpdir}/first.headers" | awk 'BEGIN{IGNORECASE=1} /^Content-Type:/ {print $2; exit}')
second_type=$(tr -d '\r' < "${tmpdir}/second.headers" | awk 'BEGIN{IGNORECASE=1} /^Content-Type:/ {print $2; exit}')

first_size=$(wc -c < "${tmpdir}/first.bin" | tr -d ' ')
second_size=$(wc -c < "${tmpdir}/second.bin" | tr -d ' ')

first_hash=$(hash_file "${tmpdir}/first.bin")
second_hash=$(hash_file "${tmpdir}/second.bin")

if [[ "${first_status}" != "200" || "${second_status}" != "200" ]]; then
    echo "Expected HTTP 200 for ${url}, got ${first_status} and ${second_status}" >&2
    exit 1
fi

if [[ "${first_type}" != image/* || "${second_type}" != image/* ]]; then
    echo "Expected image content type for ${url}, got ${first_type} and ${second_type}" >&2
    exit 1
fi

if [[ "${first_size}" -le 0 || "${second_size}" -le 0 ]]; then
    echo "Expected non-empty image body for ${url}, got ${first_size} and ${second_size} bytes" >&2
    exit 1
fi

if [[ "${first_hash}" != "${second_hash}" ]]; then
    echo "Expected stable image body for ${url}, got digests ${first_hash} and ${second_hash}" >&2
    exit 1
fi

printf 'GET smoke passed for %s (content-type=%s size=%s sha256=%s)\n' "${url}" "${first_type}" "${first_size}" "${first_hash}"
