import http from 'k6/http';
import ws from 'k6/ws';
import { check } from 'k6';

export const options = {
  scenarios: {
    // 1. High throughput read user (target: ~2.5M - 3M reqs)
    users_rest: {
      executor: 'constant-vus',
      vus: 120,
      duration: '45s',
      exec: 'testUserRest',
    },
    // 2. High throughput root info & health & metrics (target: ~1.5M - 2M reqs)
    system_endpoints: {
      executor: 'constant-vus',
      vus: 80,
      duration: '45s',
      exec: 'testSystemEndpoints',
    },
    // 3. Avatar redirect quicklink (target: ~500k reqs)
    avatar_quicklinks: {
      executor: 'constant-vus',
      vus: 40,
      duration: '45s',
      exec: 'testAvatarQuicklink',
    },
    // 4. WebSocket realtime streaming & subscribe (parallel load)
    websocket_realtime: {
      executor: 'constant-vus',
      vus: 60,
      duration: '45s',
      exec: 'testWebSocket',
    },
  },
  thresholds: {
    http_req_failed: ['rate<0.01'],
  },
};

const BASE_URL = 'http://127.0.0.1:4001';
const WS_URL = 'ws://127.0.0.1:4001/socket';
const USER_ID = '685716988471148552';

export function testUserRest() {
  const res = http.get(`${BASE_URL}/v1/users/${USER_ID}`);
  check(res, {
    'user status 200': (r) => r.status === 200,
  });
}

export function testSystemEndpoints() {
  // Batch request across multiple system endpoints simultaneously
  const responses = http.batch([
    ['GET', `${BASE_URL}/`, null, { tags: { name: 'root' } }],
    ['GET', `${BASE_URL}/health`, null, { tags: { name: 'health' } }],
    ['GET', `${BASE_URL}/metrics`, null, { tags: { name: 'metrics' } }],
  ]);

  check(responses[0], { 'root 200': (r) => r.status === 200 });
  check(responses[1], { 'health 200': (r) => r.status === 200 });
  check(responses[2], { 'metrics 200': (r) => r.status === 200 });
}

export function testAvatarQuicklink() {
  const res = http.get(`${BASE_URL}/${USER_ID}.png`, {
    redirects: 0,
  });
  check(res, {
    'avatar status 200 or 307': (r) => r.status === 200 || r.status === 307,
  });
}

export function testWebSocket() {
  const url = WS_URL;
  const res = ws.connect(url, null, function (socket) {
    let gotHello = false;

    socket.on('message', function (data) {
      try {
        const msg = JSON.parse(data);
        if (msg.op === 1) { // HELLO
          gotHello = true;
          // Subscribe to user
          socket.send(JSON.stringify({
            op: 2,
            d: { subscribe_to_id: USER_ID },
          }));
        }
      } catch (e) {}
    });

    socket.setTimeout(function () {
      socket.close();
    }, 1000); // cycle socket every 1s
  });

  check(res, { 'ws connected successfully': (r) => r && r.status === 101 });
}
