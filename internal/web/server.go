package web

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"rover-mems-agent/internal/ecu"
	"rover-mems-agent/internal/nowplaying"

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
	state      *ecu.State
	nowPlaying *nowplaying.Store
}

// NewServer creates a new web server with the given shared state and now-playing store.
func NewServer(state *ecu.State, np *nowplaying.Store) *Server {
	return &Server{state: state, nowPlaying: np}
}

// buildRouter wires all routes and returns the handler. Separated so tests can
// create the router without starting a listener.
func (s *Server) buildRouter(ctx context.Context) http.Handler {
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
		api.GET("/nowplaying", s.apiNowPlayingHandler)
		api.GET("/nowplaying/art", s.apiNowPlayingArtHandler)
	}

	router.GET("/", func(c *gin.Context) {
		c.Data(http.StatusOK, "text/html; charset=utf-8", dashboardHTML)
	})

	router.GET("/ping", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"message": "pong"})
	})

	router.GET("/connected", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"connected": s.state.Snapshot().Connected})
	})

	router.GET("/faults", func(c *gin.Context) {
		jsonData, err := json.Marshal(gin.H{"faults": s.state.Snapshot().Faults})
		if err != nil {
			c.AbortWithStatus(http.StatusInternalServerError)
			return
		}
		c.Data(http.StatusOK, "application/json", jsonData)
	})

	router.POST("/ecu/:name", func(c *gin.Context) {
		name := c.Param("name")
		s.state.SetEcuType(name)
		c.String(http.StatusOK, "ECU type set to %s", name)
	})

	router.POST("/serialPort/:name", func(c *gin.Context) {
		name := c.Param("name")
		s.state.SetSelectedSerialPort(name)
		c.String(http.StatusOK, "Serial port set to %s", name)
	})

	router.POST("/command/:name", func(c *gin.Context) {
		name := c.Param("name")
		s.state.SetUserCommand(name)
		c.String(http.StatusOK, "User command accepted %s", name)
	})

	router.GET("/ws", func(c *gin.Context) {
		s.wsHandler(c.Writer, c.Request)
	})

	router.GET("/ws/nowplaying", func(c *gin.Context) {
		s.wsNowPlayingHandler(c.Writer, c.Request, ctx)
	})

	return router
}

// Run starts the HTTP/WebSocket server on the given bind address.
func (s *Server) Run(ctx context.Context, addr string) {
	router := s.buildRouter(ctx)

	srv := &http.Server{
		Addr:    addr,
		Handler: router,
	}

	go func() {
		s.state.LogDebug("Starting webserver on " + addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.state.LogDebugf("listen: %s", err)
		}
	}()

	<-ctx.Done()
	s.state.LogDebug("Shutting down webserver...")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		s.state.LogDebugf("Server forced to shutdown: %s", err)
	}
}

func (s *Server) apiStateHandler(c *gin.Context) {
	snap := s.state.Snapshot()
	alert, errMsg := s.state.ConsumeAlertError()
	jsonData, err := json.Marshal(gin.H{
		"faults":       snap.Faults,
		"connected":    snap.Connected,
		"ecuType":      snap.EcuType,
		"userCommand":  snap.UserCommand,
		"alert":        alert,
		"error":        errMsg,
		"ecuData":      snap.Data,
		"agentVersion": snap.AgentVersion,
	})

	if err != nil {
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	c.Data(http.StatusOK, "application/json", jsonData)
}

func (s *Server) apiPortsHandler(c *gin.Context) {
	snap := s.state.Snapshot()
	jsonData, err := json.Marshal(gin.H{
		"ports":    snap.SerialPorts,
		"selected": snap.SelectedSerialPort,
	})

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
	if err := conn.SetReadDeadline(time.Now().Add(idleTimeout)); err != nil {
		return err
	}

	_, message, err := conn.ReadMessage()
	if err != nil {
		return err
	}

	if strings.TrimSpace(string(message)) != "." {
		s.state.LogDebugf("ws: ignoring unexpected message: %s", message)
		return nil
	}

	snap := s.state.Snapshot()
	alert, errMsg := s.state.ConsumeAlertError()
	payload := map[string]any{
		"faults":             snap.Faults,
		"connected":          snap.Connected,
		"ecuType":            snap.EcuType,
		"userCommand":        snap.UserCommand,
		"alert":              alert,
		"error":              errMsg,
		"ecuData":            snap.Data,
		"agentVersion":       snap.AgentVersion,
		"timestamp":          time.Now().String(),
		"serialPorts":        snap.SerialPorts,
		"selectedSerialPort": snap.SelectedSerialPort,
		"logLines":           snap.LogLines,
	}
	jsonData, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	if err := conn.SetWriteDeadline(time.Now().Add(writeWait)); err != nil {
		return err
	}
	return conn.WriteMessage(websocket.TextMessage, jsonData)
}

func (s *Server) apiNowPlayingHandler(c *gin.Context) {
	snap := s.nowPlaying.Snapshot()
	jsonData, err := json.Marshal(snap)
	if err != nil {
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	c.Data(http.StatusOK, "application/json", jsonData)
}

func (s *Server) apiNowPlayingArtHandler(c *gin.Context) {
	_, jpeg, ok := s.nowPlaying.Art()
	if !ok {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	c.Data(http.StatusOK, "image/jpeg", jpeg)
}

func (s *Server) wsNowPlayingHandler(w http.ResponseWriter, r *http.Request, ctx context.Context) {
	conn, err := wsupgrader.Upgrade(w, r, nil)
	if err != nil {
		s.state.LogDebug("ws/nowplaying: upgrade failed:", err)
		return
	}
	defer conn.Close()
	conn.SetReadLimit(512)
	// Listeners never write: keep the connection alive with pings, and extend
	// the read deadline on each pong.
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(idleTimeout))
	})

	// Send current snapshot immediately.
	if err := s.wsNowPlayingWrite(conn, s.nowPlaying.Snapshot()); err != nil {
		return
	}

	ch, unsub := s.nowPlaying.Subscribe()
	defer unsub()

	// Reader goroutine: discard incoming messages, signal close on error.
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := conn.SetReadDeadline(time.Now().Add(idleTimeout)); err != nil {
			return
		}
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()

	pingTicker := time.NewTicker(idleTimeout / 2)
	defer pingTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-done:
			return
		case <-pingTicker.C:
			if err := conn.SetWriteDeadline(time.Now().Add(writeWait)); err != nil {
				return
			}
			if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		case snap, ok := <-ch:
			if !ok {
				return
			}
			if err := s.wsNowPlayingWrite(conn, snap); err != nil {
				return
			}
		}
	}
}

func (s *Server) wsNowPlayingWrite(conn *websocket.Conn, snap nowplaying.Snapshot) error {
	jsonData, err := json.Marshal(snap)
	if err != nil {
		return err
	}
	if err := conn.SetWriteDeadline(time.Now().Add(writeWait)); err != nil {
		return err
	}
	return conn.WriteMessage(websocket.TextMessage, jsonData)
}
