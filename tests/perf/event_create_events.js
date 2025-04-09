import http from 'k6/http';
import { check } from 'k6';

const test_target = 25000;

export let options = {
  stages: [
    { duration: '10s', target: test_target / 10 },
    { duration: '30s', target: test_target },
    { duration: '10s', target: 0 },
  ],
  rps: test_target,
};

export default function () {
  const uniqueId = `${__VU}-${__ITER}-${Math.random().toString(36).substring(2, 10)}`;
  const now = new Date().toISOString();

  const payload = JSON.stringify({
    source_id: 0,
    event_type: "test_event",
    event_severity: "low",
    timestamp: now,
    details: {
      mode: "Test",
      ts: now,
      unique_id: uniqueId
    }
  });

  const headers = {
    'Content-Type': 'application/json',
    'User-Agent': 'k6-Events-Test'
  };

  const res = http.post('http://localhost:8082/v1/event/create', payload, { headers, timeout: '10s' });

  check(res, {
    'is status 201': (r) => r.status === 201,
    'has body': (r) => r.body && r.body.length > 0
  });
}
