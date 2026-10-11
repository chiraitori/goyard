import http from 'k6/http';
import { check, sleep } from 'k6';

export const options = {
  stages: [
    { duration: '3s', target: 20 },   // ramp up to 20 virtual users
    { duration: '10s', target: 50 },  // sustain 50 concurrent users
    { duration: '3s', target: 0 },    // ramp down
  ],
  thresholds: {
    http_req_failed: ['rate<0.01'],   // error rate < 1%
    http_req_duration: ['p(95)<50'],  // 95% requests < 50ms
  },
};

export default function () {
  const res = http.get('http://127.0.0.1:4001/v1/users/685716988471148552');
  check(res, {
    'status is 200': (r) => r.status === 200,
    'has valid success': (r) => r.json().success === true,
    'username is chiraitori': (r) => r.json().data.discord_user.username === 'chiraitori',
  });
}
