package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

//go:embed dashboard.html
var dashboardHTML []byte

const (
	// Time allowed to write a message to the peer.
	writeWait = 10 * time.Second
	// Time allowed to read the next message from the peer before assuming they disconnected.
	idleTimeout = 60 * time.Second
)

// wsupgrader is configured once at package level.
// FIX: Restricted CheckOrigin strictly to localhost for local usage security.
var wsupgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		host := r.Host
		// Allow strictly local loopback addresses
		return strings.HasPrefix(host, "localhost:") || strings.HasPrefix(host, "127.0.0.1:") || host == "localhost"
	},
}

// runWebserver starts the HTTP/WebSocket server on the given bind address (e.g. ":8080").
func runWebserver(addr string) {
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(gin.Recovery())
	corsConfig := cors.DefaultConfig()
	corsConfig.AllowOrigins = []string{"http://localhost", "http://127.0.0.1"}
	router.Use(cors.New(corsConfig))

	// --- State read/write endpoints ---
	api := router.Group("/api")
	{
		api.GET("", apiStateHandler)
		api.GET("/ports", apiPortsHandler)
	}

	// --- Dashboard ---
	router.GET("/", func(c *gin.Context) {
		c.Data(http.StatusOK, "text/html; charset=utf-8", dashboardHTML)
	})

	// --- Legacy flat routes (kept for backwards compatibility) ---
	router.GET("/ping", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"message": "pong"})
	})

	router.GET("/connected", func(c *gin.Context) {
		globalDataOutputLock.RLock()
		connected := globalConnected
		globalDataOutputLock.RUnlock()
		c.JSON(http.StatusOK, gin.H{"connected": connected})
	})

	router.GET("/faults", func(c *gin.Context) {
		globalDataOutputLock.RLock()
		jsonData, err := json.Marshal(gin.H{"faults": globalFaults})
		globalDataOutputLock.RUnlock()

		if err != nil {
			c.AbortWithStatus(http.StatusInternalServerError)
			return
		}
		c.Data(http.StatusOK, "application/json", jsonData)
	})

	// --- Configuration endpoints ---
	router.POST("/ecu/:name", func(c *gin.Context) {
		name := c.Param("name")
		globalDataOutputLock.Lock()
		globalEcuType = name
		globalDataOutputLock.Unlock()
		c.String(http.StatusOK, "ECU type set to %s", name)
	})

	router.POST("/serialPort/:name", func(c *gin.Context) {
		name := c.Param("name")
		globalDataOutputLock.Lock()
		globalSelectedSerialPort = name
		globalDataOutputLock.Unlock()
		c.String(http.StatusOK, "Serial port set to %s", name)
	})

	router.POST("/command/:name", func(c *gin.Context) {
		name := c.Param("name")
		globalDataOutputLock.Lock()
		globalUserCommand = name
		globalDataOutputLock.Unlock()
		c.String(http.StatusOK, "User command accepted %s", name)
	})

	// --- WebSocket ---
	router.GET("/ws", func(c *gin.Context) {
		wsHandler(c.Writer, c.Request)
	})

	srv := &http.Server{
		Addr:    addr,
		Handler: router,
	}

	go func() {
		logDebug("Starting webserver on " + addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("listen: %s\n", err)
		}
	}()

	// Wait for interrupt signal to gracefully shutdown the server
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	logDebug("Shutting down webserver...")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Fatal("Server forced to shutdown:", err)
	}
}

// apiStateHandler returns the full agent state as JSON.
func apiStateHandler(c *gin.Context) {
	globalDataOutputLock.Lock()
	snapshot := gin.H{
		"faults":       globalFaults,
		"connected":    globalConnected,
		"ecuType":      globalEcuType,
		"userCommand":  globalUserCommand,
		"alert":        globalAlert,
		"error":        globalError,
		"ecuData":      globalDataOutput,
		"agentVersion": globalAgentVersion,
	}

	// FIX: Marshal JSON INSIDE the lock. If ecuData is a map/slice, marshalling it
	// outside the lock will cause a `concurrent map iteration` panic.
	jsonData, err := json.Marshal(snapshot)

	globalAlert = ""
	globalError = ""
	globalDataOutputLock.Unlock()

	if err != nil {
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	c.Data(http.StatusOK, "application/json", jsonData)
}

// apiPortsHandler returns the list of discovered serial ports.
func apiPortsHandler(c *gin.Context) {
	globalDataOutputLock.RLock()
	// FIX: Marshal inside lock to protect slices/maps
	jsonData, err := json.Marshal(gin.H{
		"ports":    globalSerialPorts,
		"selected": globalSelectedSerialPort,
	})
	globalDataOutputLock.RUnlock()

	if err != nil {
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	c.Data(http.StatusOK, "application/json", jsonData)
}

// wsHandler upgrades the HTTP connection to a WebSocket and enters the read/write loop.
func wsHandler(w http.ResponseWriter, r *http.Request) {
	conn, err := wsupgrader.Upgrade(w, r, nil)
	if err != nil {
		logDebug("Failed to upgrade WebSocket connection:", err)
		return
	}
	defer conn.Close()
	conn.SetReadLimit(2048)
	for {
		if err := wsIteration(conn); err != nil {
			break
		}
	}
}

// wsIteration handles one request/response cycle on the WebSocket connection.
func wsIteration(conn *websocket.Conn) error {
	// As long as the client sends "." before this timeout, the connection stays alive.
	conn.SetReadDeadline(time.Now().Add(idleTimeout))

	_, message, err := conn.ReadMessage()
	if err != nil {
		return err
	}

	var jsonData []byte

	if strings.TrimSpace(string(message)) == "." {
		globalDataOutputLock.Lock()
		payload := map[string]interface{}{
			"faults":             globalFaults,
			"connected":          globalConnected,
			"ecuType":            globalEcuType,
			"userCommand":        globalUserCommand,
			"alert":              globalAlert,
			"error":              globalError,
			"ecuData":            globalDataOutput,
			"agentVersion":       globalAgentVersion,
			"timestamp":          time.Now().String(),
			"serialPorts":        globalSerialPorts,
			"selectedSerialPort": globalSelectedSerialPort,
			"logLines":           globalLogLines,
		}

		jsonData, err = json.Marshal(payload)
		globalAlert = ""
		globalError = ""
		globalDataOutputLock.Unlock()

		if err != nil {
			return err
		}
	} else {
		log.Printf("ws recv: %s", message)
		payload := map[string]interface{}{"command": "worked"}
		jsonData, err = json.Marshal(payload)
		if err != nil {
			return err
		}
	}

	conn.SetWriteDeadline(time.Now().Add(writeWait))
	return conn.WriteMessage(websocket.TextMessage, jsonData)
}
