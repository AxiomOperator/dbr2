// SPDX-License-Identifier: Apache-2.0
//
// Server-Sent Events part of the mock API (GET /api/v1/events), imported by
// scripts/mock-api.mjs and the other mock modules. Mirrors the backend
// (internal/api/ops_events.go): a ": connected" comment with `retry: 5000`,
// comment heartbeats (every 15 s; MOCK_SSE_HEARTBEAT_MS overrides), one
// `event: <type>` + `data: <json>` message per event, delivered only to
// callers holding the event's permission, and `?types=a,b` filtering.
// Missed events are never replayed. No imports of the other mock modules.

const HEARTBEAT_MS = Number(process.env.MOCK_SSE_HEARTBEAT_MS ?? 15_000);

/** @type {Set<{res: import("node:http").ServerResponse, user: object, types: Set<string>}>} */
const clients = new Set();
let seq = 0;

/**
 * Publishes one event to every connected client allowed to see it.
 * @param {string} type e.g. "job.progress"
 * @param {string} permission e.g. "backup.read" ("" = every signed-in user)
 * @param {object} data the event payload (see api/openapi.yaml, *Event schemas)
 */
export function publish(type, permission, data) {
  const id = ++seq;
  const frame = `id: ${id}\nevent: ${type}\ndata: ${JSON.stringify(data)}\n\n`;
  for (const c of clients) {
    if (c.types.size > 0 && !c.types.has(type)) continue;
    if (permission && !c.user.permissions.includes(permission)) continue;
    c.res.write(frame);
  }
}

/** Number of open streams (for the mock's log). */
export const connectedClients = () => clients.size;

/** Routes for mock-api.mjs: [method, path regex, handler(req, res, url, user)]. */
export function eventRoutes() {
  return [
    [
      "GET",
      /^\/api\/v1\/events$/,
      (req, res, url, user) => {
        const types = new Set(
          (url.searchParams.get("types") ?? "")
            .split(",")
            .map((t) => t.trim())
            .filter(Boolean),
        );
        res.writeHead(200, {
          "content-type": "text/event-stream",
          "cache-control": "no-cache",
          connection: "keep-alive",
          "x-accel-buffering": "no",
        });
        res.write(": connected\nretry: 5000\n\n");
        const client = { res, user, types };
        clients.add(client);
        const beat = setInterval(() => res.write(": heartbeat\n\n"), HEARTBEAT_MS);
        const close = () => {
          clearInterval(beat);
          clients.delete(client);
        };
        req.on("close", close);
        res.on("close", close);
      },
    ],
  ];
}
