import http from 'k6/http';
import { sleep, check } from 'k6';

const test_target = 1000;

export let options = {
  stages: [
    { duration: '10s', target: test_target / 10 },  // Ramp-up to 100 VUs over 10 seconds
    { duration: '30s', target: test_target }, // Then sustain test_target VUs for 30 seconds
    { duration: '10s', target: 0 },    // Ramp-down to 0 VUs
  ],
  rps: test_target,  // Force test_target requests per second
};

export default function () {
  let res = http.get('http://localhost:8080/v1/get_all_source_status', { timeout: '10s' });
  check(res, {
    'is status 200': (r) => r.status === 200,
  });
}
