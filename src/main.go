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
	"mud_server/src/internal/worldfile"
)

var upgrader = websocket.Upgrader{
	Subprotocols: []string{mudserver.Subprotocol},
	CheckOrigin: func(r *http.Request) bool {
	 return true
	},
}

func main() {
	// A check for content authors and CI: no database, no server.
	if len(os.Args) > 1 && os.Args[1] == "-validate-world" {
		os.Exit(worldfile.Run(os.Args[2:], os.Stdout, os.Stderr))
	}
	if len(os.Args) > 1 && os.Args[1] == "-render-world" {
		os.Exit(worldfile.RunRender(os.Args[2:], os.Stdout, os.Stderr))
	}
	if len(os.Args) > 1 && os.Args[1] == "-add-gm" {
		os.Exit(mudserver.RunAddGM(os.Args[2:], os.Stdin, os.Stdout, os.Stderr))
	}
	if len(os.Args) > 1 && os.Args[1] == "-reset-players" {
		os.Exit(mudserver.RunResetPlayers(os.Args[2:], os.Stdin, os.Stdout, os.Stderr))
	}
	if len(os.Args) > 1 && os.Args[1] == "-reset-player" {
		os.Exit(mudserver.RunResetPlayer(os.Args[2:], os.Stdin, os.Stdout, os.Stderr))
	}

	serverinstance := mudserver.CreateMudServer()

	http.HandleFunc("/", homePage)
	http.HandleFunc("/api/leaderboard", serverinstance.LeaderboardHandler)
	http.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) { handleConnections(serverinstance, w, r) })

	// CreateMudServer has started the game loop.

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

	if !mudserver.RefuseUnlessProtocol(conn) {
		return
	}

	// Once connection is upgraded, create a MudConn
	mc := si.AddConnection(conn, si.ClientIP(r))

	// Create goroutine to handle Input processing until MudConn closes
	go si.ProcessInput(mc)
}