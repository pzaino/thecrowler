import http from 'k6/http';
import { sleep, check } from 'k6';

export let options = {
  vus: 1000, // Number of virtual users
  duration: '30s', // Duration of the test
};

export default function () {
  let res = http.get('http://localhost:8080/v1/get_all_source_status');
  check(res, {
    'is status 200': (r) => r.status === 200,
  });
  sleep(1);
}
