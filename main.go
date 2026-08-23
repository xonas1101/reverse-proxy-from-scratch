package main

import (
	"log"
	"net"
)

func main() {
	listener, err := net.Listen("tcp", ":8080")
	if err != nil {
		log.Printf("Internal Server Error: %v\n", err)
	}

	listServer, err := net.Listen("tcp", ":9090")
	if err != nil {
		log.Printf("Internal Server Error: %v\n", err)
	}

	connServer, err := listServer.Accept()
	if err != nil {
		log.Printf("Error connecting to server: %v\n", err)
	}
	go handleServer(connServer)
	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Printf("Error connecting to server: %v\n", err)
		}
		go handleConnection(conn)
	}
}

func handleConnection(c net.Conn) {

}

func handleServer(c net.Conn) {

}
