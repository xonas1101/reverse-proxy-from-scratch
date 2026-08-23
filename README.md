# Go Reverse Proxy

A reverse proxy built from scratch in Go using only the standard library.

This project started as a single HTTP request forwarder and was incrementally extended into a production-style reverse proxy with persistent connections, round-robin load balancing, chunked transfer support, TLS termination, WebSocket passthrough, structured logging, and graceful shutdown.

> **Goal:** Understand how reverse proxies like NGINX, HAProxy, and Caddy work internally by implementing the networking primitives yourself.

## Features

- Single request → response forwarding
- HTTP/1.1 keep-alive connections
- Idle connection timeout
- Round-robin load balancing across multiple backends
- Chunked transfer encoding support
- TLS termination using self-signed certificates
- WebSocket upgrade & raw TCP passthrough
- `X-Forwarded-For` and `X-Forwarded-Proto` headers
- Dial timeouts for backend connections
- Graceful shutdown with in-flight connection draining
- Request logging with latency and status codes

## Architecture

<svg viewBox="0 0 340 220" xmlns="http://www.w3.org/2000/svg">
  <defs>
    <marker id="arrow" markerWidth="10" markerHeight="10" refX="9" refY="3" orient="auto">
      <path d="M0,0 L10,3 L0,6 Z" fill="#6B7280"/>
    </marker>
  </defs>
  <rect x="110" y="10" width="120" height="34" rx="8" fill="#E0F2FE" stroke="#0369A1"/>
  <text x="170" y="31" fontSize="10" fontFamily="Arial, Helvetica, sans-serif" textAnchor="middle" fontWeight="bold" fill="#075985">
    Client
  </text>
  <line x1="170" y1="44" x2="170" y2="58" stroke="#6B7280" strokeWidth="1.5" markerEnd="url(#arrow)"/>
  <rect x="70" y="58" width="200" height="52" rx="10" fill="#F3F4F6" stroke="#9CA3AF"/>
  <text x="170" y="76" fontSize="11" fontFamily="Arial, Helvetica, sans-serif" textAnchor="middle" fontWeight="bold" fill="#111827">
    Go Reverse Proxy
  </text>
  <text x="170" y="89" fontSize="8" fontFamily="Arial, Helvetica, sans-serif" textAnchor="middle" fill="#374151">
    Keep-Alive • TLS • Logging
  </text>
  <text x="170" y="99" fontSize="8" fontFamily="Arial, Helvetica, sans-serif" textAnchor="middle" fill="#374151">
    WebSocket • X-Forwarded-*
  </text>
  <line x1="170" y1="110" x2="170" y2="124" stroke="#6B7280" strokeWidth="1.5" markerEnd="url(#arrow)"/>
  <text x="170" y="136" fontSize="9" fontFamily="Arial, Helvetica, sans-serif" textAnchor="middle" fill="#374151">
    Round Robin
  </text>
  <line x1="170" y1="140" x2="60" y2="168" stroke="#9CA3AF" strokeWidth="1.5" markerEnd="url(#arrow)"/>
  <line x1="170" y1="140" x2="170" y2="168" stroke="#9CA3AF" strokeWidth="1.5" markerEnd="url(#arrow)"/>
  <line x1="170" y1="140" x2="280" y2="168" stroke="#9CA3AF" strokeWidth="1.5" markerEnd="url(#arrow)"/>
  <rect x="20" y="168" width="80" height="34" rx="8" fill="#D1FAE5" stroke="#059669"/>
  <text x="60" y="183" fontSize="9" fontFamily="Arial, Helvetica, sans-serif" textAnchor="middle" fontWeight="bold" fill="#065F46">
    :9090
  </text>
  <text x="60" y="193" fontSize="8" fontFamily="Arial, Helvetica, sans-serif" textAnchor="middle" fill="#047857">
    Backend 1
  </text>
  <rect x="130" y="168" width="80" height="34" rx="8" fill="#D1FAE5" stroke="#059669"/>
  <text x="170" y="183" fontSize="9" fontFamily="Arial, Helvetica, sans-serif" textAnchor="middle" fontWeight="bold" fill="#065F46">
    :9091
  </text>
  <text x="170" y="193" fontSize="8" fontFamily="Arial, Helvetica, sans-serif" textAnchor="middle" fill="#047857">
    Backend 2
  </text>
  <rect x="240" y="168" width="80" height="34" rx="8" fill="#D1FAE5" stroke="#059669"/>
  <text x="280" y="183" fontSize="9" fontFamily="Arial, Helvetica, sans-serif" textAnchor="middle" fontWeight="bold" fill="#065F46">
    :9092
  </text>
  <text x="280" y="193" fontSize="8" fontFamily="Arial, Helvetica, sans-serif" textAnchor="middle" fill="#047857">
    Backend 3
  </text>
</svg>

## Project Structure

```text
.
├── main.go          # Reverse proxy implementation
├── cert.pem         # TLS certificate
├── key.pem          # TLS private key
└── README.md
```

## Running

### 1. Start three backend servers

For testing, run three simple HTTP servers:

```bash
python3 -m http.server 9090
python3 -m http.server 9091
python3 -m http.server 9092
```

Or use any HTTP applications listening on those ports.

### 2. Run the proxy

HTTP:

```bash
go run main.go
```

The proxy listens on:

```text
localhost:8080
```

TLS:

```bash
go run main.go -tls
```

The proxy listens on:

```text
localhost:8443
```

## Load Balancing

Each client connection cycles through the three backends:

| Request | Backend |
|---------:|---------|
| 1 | `:9090` |
| 2 | `:9091` |
| 3 | `:9092` |
| 4 | `:9090` |
| ... | repeats |

The round-robin happens **per keep-alive connection**, not globally.

## Implemented Phases

| Phase | Description |
|------|-------------|
| 1 | Single request/response proxy |
| 2 | Keep-alive + idle timeout |
| 3 | Round-robin backend selection |
| 4 | Chunked transfer encoding |
| 5 | TLS termination |
| 6 | WebSocket upgrade passthrough |
| 7 | Logging, graceful shutdown, dial timeouts |

## Example Log

```text
proxy listening on :8080

[127.0.0.1] GET / → localhost:9090 [200] 3.2ms
[127.0.0.1] GET /about → localhost:9091 [200] 2.8ms
[127.0.0.1] GET /ws → localhost:9092 (upgrade)
```

Each log contains:

- client IP
- HTTP method
- requested path
- selected backend
- response status
- request latency

## Technical Highlights

### Keep-Alive

A single TCP connection can serve multiple HTTP requests before closing.

### Forwarded Headers

The proxy injects:

```http
X-Forwarded-For: <client-ip>
X-Forwarded-Proto: http|https
```

This preserves the original client identity for backend services.

### Chunked Responses

Instead of buffering entire responses, chunked bodies are streamed directly between backend and client while preserving HTTP framing.

### WebSockets

After detecting:

```http
Connection: Upgrade
Upgrade: websocket
```

HTTP parsing stops completely and the proxy switches to raw bidirectional TCP forwarding using `io.Copy`.

### Graceful Shutdown

On `Ctrl+C` or `SIGTERM`:

1. Stop accepting new connections.
2. Keep existing connections alive.
3. Wait for all goroutines to finish.
4. Exit cleanly.

No in-flight requests are dropped.

## Concepts Practiced

- TCP sockets
- HTTP/1.1 protocol parsing
- Buffered I/O (`bufio`)
- Connection persistence
- Load balancing algorithms
- TLS with `crypto/tls`
- WebSocket upgrade semantics
- Concurrent networking with goroutines
- Graceful shutdown using `context` and `sync.WaitGroup`
- Deadline and timeout management

## Future Improvements

- Global round-robin using atomic counters
- Health checks for unhealthy backends
- Least-connections load balancing
- HTTP/2 support
- Configuration file (YAML/JSON)
- Metrics endpoint (Prometheus)
- Request retries and circuit breaker
- Access log middleware