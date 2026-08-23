package main

import (
	"bufio"
	"fmt"
	"log"
	"net"
	"os"
	"strings"
)

func main() {
	listener, err := net.Listen("tcp", ":8080")
	if err != nil {
		log.Fatalf("Internal Server Error: %v\n", err)
	}

	connServer, err := net.Dial("tcp", "localhost:9090")
	if err != nil {
		log.Fatalf("Internal Server Error: %v\n", err)
	}

	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Printf("Error connecting to server: %v\n", err)
			continue
		}
		go handleConnection(connServer, conn)
	}
}

func handleConnection(cServer net.Conn, c net.Conn) {
	defer c.Close()

	reader := bufio.NewReader(c)
	stdout := bufio.NewWriter(os.Stdout)
	writer := bufio.NewWriter(cServer)
	host, _, _ := net.SplitHostPort(c.RemoteAddr().String())

	for lineNumber := 1; ; lineNumber++ {
		if lineNumber == 2 {
			fmt.Fprintf(writer, "X-Forwarded-For: %s\r\n", host)
			writer.Flush()
		}
		line, _ := reader.ReadString('\n')
		stdout.WriteString(line)
		stdout.Flush()
		handleServer(line, cServer)

		if line == "\r\n" || line == "\n" {
			break
		}
	}
}

func handleServer(line string, c net.Conn) {
	writer := bufio.NewWriter(c)
	stdout := bufio.NewWriter(os.Stdout)

	parts := strings.Split(line, ":")

	switch parts[0] {
	case "Host":
		line = fmt.Sprintf("Host: %s\r\n", c.RemoteAddr().String())
	case "Connection":
		line = ""
	}

	writer.WriteString(line)
	writer.Flush()
	stdout.WriteString(line)
	stdout.Flush()
}
