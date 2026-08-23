// Phase 1 — Single request/response, no persistence

//     listen on :8080, dial backend fresh per connection (or reuse, your call)
//     parse request line + headers
//     strip Connection header
//     inject X-Forwarded-For with client IP
//     forward request to backend
//     read response headers, parse Content-Length, read exact body bytes
//     write response back to client
//     close both connections after one exchange
//     test with curl against /hello, /health, /echo

// Phase 2 — Connection reuse / keep-alive

//     support Connection: keep-alive from client — don't close after one request
//     loop: read request → forward → respond → read next request on same conn
//     handle Connection: close to end the loop
//     separate connection per client (currently you share one cServer dial across all goroutines — fix this: each client needs its own backend dial, not one shared connServer)
//     add idle timeout so dead connections don't hang forever

// Phase 3 — Multiple backends / load balancing

//     config: list of upstream addresses instead of one hardcoded localhost:9090
//     round-robin or random selection per request
//     basic health check (skip a backend if dial fails)
//     path-based routing if needed (e.g. /api/* → backend A, /static/* → backend B)

// Phase 4 — Chunked transfer encoding

//     detect Transfer-Encoding: chunked in response (instead of Content-Length)
//     parse chunk size line, read chunk, repeat until 0\r\n\r\n terminator
//     forward chunks to client as they arrive (streaming, don't buffer whole body)
//     needed for your /stream route to actually work through the proxy

// Phase 5 — TLS termination

//     listen with tls.Listen on :443 using a cert/key
//     decrypt incoming HTTPS, forward as plain HTTP to backend (or re-encrypt if backend needs TLS)
//     redirect HTTP :80 → HTTPS :443 optionally
//     add X-Forwarded-Proto: https header

// Phase 6 — WebSocket / Upgrade passthrough

//     detect Upgrade: websocket header
//     after initial handshake response, switch to raw bidirectional byte copying (no more HTTP parsing)
//     both directions need concurrent io.Copy (goroutine each way) since it's now full-duplex

// Phase 7 — Observability & hardening

//     structured logging (method, path, status, latency, backend used)
//     timeouts on read/write/dial so a slow backend can't hang the proxy
//     graceful shutdown (SIGINT/SIGTERM → stop accepting, drain in-flight requests)
//     basic metrics (request count, error count, per-backend latency)
//     config file instead of hardcoded addresses

package main

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"strconv"
	"strings"
)

func main() {
	listener, err := net.Listen("tcp", ":8080")
	if err != nil {
		log.Fatalf("Internal Server Error: %v\n", err)
	}

	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Printf("Error connecting to server: %v\n", err)
			continue
		}
		go handleConnection(conn)
	}
}

func handleConnection(c net.Conn) {
	defer c.Close()

	backend, err := net.Dial("tcp", "localhost:9090")
	if err != nil {
		log.Printf("failed to dial backend: %v", err)
		return
	}
	defer backend.Close()

	reader := bufio.NewReader(c)
	stdout := bufio.NewWriter(os.Stdout)
	writer := bufio.NewWriter(backend)
	host, _, _ := net.SplitHostPort(c.RemoteAddr().String())

	shouldClose := false

	for {
		for lineNumber := 1; ; lineNumber++ {
			if lineNumber == 2 {
				fmt.Fprintf(writer, "X-Forwarded-For: %s\r\n", host)
				writer.Flush()
			}
			line, err := reader.ReadString('\n')
			if err != nil {
				return // client gone, stop handling this connection
			}
			stdout.WriteString(line)
			stdout.Flush()
			handleServer(line, backend, &shouldClose)

			if line == "\r\n" || line == "\n" {
				break
			}
		}

		forwardResponse(backend, c)

		if shouldClose {
			return
		}
	}
}

func handleServer(line string, c net.Conn, shouldClose *bool) {
	writer := bufio.NewWriter(c)
	stdout := bufio.NewWriter(os.Stdout)
	parts := strings.Split(line, ":")

	switch parts[0] {
	case "Host":
		line = fmt.Sprintf("Host: %s\r\n", c.RemoteAddr().String())
	case "Connection":
		if len(parts) > 1 && strings.TrimSpace(parts[1]) == "close" {
			*shouldClose = true
		}
		line = ""
	}
	writer.WriteString(line)
	writer.Flush()
	stdout.WriteString(line)
	stdout.Flush()
}

func forwardResponse(cServer net.Conn, c net.Conn) {
	reader := bufio.NewReader(cServer)
	writer := bufio.NewWriter(c)
	contentLength := 0

	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			break
		}
		writer.WriteString(line)
		writer.Flush()

		trimmed := strings.TrimRight(line, "\r\n")
		parts := strings.SplitN(trimmed, ":", 2)
		if len(parts) == 2 && strings.EqualFold(strings.TrimSpace(parts[0]), "Content-Length") {
			n, err := strconv.Atoi(strings.TrimSpace(parts[1]))
			if err == nil {
				contentLength = n
			}
		}

		if line == "\r\n" || line == "\n" {
			break
		}
	}

	if contentLength > 0 {
		body := make([]byte, contentLength)
		if _, err := io.ReadFull(reader, body); err != nil {
			return
		}
		writer.Write(body)
		writer.Flush()
	}
}
