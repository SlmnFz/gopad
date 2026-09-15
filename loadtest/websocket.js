import http from "k6/http";
import { check, sleep } from "k6";
import ws from "k6/ws";
import { Rate, Trend } from "k6/metrics";

const baseURL = __ENV.GOPAD_BASE_URL || "http://127.0.0.1:8080";
const websocketURL = baseURL.replace(/^http/, "ws");
const virtualUsers = Number(__ENV.GOPAD_VUS || 5);
const duration = __ENV.GOPAD_DURATION || "30s";
const latency = new Trend("gopad_round_trip_latency", true);
const received = new Rate("gopad_edits_received");

export const options = {
  vus: virtualUsers,
  duration,
  thresholds: {
    gopad_round_trip_latency: ["p(95)<500"],
    gopad_edits_received: ["rate>0.95"],
  },
};

export function setup() {
  const response = http.post(`${baseURL}/documents`);
  check(response, { "document created": (result) => result.status === 201 });
  if (response.status !== 201) {
    throw new Error(`document creation failed with status ${response.status}`);
  }
  return JSON.parse(response.body);
}

export default function (document) {
  const siteID = `k6-${__VU}`;
  let counter = 0;

  const result = ws.connect(`${websocketURL}/ws/${document.slug}`, {}, (socket) => {
    socket.on("open", () => {
      socket.send(JSON.stringify({ type: "hello", payload: { username: `k6-${__VU}` } }));
      socket.setInterval(() => {
        counter += 1;
        const sentAt = Date.now();
        socket.send(JSON.stringify({
          type: "op",
          payload: {
            type: "insert",
            id: { siteID, counter },
            value: 65 + (__VU % 26),
            clientSentAt: sentAt,
          },
        }));
      }, 800 + Math.floor(Math.random() * 1000));
    });

    socket.on("message", (message) => {
      const envelope = JSON.parse(message);
      if (envelope.type !== "op") {
        return;
      }
      const operation = envelope.payload;
      if (!operation.clientSentAt) {
        return;
      }
      latency.add(Date.now() - operation.clientSentAt);
      received.add(true);
    });

    socket.on("error", () => received.add(false));
    sleep(Number(__ENV.GOPAD_SOCKET_SECONDS || 25));
  });

  check(result, { "WebSocket connected": (response) => response && response.status === 101 });
}
