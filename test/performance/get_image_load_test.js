import http from 'k6/http';
import { check, sleep } from 'k6';

const BASE_URL = (__ENV.BASE_URL || 'http://localhost:9090').replace(/\/$/, '');
const GET_IMAGE_BUCKET = __ENV.GET_IMAGE_BUCKET || 'codeflash-get-image-benchmark';
const GET_IMAGE_OBJECT = __ENV.GET_IMAGE_OBJECT || 'seeded/favicon.png';
const GET_IMAGE_URL = `${BASE_URL}/${GET_IMAGE_BUCKET}/${GET_IMAGE_OBJECT}`;

export const options = {
    summaryTrendStats: ['avg', 'med', 'p(90)', 'p(95)', 'min', 'max'],
    scenarios: {
        get_image: {
            executor: 'ramping-vus',
            exec: 'getImageScenario',
            startVUs: 1,
            gracefulRampDown: '5s',
            stages: [
                { duration: '15s', target: 5 },
                { duration: '45s', target: 5 },
                { duration: '10s', target: 0 },
            ],
            tags: {
                endpoint: 'get-image',
            },
        },
    },
    thresholds: {
        'checks{scenario:get_image}': ['rate==1.0'],
        'http_req_failed{scenario:get_image}': ['rate==0'],
    },
};

function getImageSucceeded(response) {
    const contentType = response.headers['Content-Type'] || '';

    return response.status === 200 &&
        contentType.includes('image/') &&
        response.body &&
        response.body.length > 0;
}

function buildRequestParams() {
    return {
        headers: {
            // Distinct tokens keep each VU under the shared 100 req/min limiter without requiring auth on GET.
            Authorization: `Bearer get-image-benchmark-vu${__VU}`,
        },
        tags: {
            name: 'GET /:bucket/*',
        },
    };
}

export function getImageScenario() {
    const response = http.get(GET_IMAGE_URL, buildRequestParams());

    check(response, {
        'get image request succeeds': getImageSucceeded,
    });

    sleep(0.65);
}
