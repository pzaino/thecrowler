import http from 'k6/http';
import { check } from 'k6';

const test_target = 1000;

export let options = {
  stages: [
    { duration: '10s', target: test_target / 10 },
    { duration: '30s', target: test_target },
    { duration: '10s', target: 0 },
  ],
  rps: test_target,
};

export default function () {
  const payload = JSON.stringify({
    source_id: 0,  // leave source ID to zero so test can be performed even with empty DB
    event_type: "test_event",
    event_severity: "low",
    details: {
      Mode: "Test",
      timestamp: new Date().toISOString()  // help avoid deduplication
    }
  });

  const headers = {
    'Content-Type': 'application/json',
    'User-Agent': 'k6-Events-Test'
  };

  const res = http.post('http://localhost:8082/v1/event/create', payload, { headers, timeout: '10s' });

  check(res, {
    'is status 200 or 201': (r) => r.status === 200 || r.status === 201,
    'body is not empty': (r) => r.body && r.body.length > 0
  });
}
