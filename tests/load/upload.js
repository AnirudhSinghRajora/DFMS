// k6 Load Test: Upload Throughput
// Run: k6 run tests/load/upload.js
// Target: 50 concurrent uploads of 10MB files, sustained 5 minutes.

import http from 'k6/http';
import { check, sleep } from 'k6';
import { Rate, Trend } from 'k6/metrics';
import { randomBytes } from 'k6/crypto';

const uploadErrorRate = new Rate('upload_errors');
const uploadDuration = new Trend('upload_duration_ms');

const BASE_URL = __ENV.BASE_URL || 'http://localhost:8080';

export const options = {
  scenarios: {
    upload_load: {
      executor: 'constant-vus',
      vus: 50,
      duration: '5m',
    },
  },
  thresholds: {
    'upload_errors': ['rate<0.05'],          // < 5% error rate
    'upload_duration_ms': ['p(95)<10000'],   // p95 < 10s
    'http_req_duration': ['p(99)<15000'],    // p99 < 15s
  },
};

// Pre-register a test user and get auth token
export function setup() {
  const email = `loadtest-upload-${Date.now()}@test.com`;
  const registerRes = http.post(`${BASE_URL}/api/v1/auth/register`, JSON.stringify({
    email: email,
    password: 'loadtest12345',
    display_name: 'Load Test Upload',
  }), { headers: { 'Content-Type': 'application/json' } });

  if (registerRes.status !== 201) {
    // User might already exist, try login
    const loginRes = http.post(`${BASE_URL}/api/v1/auth/login`, JSON.stringify({
      email: email,
      password: 'loadtest12345',
    }), { headers: { 'Content-Type': 'application/json' } });

    return { token: JSON.parse(loginRes.body).access_token };
  }

  return { token: JSON.parse(registerRes.body).access_token };
}

export default function (data) {
  // Generate 10MB of random data
  const fileData = randomBytes(10 * 1024 * 1024);
  const fileName = `loadtest-${__VU}-${__ITER}.bin`;

  const start = Date.now();
  const res = http.post(`${BASE_URL}/api/v1/files/upload`, fileData, {
    headers: {
      'Authorization': `Bearer ${data.token}`,
      'Content-Type': 'application/octet-stream',
      'X-File-Name': fileName,
    },
    timeout: '30s',
  });

  const duration = Date.now() - start;
  uploadDuration.add(duration);

  const success = check(res, {
    'upload status is 201': (r) => r.status === 201,
    'response has file_id': (r) => {
      try { return JSON.parse(r.body).file_id !== undefined; } catch { return false; }
    },
  });

  uploadErrorRate.add(!success);
  sleep(0.5); // Small pause between uploads
}
