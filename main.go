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
