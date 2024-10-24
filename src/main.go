package main

import (
	"fmt"
	"net/http"

	"github.com/gorilla/websocket"

	mudserver "mud_server/src/internal/mudserver"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
	 return true
	},
}

func main() {
	serverinstance := mudserver.CreateMudServer()

	http.HandleFunc("/", homePage)
	http.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {handleConnections(serverinstance, w, r)})

	go serverinstance.HandleMessages()
	go serverinstance.RunMaintenanceactions()

	fmt.Println("Server starting on "+serverinstance.Configuration.Server.ListenPort)
	err := http.ListenAndServe(serverinstance.Configuration.Server.ListenPort, nil)
	if err != nil {
		panic("Error starting server: " + err.Error())
	}
	fmt.Println("Server started on "+serverinstance.Configuration.Server.ListenPort)
}

func homePage(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintf(w, "Welcome!")
}

func handleConnections(si *mudserver.MudServer,w http.ResponseWriter, r *http.Request) {
	// Upgrade the HTTP connection to a Websocket connection
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		fmt.Println(err)
		return
	}

	// Once connection is upgraded, create a MudConn
	mc := si.AddConnection(conn)

	// Create goroutine to handle Input processing until MudConn closes
	go si.ProcessInput(mc)
}