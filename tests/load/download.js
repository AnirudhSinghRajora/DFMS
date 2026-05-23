// k6 Load Test: Download Throughput
// Run: k6 run tests/load/download.js
// Target: 100 concurrent downloads of pre-uploaded files, sustained 5 minutes.

import http from 'k6/http';
import { check, sleep } from 'k6';
import { Rate, Trend } from 'k6/metrics';

const downloadErrorRate = new Rate('download_errors');
const downloadDuration = new Trend('download_duration_ms');

const BASE_URL = __ENV.BASE_URL || 'http://localhost:8080';

export const options = {
  scenarios: {
    download_load: {
      executor: 'constant-vus',
      vus: 100,
      duration: '5m',
    },
  },
  thresholds: {
    'download_errors': ['rate<0.05'],
    'download_duration_ms': ['p(95)<5000'],
    'http_req_duration': ['p(99)<10000'],
  },
};

// Pre-upload test files and collect their IDs
export function setup() {
  const email = `loadtest-download-${Date.now()}@test.com`;
  const registerRes = http.post(`${BASE_URL}/api/v1/auth/register`, JSON.stringify({
    email: email,
    password: 'loadtest12345',
    display_name: 'Load Test Download',
  }), { headers: { 'Content-Type': 'application/json' } });

  let token;
  if (registerRes.status === 201) {
    token = JSON.parse(registerRes.body).access_token;
  } else {
    const loginRes = http.post(`${BASE_URL}/api/v1/auth/login`, JSON.stringify({
      email: email,
      password: 'loadtest12345',
    }), { headers: { 'Content-Type': 'application/json' } });
    token = JSON.parse(loginRes.body).access_token;
  }

  // Upload 10 files for download testing
  const fileIds = [];
  for (let i = 0; i < 10; i++) {
    const data = new ArrayBuffer(1024 * 1024); // 1MB each
    const res = http.post(`${BASE_URL}/api/v1/files/upload`, data, {
      headers: {
        'Authorization': `Bearer ${token}`,
        'Content-Type': 'application/octet-stream',
        'X-File-Name': `download-test-${i}.bin`,
      },
    });
    if (res.status === 201) {
      try {
        fileIds.push(JSON.parse(res.body).file_id);
      } catch (e) { /* skip */ }
    }
  }

  return { token, fileIds };
}

export default function (data) {
  if (data.fileIds.length === 0) {
    console.error('No files available for download');
    return;
  }

  // Pick a random file to download
  const fileId = data.fileIds[Math.floor(Math.random() * data.fileIds.length)];

  const start = Date.now();
  const res = http.get(`${BASE_URL}/api/v1/files/${fileId}/download`, {
    headers: { 'Authorization': `Bearer ${data.token}` },
    timeout: '15s',
    responseType: 'binary',
  });

  const duration = Date.now() - start;
  downloadDuration.add(duration);

  const success = check(res, {
    'download status is 200': (r) => r.status === 200,
    'has content-length': (r) => r.headers['Content-Length'] !== undefined,
  });

  downloadErrorRate.add(!success);
  sleep(0.2);
}
