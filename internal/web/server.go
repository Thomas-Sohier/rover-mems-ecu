package web

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

	"rover-mems-agent/internal/ecu"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

//go:embed dashboard.html
var dashboardHTML []byte

const (
	writeWait   = 10 * time.Second
	idleTimeout = 60 * time.Second
)

var wsupgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		host := r.Host
		return strings.HasPrefix(host, "localhost:") || strings.HasPrefix(host, "127.0.0.1:") || host == "localhost"
	},
}

// Server holds the web server dependencies.
type Server struct {
	state *ecu.State
}

// NewServer creates a new web server with the given shared state.
func NewServer(state *ecu.State) *Server {
	return &Server{state: state}
}

// Run starts the HTTP/WebSocket server on the given bind address.
func (s *Server) Run(addr string) {
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(gin.Recovery())
	corsConfig := cors.DefaultConfig()
	corsConfig.AllowOrigins = []string{"http://localhost", "http://127.0.0.1"}
	router.Use(cors.New(corsConfig))

	api := router.Group("/api")
	{
		api.GET("", s.apiStateHandler)
		api.GET("/ports", s.apiPortsHandler)
	}

	router.GET("/", func(c *gin.Context) {
		c.Data(http.StatusOK, "text/html; charset=utf-8", dashboardHTML)
	})

	router.GET("/ping", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"message": "pong"})
	})

	router.GET("/connected", func(c *gin.Context) {
		s.state.RLock()
		connected := s.state.Connected
		s.state.RUnlock()
		c.JSON(http.StatusOK, gin.H{"connected": connected})
	})

	router.GET("/faults", func(c *gin.Context) {
		s.state.RLock()
		jsonData, err := json.Marshal(gin.H{"faults": s.state.Faults})
		s.state.RUnlock()
		if err != nil {
			c.AbortWithStatus(http.StatusInternalServerError)
			return
		}
		c.Data(http.StatusOK, "application/json", jsonData)
	})

	router.POST("/ecu/:name", func(c *gin.Context) {
		name := c.Param("name")
		s.state.Lock()
		s.state.EcuType = name
		s.state.Unlock()
		c.String(http.StatusOK, "ECU type set to %s", name)
	})

	router.POST("/serialPort/:name", func(c *gin.Context) {
		name := c.Param("name")
		s.state.Lock()
		s.state.SelectedSerialPort = name
		s.state.Unlock()
		c.String(http.StatusOK, "Serial port set to %s", name)
	})

	router.POST("/command/:name", func(c *gin.Context) {
		name := c.Param("name")
		s.state.Lock()
		s.state.UserCommand = name
		s.state.Unlock()
		c.String(http.StatusOK, "User command accepted %s", name)
	})

	router.GET("/ws", func(c *gin.Context) {
		s.wsHandler(c.Writer, c.Request)
	})

	srv := &http.Server{
		Addr:    addr,
		Handler: router,
	}

	go func() {
		s.state.LogDebug("Starting webserver on " + addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("listen: %s\n", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	s.state.LogDebug("Shutting down webserver...")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Fatal("Server forced to shutdown:", err)
	}
}

func (s *Server) apiStateHandler(c *gin.Context) {
	s.state.Lock()
	snapshot := gin.H{
		"faults":       s.state.Faults,
		"connected":    s.state.Connected,
		"ecuType":      s.state.EcuType,
		"userCommand":  s.state.UserCommand,
		"alert":        s.state.Alert,
		"error":        s.state.Error,
		"ecuData":      s.state.Data,
		"agentVersion": s.state.AgentVersion,
	}
	jsonData, err := json.Marshal(snapshot)
	s.state.Alert = ""
	s.state.Error = ""
	s.state.Unlock()

	if err != nil {
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	c.Data(http.StatusOK, "application/json", jsonData)
}

func (s *Server) apiPortsHandler(c *gin.Context) {
	s.state.RLock()
	jsonData, err := json.Marshal(gin.H{
		"ports":    s.state.SerialPorts,
		"selected": s.state.SelectedSerialPort,
	})
	s.state.RUnlock()

	if err != nil {
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	c.Data(http.StatusOK, "application/json", jsonData)
}

func (s *Server) wsHandler(w http.ResponseWriter, r *http.Request) {
	conn, err := wsupgrader.Upgrade(w, r, nil)
	if err != nil {
		s.state.LogDebug("Failed to upgrade WebSocket connection:", err)
		return
	}
	defer conn.Close()
	conn.SetReadLimit(2048)
	for {
		if err := s.wsIteration(conn); err != nil {
			break
		}
	}
}

func (s *Server) wsIteration(conn *websocket.Conn) error {
	conn.SetReadDeadline(time.Now().Add(idleTimeout))

	_, message, err := conn.ReadMessage()
	if err != nil {
		return err
	}

	var jsonData []byte

	if strings.TrimSpace(string(message)) == "." {
		s.state.Lock()
		payload := map[string]interface{}{
			"faults":             s.state.Faults,
			"connected":          s.state.Connected,
			"ecuType":            s.state.EcuType,
			"userCommand":        s.state.UserCommand,
			"alert":              s.state.Alert,
			"error":              s.state.Error,
			"ecuData":            s.state.Data,
			"agentVersion":       s.state.AgentVersion,
			"timestamp":          time.Now().String(),
			"serialPorts":        s.state.SerialPorts,
			"selectedSerialPort": s.state.SelectedSerialPort,
			"logLines":           s.state.LogLines,
		}
		jsonData, err = json.Marshal(payload)
		s.state.Alert = ""
		s.state.Error = ""
		s.state.Unlock()

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
