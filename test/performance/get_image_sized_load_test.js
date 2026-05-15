import http from 'k6/http';
import { check, sleep } from 'k6';

const BASE_URL = (__ENV.BASE_URL || 'http://localhost:9090').replace(/\/$/, '');
const GET_IMAGE_BUCKET = __ENV.GET_IMAGE_BUCKET || 'codeflash-get-image-benchmark';
const GET_IMAGE_OBJECT = __ENV.GET_IMAGE_OBJECT || 'seeded/favicon.png';
const GET_IMAGE_WIDTH = __ENV.GET_IMAGE_WIDTH || '16';
const GET_IMAGE_HEIGHT = __ENV.GET_IMAGE_HEIGHT || '16';
const GET_IMAGE_URL = `${BASE_URL}/${GET_IMAGE_BUCKET}/w:${GET_IMAGE_WIDTH}/h:${GET_IMAGE_HEIGHT}/${GET_IMAGE_OBJECT}`;

export const options = {
    summaryTrendStats: ['avg', 'med', 'p(90)', 'p(95)', 'min', 'max'],
    scenarios: {
        get_image_sized: {
            executor: 'ramping-vus',
            exec: 'getImageSizedScenario',
            startVUs: 1,
            gracefulRampDown: '5s',
            stages: [
                { duration: '15s', target: 5 },
                { duration: '45s', target: 5 },
                { duration: '10s', target: 0 },
            ],
            tags: {
                endpoint: 'get-image-sized',
            },
        },
    },
    thresholds: {
        'checks{scenario:get_image_sized}': ['rate==1.0'],
        'http_req_failed{scenario:get_image_sized}': ['rate==0'],
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

function buildRequestParams() {
    return {
        headers: {
            // Distinct tokens keep each VU below the shared 100 req/min limiter.
            Authorization: `Bearer get-image-sized-benchmark-vu${__VU}`,
        },
        tags: {
            name: 'GET /:bucket/w:width/h:height/*',
        },
    };
}

export function getImageSizedScenario() {
    const response = http.get(GET_IMAGE_URL, buildRequestParams());

    check(response, {
        'sized get image request succeeds': getImageSizedSucceeded,
    });

    sleep(0.65);
}
