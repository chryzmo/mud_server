package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

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
	http.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) { handleConnections(serverinstance, w, r) })

	go serverinstance.HandleMessages()
	go serverinstance.RunMaintenanceactions()

	httpServer := &http.Server{Addr: serverinstance.Configuration.Server.ListenPort}

	// On SIGTERM (systemd stop or restart) or Ctrl-C: stop accepting
	// connections, then save and disconnect everyone.
	stop, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer cancel()
	go func() {
		<-stop.Done()
		fmt.Println("Shutting down")
		ctx, cancelShutdown := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancelShutdown()
		httpServer.Shutdown(ctx)
		serverinstance.Shutdown(ctx)
	}()

	fmt.Println("Server starting on "+serverinstance.Configuration.Server.ListenPort)
	err := httpServer.ListenAndServe()
	if err != nil && err != http.ErrServerClosed {
		panic("Error starting server: " + err.Error())
	}
	<-stop.Done()
	// Give the shutdown goroutine time to finish saving before main returns.
	serverinstance.Shutdown(context.Background())
	fmt.Println("Server stopped")
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