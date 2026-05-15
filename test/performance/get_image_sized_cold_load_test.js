import http from 'k6/http';
import { check, sleep } from 'k6';

const BASE_URL = (__ENV.BASE_URL || 'http://localhost:9090').replace(/\/$/, '');
const GET_IMAGE_BUCKET = __ENV.GET_IMAGE_BUCKET || 'codeflash-get-image-sized-miss-benchmark';
const GET_IMAGE_OBJECT_PREFIX = __ENV.GET_IMAGE_OBJECT_PREFIX || 'cold';
const GET_IMAGE_OBJECT_BASENAME = __ENV.GET_IMAGE_OBJECT_BASENAME || 'favicon.png';
const GET_IMAGE_OBJECTS_PER_VU = Number(__ENV.GET_IMAGE_OBJECTS_PER_VU || '128');
const GET_IMAGE_OBJECT_COUNT = Number(__ENV.GET_IMAGE_OBJECT_COUNT || '640');
const GET_IMAGE_WIDTH = __ENV.GET_IMAGE_WIDTH || '16';
const GET_IMAGE_HEIGHT = __ENV.GET_IMAGE_HEIGHT || '16';

export const options = {
    summaryTrendStats: ['avg', 'med', 'p(90)', 'p(95)', 'min', 'max'],
    scenarios: {
        get_image_sized_cold: {
            executor: 'ramping-vus',
            exec: 'getImageSizedColdScenario',
            startVUs: 1,
            gracefulRampDown: '5s',
            stages: [
                { duration: '15s', target: 5 },
                { duration: '45s', target: 5 },
                { duration: '10s', target: 0 },
            ],
            tags: {
                endpoint: 'get-image-sized-cold',
            },
        },
    },
    thresholds: {
        'checks{scenario:get_image_sized_cold}': ['rate==1.0'],
        'http_req_failed{scenario:get_image_sized_cold}': ['rate==0'],
    },
};

function getImageSizedSucceeded(response) {
    const contentType = response.headers['Content-Type'] || '';
    const width = response.headers.Width || response.headers.width || '';
    const height = response.headers.Height || response.headers.height || '';

    return response.status === 200 &&
        contentType.includes('image/') &&
        response.body &&
        response.body.length > 0 &&
        width === GET_IMAGE_WIDTH &&
        height === GET_IMAGE_HEIGHT;
}

function getObjectName() {
    const objectIndex = ((__VU - 1) * GET_IMAGE_OBJECTS_PER_VU) + __ITER;
    if (objectIndex >= GET_IMAGE_OBJECT_COUNT) {
        throw new Error(`insufficient seeded objects for cold sized GET benchmark: index=${objectIndex} count=${GET_IMAGE_OBJECT_COUNT}`);
    }

    const paddedIndex = String(objectIndex).padStart(4, '0');
    return `${GET_IMAGE_OBJECT_PREFIX}/${paddedIndex}-${GET_IMAGE_OBJECT_BASENAME}`;
}

function buildRequestParams() {
    return {
        headers: {
            // Distinct tokens keep each VU below the shared 100 req/min limiter.
            Authorization: `Bearer get-image-sized-cold-benchmark-vu${__VU}`,
        },
        tags: {
            name: 'GET /:bucket/w:width/h:height/* cold miss',
        },
    };
}

export function getImageSizedColdScenario() {
    const objectName = getObjectName();
    const url = `${BASE_URL}/${GET_IMAGE_BUCKET}/w:${GET_IMAGE_WIDTH}/h:${GET_IMAGE_HEIGHT}/${objectName}`;
    const response = http.get(url, buildRequestParams());

    check(response, {
        'cold sized get image request succeeds': getImageSizedSucceeded,
    });

    sleep(0.65);
}
