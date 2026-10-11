import http from 'k6/http';
import ws from 'k6/ws';
import { check } from 'k6';

// Target: 5,000,000 total requests across all endpoints
// Running 250 concurrent VUs distributed evenly across routes
export const options = {
  scenarios: {
    // 1. User presence JSON (~2,500,000 requests)
    rest_users: {
      executor: 'constant-vus',
      vus: 120,
      duration: '110s',
      exec: 'testUser',
    },
    // 2. Health & Metrics (~1,500,000 requests)
    system_health_metrics: {
      executor: 'constant-vus',
      vus: 80,
      duration: '110s',
      exec: 'testSystem',
    },
    // 3. Root info (~1,000,000 requests)
    system_root: {
      executor: 'constant-vus',
      vus: 50,
      duration: '110s',
      exec: 'testRoot',
    },
    // 4. Realtime WebSockets streaming alongside REST
    realtime_sockets: {
      executor: 'ramping-vus',
      startVUs: 0,
      stages: [
        { duration: '10s', target: 50 },
        { duration: '90s', target: 50 },
        { duration: '10s', target: 0 },
      ],
      exec: 'testWS',
    },
  },
  thresholds: {
    http_req_failed: ['rate<0.001'], // 99.9% success required
    http_req_duration: ['p(95)<30'], // p95 under 30ms under ultra high volume
  },
};

const BASE_URL = 'http://127.0.0.1:4001';
const WS_URL = 'ws://127.0.0.1:4001/socket';
const USER_ID = '685716988471148552';

export function testUser() {
  const res = http.get(`${BASE_URL}/v1/users/${USER_ID}`);
  check(res, {
    'user status 200': (r) => r.status === 200,
  });
}

export function testSystem() {
  const res = http.get(`${BASE_URL}/health`);
  check(res, {
    'health 200': (r) => r.status === 200,
  });
  const mRes = http.get(`${BASE_URL}/metrics`);
  check(mRes, {
    'metrics 200': (m) => m.status === 200,
  });
}

export function testRoot() {
  const res = http.get(`${BASE_URL}/`);
  check(res, {
    'root 200': (r) => r.status === 200,
  });
}

export function testWS() {
  const res = ws.connect(WS_URL, null, function (socket) {
    socket.on('message', function (data) {
      try {
        const msg = JSON.parse(data);
        if (msg.op === 1) { // HELLO
          socket.send(JSON.stringify({
            op: 2,
            d: { subscribe_to_id: USER_ID },
          }));
        }
      } catch (e) {}
    });

    socket.setTimeout(function () {
      socket.close();
    }, 5000);
  });

  check(res, { 'ws connected': (r) => r && r.status === 101 });
}
