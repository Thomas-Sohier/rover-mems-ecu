package main

import (
	"encoding/json"
	_ "embed"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

//go:embed dashboard.html
var dashboardHTML []byte

// wsupgrader is configured once at package level.
// CheckOrigin allows all origins (suitable for a local diagnostic tool).
var wsupgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin:     func(r *http.Request) bool { return true },
}

// runWebserver starts the HTTP/WebSocket server on the given bind address (e.g. ":8080").
func runWebserver(addr string) {
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard // silence Gin's request log in production

	router := gin.Default()
	router.Use(cors.Default())

	// --- State read/write endpoints ---
	api := router.Group("/api")
	{
		// Full state snapshot (also clears one-shot alert/error fields)
		api.GET("", apiStateHandler)
		// List discovered serial ports and currently selected one
		api.GET("/ports", apiPortsHandler)
	}

	// --- Dashboard ---
	router.GET("/", func(c *gin.Context) {
		c.Data(http.StatusOK, "text/html; charset=utf-8", dashboardHTML)
	})

	// --- Legacy flat routes (kept for backwards compatibility) ---
	router.GET("/ping", func(c *gin.Context) {
		c.JSON(200, gin.H{"message": "pong"})
	})
	router.GET("/connected", func(c *gin.Context) {
		globalDataOutputLock.RLock()
		connected := globalConnected
		globalDataOutputLock.RUnlock()
		c.JSON(200, gin.H{"connected": connected})
	})
	router.GET("/faults", func(c *gin.Context) {
		globalDataOutputLock.RLock()
		faults := globalFaults
		globalDataOutputLock.RUnlock()
		c.JSON(200, gin.H{"faults": faults})
	})

	// --- Configuration endpoints ---
	router.GET("/ecu/:name", func(c *gin.Context) {
		name := c.Param("name")
		globalDataOutputLock.Lock()
		globalEcuType = name
		globalDataOutputLock.Unlock()
		c.String(http.StatusOK, "ECU type set to %s", name)
	})
	router.GET("/serialPort/:name", func(c *gin.Context) {
		name := c.Param("name")
		globalDataOutputLock.Lock()
		globalSelectedSerialPort = name
		globalDataOutputLock.Unlock()
		c.String(http.StatusOK, "Serial port set to %s", name)
	})
	router.GET("/command/:name", func(c *gin.Context) {
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

	logDebug("Starting webserver on " + addr)
	router.Run(addr)
}

// apiStateHandler returns the full agent state as JSON.
// It atomically clears the one-shot alert and error fields so that
// a second call does not repeat them.
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
	globalAlert = ""
	globalError = ""
	globalDataOutputLock.Unlock()

	c.JSON(200, snapshot)
}

// apiPortsHandler returns the list of discovered serial ports and which one is selected.
func apiPortsHandler(c *gin.Context) {
	globalDataOutputLock.RLock()
	ports := globalSerialPorts
	selected := globalSelectedSerialPort
	globalDataOutputLock.RUnlock()

	c.JSON(200, gin.H{
		"ports":    ports,
		"selected": selected,
	})
}

// wsHandler upgrades the HTTP connection to a WebSocket and enters the read/write loop.
func wsHandler(w http.ResponseWriter, r *http.Request) {
	conn, err := wsupgrader.Upgrade(w, r, nil)
	if err != nil {
		logDebug("Failed to upgrade WebSocket connection:", err)
		return
	}

	for {
		if err := wsIteration(conn); err != nil {
			break
		}
	}
}

// wsIteration handles one request/response cycle on the WebSocket connection.
// The browser sends "." to request a state snapshot; any other message is treated as a command.
func wsIteration(conn *websocket.Conn) error {
	_, message, err := conn.ReadMessage()
	if err != nil {
		return err
	}

	var payload map[string]interface{}

	if strings.TrimSpace(string(message)) == "." {
		// State request: snapshot everything under lock, then release before encoding
		globalDataOutputLock.Lock()
		payload = map[string]interface{}{
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
		globalAlert = ""
		globalError = ""
		globalDataOutputLock.Unlock()
	} else {
		log.Printf("ws recv: %s", message)
		payload = map[string]interface{}{"command": "worked"}
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	return conn.WriteMessage(websocket.TextMessage, jsonData)
}
