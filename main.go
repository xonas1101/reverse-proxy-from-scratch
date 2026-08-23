// Phase 1 — Single request/response
// Phase 2 — Keep-alive / idle timeout
// Phase 3 — Round robin across backends (per-connection cycling)
// Phase 4 — Chunked transfer encoding
// Phase 5 — TLS termination
// Phase 6 — WebSocket / Upgrade passthrough
// Phase 7 — Logging, graceful shutdown, dial timeouts

package main

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

var backendAddrs = []string{
	"localhost:9090",
	"localhost:9091",
	"localhost:9092",
}

var (
	activeConns  sync.WaitGroup
	shuttingDown = false
	shutdownMu   sync.Mutex
)

func isShuttingDown() bool {
	shutdownMu.Lock()
	defer shutdownMu.Unlock()
	return shuttingDown
}

func main() {
	useTLS := len(os.Args) > 1 && os.Args[1] == "-tls"

	var listener net.Listener
	var err error

	if useTLS {
		cert, cerr := tls.LoadX509KeyPair("cert.pem", "key.pem")
		if cerr != nil {
			log.Fatalf("failed to load TLS cert/key: %v", cerr)
		}
		tlsCfg := &tls.Config{Certificates: []tls.Certificate{cert}}
		listener, err = tls.Listen("tcp", ":8443", tlsCfg)
		if err != nil {
			log.Fatalf("failed to listen (tls): %v\n", err)
		}
		log.Println("proxy listening on :8443 (TLS)")
	} else {
		listener, err = net.Listen("tcp", ":8080")
		if err != nil {
			log.Fatalf("failed to listen: %v\n", err)
		}
		log.Println("proxy listening on :8080")
	}

	// graceful shutdown
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		<-ctx.Done()
		log.Println("shutdown signal received, closing listener...")
		shutdownMu.Lock()
		shuttingDown = true
		shutdownMu.Unlock()
		listener.Close()
	}()

	for {
		conn, err := listener.Accept()
		if err != nil {
			if isShuttingDown() {
				break
			}
			log.Printf("accept error: %v\n", err)
			continue
		}
		activeConns.Add(1)
		go func() {
			defer activeConns.Done()
			handleConnection(conn, useTLS)
		}()
	}

	log.Println("draining in-flight connections...")
	activeConns.Wait()
	log.Println("shutdown complete")
}

func connectBackends() (*bufio.Writer, *bufio.Writer, *bufio.Writer, net.Conn, net.Conn, net.Conn) {
	dial := func(addr string) net.Conn {
		conn, err := net.DialTimeout("tcp", addr, 3*time.Second)
		if err != nil {
			log.Printf("failed to dial %s: %v", addr, err)
			return nil
		}
		return conn
	}

	b1 := dial(backendAddrs[0])
	b2 := dial(backendAddrs[1])
	b3 := dial(backendAddrs[2])

	var w1, w2, w3 *bufio.Writer
	if b1 != nil {
		w1 = bufio.NewWriter(b1)
	}
	if b2 != nil {
		w2 = bufio.NewWriter(b2)
	}
	if b3 != nil {
		w3 = bufio.NewWriter(b3)
	}
	return w1, w2, w3, b1, b2, b3
}

func handleConnection(c net.Conn, useTLS bool) {
	start := time.Now()
	defer c.Close()

	w1, w2, w3, b1, b2, b3 := connectBackends()
	if b1 != nil {
		defer b1.Close()
	}
	if b2 != nil {
		defer b2.Close()
	}
	if b3 != nil {
		defer b3.Close()
	}

	reader := bufio.NewReader(c)
	stdout := bufio.NewWriter(os.Stdout)
	host, _, _ := net.SplitHostPort(c.RemoteAddr().String())
	shouldClose := false
	isUpgrade := false

	for i := 1; ; i++ {
		var writer *bufio.Writer
		var backend net.Conn
		var backendAddr string

		switch i % 3 {
		case 1:
			writer, backend, backendAddr = w1, b1, backendAddrs[0]
		case 2:
			writer, backend, backendAddr = w2, b2, backendAddrs[1]
		case 0:
			writer, backend, backendAddr = w3, b3, backendAddrs[2]
		}

		if backend == nil {
			log.Printf("selected backend unavailable, stopping conn from %s", host)
			return
		}

		var method, path string

		for lineNumber := 1; ; lineNumber++ {
			if lineNumber == 2 {
				fmt.Fprintf(writer, "X-Forwarded-For: %s\r\n", host)
				if useTLS {
					fmt.Fprintf(writer, "X-Forwarded-Proto: https\r\n")
				}
				writer.Flush()
			}
			c.SetReadDeadline(time.Now().Add(10 * time.Second))
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			stdout.WriteString(line)
			stdout.Flush()

			if lineNumber == 1 {
				parts := strings.Fields(line)
				if len(parts) >= 2 {
					method, path = parts[0], parts[1]
				}
			}

			upgraded := handleServer(line, backend, &shouldClose)
			if upgraded {
				isUpgrade = true
			}

			if line == "\r\n" || line == "\n" {
				break
			}
		}

		if isUpgrade {
			log.Printf("[%s] %s %s → %s (upgrade, switching to raw passthrough)", host, method, path, backendAddr)
			pipeRaw(c, backend)
			return
		}

		status := forwardResponse(backend, c)
		log.Printf("[%s] %s %s → %s [%d] %v", host, method, path, backendAddr, status, time.Since(start))

		if shouldClose {
			return
		}
	}
}

// pipeRaw does bidirectional raw byte copying, used after a websocket
// upgrade handshake — no more HTTP parsing from this point on.
func pipeRaw(client net.Conn, backend net.Conn) {
	client.SetReadDeadline(time.Time{}) // clear timeout, connection is now long-lived
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		io.Copy(backend, client)
	}()
	go func() {
		defer wg.Done()
		io.Copy(client, backend)
	}()
	wg.Wait()
}

// handleServer mutates a single header line before forwarding it to the
// backend. Returns true if this line signals a websocket upgrade.
func handleServer(line string, c net.Conn, shouldClose *bool) bool {
	writer := bufio.NewWriter(c)
	stdout := bufio.NewWriter(os.Stdout)
	parts := strings.SplitN(line, ":", 2)
	upgrade := false

	switch strings.TrimSpace(parts[0]) {
	case "Host":
		line = fmt.Sprintf("Host: %s\r\n", c.RemoteAddr().String())
	case "Connection":
		if len(parts) > 1 && strings.Contains(strings.ToLower(parts[1]), "close") {
			*shouldClose = true
		}
		if len(parts) > 1 && strings.Contains(strings.ToLower(parts[1]), "upgrade") {
			line = "Connection: Upgrade\r\n"
		} else {
			line = ""
		}
	case "Upgrade":
		if len(parts) > 1 && strings.Contains(strings.ToLower(parts[1]), "websocket") {
			upgrade = true
		}
	}
	writer.WriteString(line)
	writer.Flush()
	stdout.WriteString(line)
	stdout.Flush()
	return upgrade
}

// forwardResponse reads response headers, dispatches to chunked or
// content-length body handling, and returns the status code for logging.
func forwardResponse(cServer net.Conn, c net.Conn) int {
	reader := bufio.NewReader(cServer)
	writer := bufio.NewWriter(c)
	contentLength := 0
	chunked := false
	status := 0

	for lineNumber := 1; ; lineNumber++ {
		line, err := reader.ReadString('\n')
		if err != nil {
			break
		}
		writer.WriteString(line)
		writer.Flush()

		if lineNumber == 1 {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				if n, err := strconv.Atoi(fields[1]); err == nil {
					status = n
				}
			}
		}

		trimmed := strings.TrimRight(line, "\r\n")
		parts := strings.SplitN(trimmed, ":", 2)
		if len(parts) == 2 {
			key := strings.TrimSpace(parts[0])
			val := strings.TrimSpace(parts[1])
			if strings.EqualFold(key, "Content-Length") {
				if n, err := strconv.Atoi(val); err == nil {
					contentLength = n
				}
			}
			if strings.EqualFold(key, "Transfer-Encoding") && strings.EqualFold(val, "chunked") {
				chunked = true
			}
		}

		if line == "\r\n" || line == "\n" {
			break
		}
	}

	if chunked {
		forwardChunks(reader, writer)
		return status
	}

	if contentLength > 0 {
		body := make([]byte, contentLength)
		if _, err := io.ReadFull(reader, body); err != nil {
			return status
		}
		writer.Write(body)
		writer.Flush()
	}
	return status
}

func forwardChunks(reader *bufio.Reader, writer *bufio.Writer) {
	for {
		sizeLine, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		writer.WriteString(sizeLine)
		writer.Flush()

		sizeHex := strings.TrimRight(sizeLine, "\r\n")
		size, err := strconv.ParseInt(sizeHex, 16, 64)
		if err != nil {
			return
		}

		if size == 0 {
			trailer, _ := reader.ReadString('\n')
			writer.WriteString(trailer)
			writer.Flush()
			return
		}

		chunk := make([]byte, size)
		if _, err := io.ReadFull(reader, chunk); err != nil {
			return
		}
		writer.Write(chunk)

		trailingCRLF := make([]byte, 2)
		io.ReadFull(reader, trailingCRLF)
		writer.Write(trailingCRLF)
		writer.Flush()
	}
}
