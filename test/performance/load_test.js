import http from 'k6/http';
import { check, sleep } from 'k6';

const BASE_URL = __ENV.BASE_URL || 'http://localhost:9090';
const RESIZE_WIDTH = __ENV.RESIZE_WIDTH || '16';
const RESIZE_HEIGHT = __ENV.RESIZE_HEIGHT || '16';

function loadFixture() {
    const candidates = [
        'public/favicon.png',
        '../../public/favicon.png',
    ];

    for (const path of candidates) {
        try {
            return open(path, 'b');
        } catch (error) {
            // Try the next repo-relative location.
        }
    }

    throw new Error(`Unable to load resize fixture from: ${candidates.join(', ')}`);
}

const RESIZE_FIXTURE = loadFixture();

export const options = {
    summaryTrendStats: ['avg', 'med', 'p(90)', 'p(95)', 'min', 'max'],
    scenarios: {
        resize: {
            executor: 'ramping-vus',
            exec: 'resizeScenario',
            startVUs: 1,
            gracefulRampDown: '5s',
            stages: [
                { duration: '15s', target: 5 },
                { duration: '45s', target: 5 },
                { duration: '10s', target: 0 },
            ],
            tags: {
                endpoint: 'resize',
            },
        },
    },
    thresholds: {
        'checks{scenario:resize}': ['rate==1.0'],
        'http_req_failed{scenario:resize}': ['rate==0'],
    },
};

function resizeSucceeded(response) {
    if (response.status !== 200) {
        return false;
    }

    try {
        return response.json('success') === true &&
            response.json('message') === 'Image processed successfully';
    } catch (error) {
        return false;
    }
}

function buildResizePayload() {
    return {
        width: RESIZE_WIDTH,
        height: RESIZE_HEIGHT,
        file: http.file(RESIZE_FIXTURE, 'favicon.png', 'image/png'),
    };
}

function buildRequestParams() {
    return {
        headers: {
            Authorization: `Bearer resize-benchmark-vu${__VU}`,
        },
        tags: {
            name: 'POST /resize',
        },
    };
}

export function resizeScenario() {
    const response = http.post(`${BASE_URL}/resize`, buildResizePayload(), buildRequestParams());

    check(response, {
        'resize request succeeds': resizeSucceeded,
    });

    // Keep each VU below the 100 req/min limiter while still exercising resize continuously.
    sleep(0.65);
}
