#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
REPO_ROOT=$(CDPATH= cd -- "${SCRIPT_DIR}/../.." && pwd)

MINIO_CONTAINER=${MINIO_CONTAINER:-cdn-minio}
GET_IMAGE_BUCKET=${GET_IMAGE_BUCKET:-codeflash-get-image-benchmark}
GET_IMAGE_OBJECT=${GET_IMAGE_OBJECT:-seeded/favicon.png}
GET_IMAGE_FIXTURE=${GET_IMAGE_FIXTURE:-"${REPO_ROOT}/public/favicon.png"}
MINIO_TEMP_PATH=${MINIO_TEMP_PATH:-/tmp/codeflash-get-image-fixture.png}

if [[ ! -f "${GET_IMAGE_FIXTURE}" ]]; then
    echo "Fixture not found: ${GET_IMAGE_FIXTURE}" >&2
    exit 1
fi

docker cp "${GET_IMAGE_FIXTURE}" "${MINIO_CONTAINER}:${MINIO_TEMP_PATH}" >/dev/null

docker exec \
    -e TARGET_BUCKET="${GET_IMAGE_BUCKET}" \
    -e TARGET_OBJECT="${GET_IMAGE_OBJECT}" \
    -e TARGET_TEMP_PATH="${MINIO_TEMP_PATH}" \
    "${MINIO_CONTAINER}" \
    sh -lc '
        set -eu
        MC_DIR=/tmp/codeflash-mc
        mkdir -p "${MC_DIR}"
        mc --config-dir "${MC_DIR}" alias set local http://127.0.0.1:9000 "${MINIO_ROOT_USER}" "${MINIO_ROOT_PASSWORD}" >/dev/null
        mc --config-dir "${MC_DIR}" mb --ignore-existing "local/${TARGET_BUCKET}" >/dev/null
        mc --config-dir "${MC_DIR}" cp "${TARGET_TEMP_PATH}" "local/${TARGET_BUCKET}/${TARGET_OBJECT}" >/dev/null
        rm -f "${TARGET_TEMP_PATH}"
    '

printf 'seeded %s/%s\n' "${GET_IMAGE_BUCKET}" "${GET_IMAGE_OBJECT}"
