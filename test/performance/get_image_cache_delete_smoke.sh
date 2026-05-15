#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
REPO_ROOT=$(CDPATH= cd -- "${SCRIPT_DIR}/../.." && pwd)

BASE_URL=${BASE_URL:-http://localhost:9090}
ENV_FILE=${ENV_FILE:-"${REPO_ROOT}/.env"}
REDIS_CONTAINER=${REDIS_CONTAINER:-cdn-redis}
GET_IMAGE_BUCKET=${GET_IMAGE_BUCKET:-codeflash-get-image-cache-delete}
GET_IMAGE_OBJECT=${GET_IMAGE_OBJECT:-seeded/favicon.png}
GET_IMAGE_WIDTH=${GET_IMAGE_WIDTH:-16}
GET_IMAGE_HEIGHT=${GET_IMAGE_HEIGHT:-16}
NOTFOUND_FIXTURE=${NOTFOUND_FIXTURE:-"${REPO_ROOT}/public/notfound.png"}
TOKEN=${TOKEN:-}

hash_file() {
    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum "$1" | awk '{print $1}'
        return
    fi

    shasum -a 256 "$1" | awk '{print $1}'
}

if [[ -z "${TOKEN}" && -f "${ENV_FILE}" ]]; then
    TOKEN=$(grep -m1 '^TOKEN=' "${ENV_FILE}" | cut -d= -f2- | tr -d '\r' || true)
    TOKEN=${TOKEN#\"}
    TOKEN=${TOKEN%\"}
fi

if [[ -z "${TOKEN}" ]]; then
    echo "TOKEN not found. Set TOKEN or provide an .env file." >&2
    exit 1
fi

if [[ ! -f "${NOTFOUND_FIXTURE}" ]]; then
    echo "Not-found fixture not found: ${NOTFOUND_FIXTURE}" >&2
    exit 1
fi

GET_IMAGE_BUCKET="${GET_IMAGE_BUCKET}" \
GET_IMAGE_OBJECT="${GET_IMAGE_OBJECT}" \
bash "${SCRIPT_DIR}/seed_get_image_fixture.sh" >/dev/null

cache_key="resize:${GET_IMAGE_BUCKET}:${GET_IMAGE_OBJECT}:${GET_IMAGE_WIDTH}:${GET_IMAGE_HEIGHT}"
sized_url="${BASE_URL%/}/${GET_IMAGE_BUCKET}/w:${GET_IMAGE_WIDTH}/h:${GET_IMAGE_HEIGHT}/${GET_IMAGE_OBJECT}"
delete_url="${BASE_URL%/}/${GET_IMAGE_BUCKET}/${GET_IMAGE_OBJECT}"
remove_bucket_url="${BASE_URL%/}/minio/${GET_IMAGE_BUCKET}/delete"

docker exec "${REDIS_CONTAINER}" redis-cli DEL "${cache_key}" >/dev/null

tmpdir=$(mktemp -d)
trap 'rm -rf "${tmpdir}"' EXIT

prime_status=$(curl -sS -o "${tmpdir}/prime.bin" -D "${tmpdir}/prime.headers" -w '%{http_code}' "${sized_url}")
prime_hash=$(hash_file "${tmpdir}/prime.bin")
notfound_hash=$(hash_file "${NOTFOUND_FIXTURE}")
cache_exists=$(docker exec "${REDIS_CONTAINER}" redis-cli EXISTS "${cache_key}" | tr -d '\r')

if [[ "${prime_status}" != "200" ]]; then
    echo "Expected HTTP 200 while priming ${sized_url}, got ${prime_status}" >&2
    exit 1
fi

if [[ "${cache_exists}" != "1" ]]; then
    echo "Expected Redis cache key ${cache_key} to exist after priming, got ${cache_exists}" >&2
    exit 1
fi

delete_status=$(curl -sS -X DELETE -H "Authorization: Bearer ${TOKEN}" -o "${tmpdir}/delete.json" -w '%{http_code}' "${delete_url}")
post_delete_status=$(curl -sS -o "${tmpdir}/post_delete.bin" -D "${tmpdir}/post_delete.headers" -w '%{http_code}' "${sized_url}")
post_delete_hash=$(hash_file "${tmpdir}/post_delete.bin")

if [[ "${delete_status}" != "200" ]]; then
    echo "Expected HTTP 200 from DELETE ${delete_url}, got ${delete_status}" >&2
    exit 1
fi

if [[ "${post_delete_status}" != "200" ]]; then
    echo "Expected HTTP 200 from sized GET after delete ${sized_url}, got ${post_delete_status}" >&2
    exit 1
fi

if [[ "${post_delete_hash}" == "${prime_hash}" ]]; then
    echo "Expected sized GET after delete not to serve stale cached bytes for ${sized_url}" >&2
    exit 1
fi

if [[ "${post_delete_hash}" != "${notfound_hash}" ]]; then
    echo "Expected sized GET after delete to match ${NOTFOUND_FIXTURE}, got digest ${post_delete_hash}" >&2
    exit 1
fi

remove_bucket_status=$(curl -sS -X DELETE -H "Authorization: Bearer ${TOKEN}" -o "${tmpdir}/remove_bucket.json" -w '%{http_code}' "${remove_bucket_url}")
post_bucket_status=$(curl -sS -o "${tmpdir}/post_bucket.bin" -D "${tmpdir}/post_bucket.headers" -w '%{http_code}' "${sized_url}")
post_bucket_hash=$(hash_file "${tmpdir}/post_bucket.bin")

if [[ "${remove_bucket_status}" != "200" ]]; then
    echo "Expected HTTP 200 from DELETE ${remove_bucket_url}, got ${remove_bucket_status}" >&2
    exit 1
fi

if [[ "${post_bucket_status}" != "200" ]]; then
    echo "Expected HTTP 200 from sized GET after bucket delete ${sized_url}, got ${post_bucket_status}" >&2
    exit 1
fi

if [[ "${post_bucket_hash}" != "${notfound_hash}" ]]; then
    echo "Expected sized GET after bucket delete to match ${NOTFOUND_FIXTURE}, got digest ${post_bucket_hash}" >&2
    exit 1
fi

printf 'GET cache delete smoke passed: %s primed cache key %s, then delete and bucket delete both fell back to notfound.png\n' "${sized_url}" "${cache_key}"
