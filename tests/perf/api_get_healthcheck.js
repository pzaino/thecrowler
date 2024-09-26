import http from 'k6/http';
import { sleep, check } from 'k6';

const test_target = 1000;

export let options = {
  stages: [
    { duration: '10s', target: 100 },  // Ramp-up to 100 VUs over 10 seconds
    { duration: '30s', target: test_target }, // Then sustain test_target VUs for 30 seconds
    { duration: '10s', target: 0 },    // Ramp-down to 0 VUs
  ],
  rps: test_target,  // Force test_target requests per second
};

/*
 * This test script sends a GET request to the health check endpoint
 * and checks if the response status code is 200.
 * This perf test allows to measure performance of the API without
 * having the DB queries in the way.
 */
export default function () {
  let res = http.get('http://localhost:8080/v1/health');
  check(res, {
    'is status 200': (r) => r.status === 200,
  });
}
