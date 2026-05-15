#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
REPO_ROOT=$(CDPATH= cd -- "${SCRIPT_DIR}/../.." && pwd)

BASE_URL=${BASE_URL:-http://localhost:9090}
GET_IMAGE_BUCKET=${GET_IMAGE_BUCKET:-codeflash-get-image-benchmark}
GET_IMAGE_OBJECT=${GET_IMAGE_OBJECT:-seeded/favicon.png}
GET_IMAGE_FIXTURE=${GET_IMAGE_FIXTURE:-"${REPO_ROOT}/public/favicon.png"}
GET_IMAGE_WIDTH=${GET_IMAGE_WIDTH:-16}
GET_IMAGE_HEIGHT=${GET_IMAGE_HEIGHT:-16}

hash_file() {
    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum "$1" | awk '{print $1}'
        return
    fi

    shasum -a 256 "$1" | awk '{print $1}'
}

if [[ ! -f "${GET_IMAGE_FIXTURE}" ]]; then
    echo "Fixture not found: ${GET_IMAGE_FIXTURE}" >&2
    exit 1
fi

if ! command -v identify >/dev/null 2>&1; then
    echo "ImageMagick identify is required for the GET smoke check" >&2
    exit 1
fi

expected_resize_dimensions() {
    local width=$1
    local height=$2
    local target_width=$3
    local target_height=$4

    awk \
        -v width="${width}" \
        -v height="${height}" \
        -v target_width="${target_width}" \
        -v target_height="${target_height}" '
            BEGIN {
                if (target_width == 0 && target_height == 0) {
                    printf "%dx%d\n", width, height
                    exit
                }

                if (target_width == 0) {
                    printf "%dx%d\n", int(target_height * (width / height)), target_height
                    exit
                }

                if (target_height == 0) {
                    printf "%dx%d\n", target_width, int(target_width * (height / width))
                    exit
                }

                scale_width = target_width / width
                scale_height = target_height / height
                scale = scale_width
                if (scale_height < scale) {
                    scale = scale_height
                }

                if (scale > 1) {
                    printf "%dx%d\n", width, height
                    exit
                }

                printf "%dx%d\n", int(width * scale), int(height * scale)
            }
        '
}

bash "${SCRIPT_DIR}/seed_get_image_fixture.sh" >/dev/null

tmpdir=$(mktemp -d)
trap 'rm -rf "${tmpdir}"' EXIT

plain_url="${BASE_URL%/}/${GET_IMAGE_BUCKET}/${GET_IMAGE_OBJECT}"
sized_url="${BASE_URL%/}/${GET_IMAGE_BUCKET}/w:${GET_IMAGE_WIDTH}/h:${GET_IMAGE_HEIGHT}/${GET_IMAGE_OBJECT}"

plain_status=$(curl -sS -o "${tmpdir}/plain.bin" -D "${tmpdir}/plain.headers" -w '%{http_code}' "${plain_url}")
sized_status=$(curl -sS -o "${tmpdir}/sized.bin" -D "${tmpdir}/sized.headers" -w '%{http_code}' "${sized_url}")

plain_type=$(tr -d '\r' < "${tmpdir}/plain.headers" | awk 'BEGIN{IGNORECASE=1} /^Content-Type:/ {print $2; exit}')
sized_type=$(tr -d '\r' < "${tmpdir}/sized.headers" | awk 'BEGIN{IGNORECASE=1} /^Content-Type:/ {print $2; exit}')

plain_size=$(wc -c < "${tmpdir}/plain.bin" | tr -d ' ')
sized_size=$(wc -c < "${tmpdir}/sized.bin" | tr -d ' ')

fixture_hash=$(hash_file "${GET_IMAGE_FIXTURE}")
plain_hash=$(hash_file "${tmpdir}/plain.bin")

read -r fixture_width fixture_height <<<"$(identify -format '%w %h' "${GET_IMAGE_FIXTURE}")"
plain_dimensions=$(identify -format '%wx%h' "${tmpdir}/plain.bin")
sized_dimensions=$(identify -format '%wx%h' "${tmpdir}/sized.bin")
expected_sized_dimensions=$(expected_resize_dimensions "${fixture_width}" "${fixture_height}" "${GET_IMAGE_WIDTH}" "${GET_IMAGE_HEIGHT}")

if [[ "${plain_status}" != "200" || "${sized_status}" != "200" ]]; then
    echo "Expected HTTP 200 for ${plain_url} and ${sized_url}, got ${plain_status} and ${sized_status}" >&2
    exit 1
fi

if [[ "${plain_type}" != image/* || "${sized_type}" != image/* ]]; then
    echo "Expected image content type for ${plain_url} and ${sized_url}, got ${plain_type} and ${sized_type}" >&2
    exit 1
fi

if [[ "${plain_size}" -le 0 || "${sized_size}" -le 0 ]]; then
    echo "Expected non-empty image body for ${plain_url} and ${sized_url}, got ${plain_size} and ${sized_size} bytes" >&2
    exit 1
fi

if [[ "${plain_hash}" != "${fixture_hash}" ]]; then
    echo "Expected plain GET body for ${plain_url} to match ${GET_IMAGE_FIXTURE}, got digests ${plain_hash} and ${fixture_hash}" >&2
    exit 1
fi

if [[ "${plain_dimensions}" != "${fixture_width}x${fixture_height}" ]]; then
    echo "Expected plain GET dimensions ${fixture_width}x${fixture_height} for ${plain_url}, got ${plain_dimensions}" >&2
    exit 1
fi

if [[ "${sized_dimensions}" != "${expected_sized_dimensions}" ]]; then
    echo "Expected sized GET dimensions ${expected_sized_dimensions} for ${sized_url}, got ${sized_dimensions}" >&2
    exit 1
fi

printf 'GET smoke passed: plain %s matches fixture sha256=%s and sized %s returns %s\n' "${plain_url}" "${plain_hash}" "${sized_url}" "${sized_dimensions}"
