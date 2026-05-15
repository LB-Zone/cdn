#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
REPO_ROOT=$(CDPATH= cd -- "${SCRIPT_DIR}/../.." && pwd)

MINIO_CONTAINER=${MINIO_CONTAINER:-cdn-minio}
REDIS_CONTAINER=${REDIS_CONTAINER:-cdn-redis}
GET_IMAGE_BUCKET=${GET_IMAGE_BUCKET:-codeflash-get-image-sized-miss-benchmark}
GET_IMAGE_OBJECT_PREFIX=${GET_IMAGE_OBJECT_PREFIX:-cold}
GET_IMAGE_OBJECT_BASENAME=${GET_IMAGE_OBJECT_BASENAME:-favicon.png}
GET_IMAGE_OBJECT_COUNT=${GET_IMAGE_OBJECT_COUNT:-640}
GET_IMAGE_FIXTURE=${GET_IMAGE_FIXTURE:-"${REPO_ROOT}/public/favicon.png"}
MINIO_TEMP_PATH=${MINIO_TEMP_PATH:-/tmp/codeflash-get-image-sized-cold-fixture.png}

if [[ ! -f "${GET_IMAGE_FIXTURE}" ]]; then
    echo "Fixture not found: ${GET_IMAGE_FIXTURE}" >&2
    exit 1
fi

if ! [[ "${GET_IMAGE_OBJECT_COUNT}" =~ ^[0-9]+$ ]] || [[ "${GET_IMAGE_OBJECT_COUNT}" -le 0 ]]; then
    echo "GET_IMAGE_OBJECT_COUNT must be a positive integer, got: ${GET_IMAGE_OBJECT_COUNT}" >&2
    exit 1
fi

docker cp "${GET_IMAGE_FIXTURE}" "${MINIO_CONTAINER}:${MINIO_TEMP_PATH}" >/dev/null

docker exec \
    -e TARGET_BUCKET="${GET_IMAGE_BUCKET}" \
    -e TARGET_PREFIX="${GET_IMAGE_OBJECT_PREFIX}" \
    -e TARGET_BASENAME="${GET_IMAGE_OBJECT_BASENAME}" \
    -e TARGET_COUNT="${GET_IMAGE_OBJECT_COUNT}" \
    -e TARGET_TEMP_PATH="${MINIO_TEMP_PATH}" \
    "${MINIO_CONTAINER}" \
    sh -lc '
        set -eu
        MC_DIR=/tmp/codeflash-mc
        mkdir -p "${MC_DIR}"
        mc --config-dir "${MC_DIR}" alias set local http://127.0.0.1:9000 "${MINIO_ROOT_USER}" "${MINIO_ROOT_PASSWORD}" >/dev/null
        mc --config-dir "${MC_DIR}" mb --ignore-existing "local/${TARGET_BUCKET}" >/dev/null
        mc --config-dir "${MC_DIR}" rm --recursive --force "local/${TARGET_BUCKET}/${TARGET_PREFIX}" >/dev/null 2>&1 || true

        i=0
        while [ "${i}" -lt "${TARGET_COUNT}" ]; do
            object_name=$(printf "%s/%04d-%s" "${TARGET_PREFIX}" "${i}" "${TARGET_BASENAME}")
            mc --config-dir "${MC_DIR}" cp "${TARGET_TEMP_PATH}" "local/${TARGET_BUCKET}/${object_name}" >/dev/null
            i=$((i + 1))
        done

        rm -f "${TARGET_TEMP_PATH}"
    '

docker exec \
    -e CACHE_PATTERN="resize:${GET_IMAGE_BUCKET}:*" \
    "${REDIS_CONTAINER}" \
    sh -lc '
        set -eu
        redis-cli --scan --pattern "${CACHE_PATTERN}" | while IFS= read -r key; do
            if [ -n "${key}" ]; then
                redis-cli DEL "${key}" >/dev/null
            fi
        done
    '

printf 'seeded %s/%s with %s objects and cleared matching resize cache keys\n' "${GET_IMAGE_BUCKET}" "${GET_IMAGE_OBJECT_PREFIX}" "${GET_IMAGE_OBJECT_COUNT}"
