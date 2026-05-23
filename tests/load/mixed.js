// k6 Load Test: Mixed Workload
// Run: k6 run tests/load/mixed.js
// Target: 30% upload + 60% download + 10% metadata, sustained 10 minutes.

import http from 'k6/http';
import { check, sleep } from 'k6';
import { Rate } from 'k6/metrics';
import { randomBytes } from 'k6/crypto';

const errorRate = new Rate('mixed_errors');

const BASE_URL = __ENV.BASE_URL || 'http://localhost:8080';

export const options = {
  scenarios: {
    mixed_workload: {
      executor: 'constant-vus',
      vus: 50,
      duration: '10m',
    },
  },
  thresholds: {
    'mixed_errors': ['rate<0.05'],
    'http_req_duration': ['p(95)<10000', 'p(99)<20000'],
  },
};

export function setup() {
  const email = `loadtest-mixed-${Date.now()}@test.com`;
  const registerRes = http.post(`${BASE_URL}/api/v1/auth/register`, JSON.stringify({
    email: email,
    password: 'loadtest12345',
    display_name: 'Load Test Mixed',
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

  // Seed some files
  const fileIds = [];
  for (let i = 0; i < 5; i++) {
    const data = new ArrayBuffer(512 * 1024); // 512KB
    const res = http.post(`${BASE_URL}/api/v1/files/upload`, data, {
      headers: {
        'Authorization': `Bearer ${token}`,
        'Content-Type': 'application/octet-stream',
        'X-File-Name': `mixed-seed-${i}.bin`,
      },
    });
    if (res.status === 201) {
      try { fileIds.push(JSON.parse(res.body).file_id); } catch (e) {}
    }
  }

  return { token, fileIds };
}

export default function (data) {
  const roll = Math.random();
  let success;

  if (roll < 0.3) {
    // 30% — Upload
    const fileData = randomBytes(1024 * 1024); // 1MB
    const res = http.post(`${BASE_URL}/api/v1/files/upload`, fileData, {
      headers: {
        'Authorization': `Bearer ${data.token}`,
        'Content-Type': 'application/octet-stream',
        'X-File-Name': `mixed-upload-${__VU}-${__ITER}.bin`,
      },
      timeout: '15s',
    });

    success = check(res, { 'upload 201': (r) => r.status === 201 });

    // Track uploaded file for future downloads
    if (res.status === 201) {
      try { data.fileIds.push(JSON.parse(res.body).file_id); } catch (e) {}
    }
  } else if (roll < 0.9) {
    // 60% — Download
    if (data.fileIds.length === 0) {
      sleep(0.5);
      return;
    }
    const fileId = data.fileIds[Math.floor(Math.random() * data.fileIds.length)];
    const res = http.get(`${BASE_URL}/api/v1/files/${fileId}/download`, {
      headers: { 'Authorization': `Bearer ${data.token}` },
      timeout: '10s',
      responseType: 'binary',
    });

    success = check(res, { 'download 200': (r) => r.status === 200 });
  } else {
    // 10% — Metadata (list files)
    const res = http.get(`${BASE_URL}/api/v1/files?page=1&page_size=20`, {
      headers: { 'Authorization': `Bearer ${data.token}` },
      timeout: '5s',
    });

    success = check(res, {
      'list 200': (r) => r.status === 200,
      'p99 < 100ms': (r) => r.timings.duration < 100,
    });
  }

  errorRate.add(!success);
  sleep(0.3);
}
