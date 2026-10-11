import http from 'k6/http';
import ws from 'k6/ws';
import { check } from 'k6';

export const options = {
  scenarios: {
    // 1. High-throughput REST read requests (pushing towards 1M requests)
    rest_users: {
      executor: 'constant-vus',
      vus: 100,
      duration: '30s',
      exec: 'restTest',
    },
    // 2. Real-time WebSocket clients like Lanyard frontend hooks
    realtime_sockets: {
      executor: 'ramping-vus',
      startVUs: 0,
      stages: [
        { duration: '5s', target: 50 },
        { duration: '20s', target: 50 },
        { duration: '5s', target: 0 },
      ],
      exec: 'wsTest',
    },
  },
  thresholds: {
    http_req_failed: ['rate<0.01'],
    http_req_duration: ['p(95)<50'],
    checks: ['rate>0.99'],
  },
};

// REST test reading user
export function restTest() {
  const res = http.get('http://127.0.0.1:4001/v1/users/685716988471148552');
  check(res, {
    'status is 200': (r) => r.status === 200,
    'has valid json': (r) => r.json().success === true,
  });
}

// Real-time WebSocket test (exact Lanyard protocol)
export function wsTest() {
  const url = 'ws://127.0.0.1:4001/socket';

  const res = ws.connect(url, null, function (socket) {
    socket.on('open', function () {
      // Send op 2: Initialize subscription to user
      socket.send(JSON.stringify({
        op: 2,
        d: { subscribe_to_id: '685716988471148552' }
      }));
    });

    socket.on('message', function (data) {
      const msg = JSON.parse(data);
      check(msg, {
        'valid opcode': (m) => m.op === 0 || m.op === 1,
      });

      // Heartbeat periodically (op 3)
      if (msg.op === 1) {
        socket.send(JSON.stringify({ op: 3 }));
      }
    });

    socket.setTimeout(function () {
      socket.close();
    }, 4000);
  });

  check(res, { 'ws connected': (r) => r && r.status === 101 });
}
