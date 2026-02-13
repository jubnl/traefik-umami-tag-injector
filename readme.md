# Traefik Umami Tag Injector

A Traefik middleware plugin that injects an [Umami](https://umami.is) analytics `<script>` tag into eligible HTML
responses.

The plugin is designed to be:

- **Streaming-safe** – does not buffer entire responses.
- **Memory-efficient** – only inspects the first part of the response.
- **Per-site configurable** – the Umami `websiteId` can be set directly via Traefik labels.
- **Non-intrusive** – skips non-HTML, compressed, websocket, and non-GET traffic.

---

## Features

- Injects a `<script defer ...>` tag into HTML responses.
- Streaming look-ahead injection (no full buffering).
- Per-router configuration via labels (`websiteId`).
- Fallback to header-based website ID if needed.
- Case-insensitive `</head>` detection.
- Optional fallback to `</body>` injection.
- Safe passthrough for:
    - Non-GET requests
    - WebSocket / Upgrade requests
    - Non-HTML responses
    - Compressed responses
    - Responses where the script already exists
- Automatically removes `Content-Length` and `ETag` if injection occurs.

---

## How It Works

The middleware wraps the upstream response writer and:

1. Only processes **HTTP GET** requests.
2. Skips WebSocket / Upgrade traffic.
3. Determines the `websiteId`:
    - First from middleware config (`websiteId`)
    - Then from request header (`websiteIdHeader`)
4. Streams the response and buffers only the first `maxLookaheadBytes`.
5. Searches for `</head>` (case-insensitive).
6. Injects the Umami script before `</head>` if found.
7. Optionally falls back to `</body>` if enabled.
8. If neither is found within the lookahead window, the response is passed through unchanged.

---

## Default Script Injected

```html

<script defer src="https://analytics.jubnl.ch/script.js" data-website-id="YOUR_ID"></script>
```

## Configuration

### Plugin configuration Fields

| Field                | Type   | Default                                | Description                                                  |
|----------------------|--------|----------------------------------------|--------------------------------------------------------------|
| `scriptSrc`          | string | `https://analytics.jubnl.ch/script.js` | URL of the analytics script.                                 |
| `websiteId`          | string | `""`                                   | Umami website ID. If empty, header fallback is used.         |
| `websiteIdHeader`    | string | `X-Analytics-Website-Id`               | Header name used when `websiteId` is not set.                |
| `maxLookaheadBytes`  | int    | `131072` (128 KiB)                     | Maximum bytes to buffer while searching for injection point. |
| `injectBefore`       | string | `</head>`                              | HTML tag to inject before. Case-insensitive.                 |
| `alsoMatchBodyClose` | bool   | `true`                                 | If `</head>` is not found, try `</body>`.                    |

## Installation

### Static Traefik Configuration

```yaml
experimental:
  localPlugins:
    analyticsinject:
      moduleName: github.com/jubnl/traefik-umami-tag-injector
```

### Usage with traefik labels

Set the `websiteId` directly via labels, no dynamic file edits required.

```yaml
labels:
  - "traefik.enable=true"
  - "traefik.http.routers.myapp.rule=Host(`example.com`)"
  - "traefik.http.routers.myapp.entrypoints=websecure"

  - "traefik.http.middlewares.myapp-umami.plugin.analyticsinject.websiteId=YOUR_UMAMI_ID"

  - "traefik.http.routers.myapp.middlewares=myapp-umami"
```

Only the `websiteId` label needs to change per site.

### Optional: Header Fallback Mode

If you prefer setting the ID via header instead of middleware config:

```yaml
- "traefik.http.middlewares.myapp-umami.headers.customrequestheaders.X-Analytics-Website-Id=YOUR_ID"
```

## Behavior Summary

| Scenario                                | Result                               |
|-----------------------------------------|--------------------------------------|
| Non-GET request                         | Passthrough                          |
| WebSocket / Upgrade                     | Passthrough                          |
| Non-HTML response                       | Passthrough                          |
| Compressed response                     | Passthrough                          |
| Script already present                  | Passthrough                          |
| `</head>` found                         | Inject before it                     |
| `</head>` not found but `</body>` found | Inject before `</body>` (if enabled) |
| No injection point found                | Passthrough                          |
| Large responses                         | Safe streaming, no truncation        |

## Performance Notes

- No full response buffering.
- Memory usage bounded by maxLookaheadBytes.
- Designed for high-traffic environments.

## Security Considerations

- Does not modify CSP headers automatically.
- If your site uses strict CSP, you must allow the script domain manually.
- Only modifies HTML content types.

## Development

Run tests:

```shell
go test -v ./...
```

## License

MIT

## Suggestions / Feedback

Please open an issue or PR.