# WebSocket API endpoints

The API services expose optional WebSocket streams using the same v1 route conventions as the REST APIs. Enable them in configuration before exposing them to browsers.

## Paths

- Search/API service: `GET /v1/ws`
- Events service: `GET /v1/event/ws`

Both endpoints send JSON messages:

```json
{
  "type": "event.created",
  "service": "events",
  "timestamp": "2026-06-21T00:00:00Z",
  "payload": {}
}
```

The `payload` reuses the same response and event structures used by the existing REST APIs. Updates are published only after the related operation succeeds or, for database notifications, after the service receives the persisted event notification.

## Configuration

```yaml
api:
  websocket:
    enabled: true
    allowed_origins: ["https://console.example.com"]
    heartbeat_interval: 30
    write_queue_size: 64
    write_timeout: 5

events:
  websocket:
    enabled: true
    allowed_origins: ["https://console.example.com"]
    heartbeat_interval: 30
    write_queue_size: 64
    write_timeout: 5
```

Use explicit `allowed_origins` in production. `"*"` allows any browser origin.

## Minimal browser example

```html
<script>
  const api = new WebSocket('ws://localhost:8080/v1/ws');
  api.onmessage = (event) => console.log('API update', JSON.parse(event.data));

  const events = new WebSocket('ws://localhost:8082/v1/event/ws');
  events.onmessage = (event) => console.log('Event update', JSON.parse(event.data));
</script>
```
