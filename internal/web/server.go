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
	"rover-mems-agent/internal/headunit"
	"rover-mems-agent/internal/navigation"
	"rover-mems-agent/internal/notification"
	"rover-mems-agent/internal/nowplaying"
	"rover-mems-agent/internal/wifi"

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
	state        *ecu.State
	nowPlaying   *nowplaying.Store
	navigation   *navigation.Store
	notification *notification.Store
	headunit     *headunit.Store
}

// NewServer creates a new web server with the given shared state and the
// now-playing, navigation, notification, and head-unit-control stores.
func NewServer(state *ecu.State, np *nowplaying.Store, nav *navigation.Store, notif *notification.Store, hu *headunit.Store) *Server {
	return &Server{state: state, nowPlaying: np, navigation: nav, notification: notif, headunit: hu}
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
		api.GET("/navigation", s.apiNavigationHandler)
		api.GET("/navigation/icon", s.apiNavigationIconHandler)
		api.GET("/notifications", s.apiNotificationsHandler)
		api.GET("/headunit", s.apiHeadUnitHandler)
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

	router.POST("/wifi/enable", func(c *gin.Context) {
		if err := wifi.EnableWifi(); err != nil {
			c.String(http.StatusInternalServerError, err.Error())
			return
		}
		c.String(http.StatusOK, "wifi enabled")
	})

	router.POST("/wifi/disable", func(c *gin.Context) {
		if err := wifi.DisableWifi(); err != nil {
			c.String(http.StatusInternalServerError, err.Error())
			return
		}
		c.String(http.StatusOK, "wifi disabled")
	})

	router.GET("/ws", func(c *gin.Context) {
		s.wsHandler(c.Writer, c.Request)
	})

	router.GET("/ws/nowplaying", func(c *gin.Context) {
		s.wsNowPlayingHandler(c.Writer, c.Request, ctx)
	})

	router.GET("/ws/navigation", func(c *gin.Context) {
		s.wsNavigationHandler(c.Writer, c.Request, ctx)
	})

	router.GET("/ws/notifications", func(c *gin.Context) {
		s.wsNotificationsHandler(c.Writer, c.Request, ctx)
	})

	router.GET("/ws/headunit", func(c *gin.Context) {
		s.wsHeadUnitHandler(c.Writer, c.Request, ctx)
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

// --- Navigation ---

func (s *Server) apiNavigationHandler(c *gin.Context) {
	snap := s.navigation.Snapshot()
	jsonData, err := json.Marshal(snap)
	if err != nil {
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	c.Data(http.StatusOK, "application/json", jsonData)
}

func (s *Server) apiNavigationIconHandler(c *gin.Context) {
	_, png, ok := s.navigation.Icon()
	if !ok {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	c.Data(http.StatusOK, "image/png", png)
}

// wsNavigationHandler streams navigation snapshots. Like /ws/nowplaying it
// pushes the current snapshot on connect, then every change. The PNG maneuver
// icon itself is fetched separately from /api/navigation/icon; the snapshot
// carries icon_id and has_icon.
func (s *Server) wsNavigationHandler(w http.ResponseWriter, r *http.Request, ctx context.Context) {
	conn, err := wsupgrader.Upgrade(w, r, nil)
	if err != nil {
		s.state.LogDebug("ws/navigation: upgrade failed:", err)
		return
	}
	defer conn.Close()
	conn.SetReadLimit(512)
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(idleTimeout))
	})

	if err := s.wsJSONWrite(conn, s.navigation.Snapshot()); err != nil {
		return
	}

	ch, unsub := s.navigation.Subscribe()
	defer unsub()

	done := s.wsReaderDone(conn)
	pingTicker := time.NewTicker(idleTimeout / 2)
	defer pingTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-done:
			return
		case <-pingTicker.C:
			if err := s.wsPing(conn); err != nil {
				return
			}
		case snap, ok := <-ch:
			if !ok {
				return
			}
			if err := s.wsJSONWrite(conn, snap); err != nil {
				return
			}
		}
	}
}

// --- Notifications (alerts) ---

func (s *Server) apiNotificationsHandler(c *gin.Context) {
	alert, ok := s.notification.Last()
	if !ok {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	jsonData, err := json.Marshal(alert)
	if err != nil {
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	c.Data(http.StatusOK, "application/json", jsonData)
}

// wsNotificationsHandler streams alerts as they arrive. Alerts are fire-once
// events, so — unlike navigation/now-playing — no initial snapshot is sent on
// connect; only alerts posted while connected are delivered.
func (s *Server) wsNotificationsHandler(w http.ResponseWriter, r *http.Request, ctx context.Context) {
	conn, err := wsupgrader.Upgrade(w, r, nil)
	if err != nil {
		s.state.LogDebug("ws/notifications: upgrade failed:", err)
		return
	}
	defer conn.Close()
	conn.SetReadLimit(512)
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(idleTimeout))
	})

	ch, unsub := s.notification.Subscribe()
	defer unsub()

	done := s.wsReaderDone(conn)
	pingTicker := time.NewTicker(idleTimeout / 2)
	defer pingTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-done:
			return
		case <-pingTicker.C:
			if err := s.wsPing(conn); err != nil {
				return
			}
		case alert, ok := <-ch:
			if !ok {
				return
			}
			if err := s.wsJSONWrite(conn, alert); err != nil {
				return
			}
		}
	}
}

// --- Head-unit remote control ---

// apiHeadUnitHandler returns the cached catalog reported by the frontend, or 404
// when none has been reported yet.
func (s *Server) apiHeadUnitHandler(c *gin.Context) {
	catalog, ok := s.headunit.Catalog()
	if !ok {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	c.Data(http.StatusOK, "application/json", catalog)
}

// wsHeadUnitHandler bridges the on-device frontend to the phone. It is
// bidirectional: the frontend pushes its catalog (JSON object) as text messages,
// which are cached and relayed to the phone over BLE; the agent writes the
// phone's commands (JSON) to the socket so the frontend can apply them and push
// an updated catalog back. The frontend is the single source of truth.
func (s *Server) wsHeadUnitHandler(w http.ResponseWriter, r *http.Request, ctx context.Context) {
	conn, err := wsupgrader.Upgrade(w, r, nil)
	if err != nil {
		s.state.LogDebug("ws/headunit: upgrade failed:", err)
		return
	}
	defer conn.Close()
	conn.SetReadLimit(64 * 1024)
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(idleTimeout))
	})

	cmdCh, unsub := s.headunit.SubscribeCommands()
	defer unsub()

	// Reader goroutine: each inbound message is a catalog update from the
	// frontend. Closes done on read error (peer gone).
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := conn.SetReadDeadline(time.Now().Add(idleTimeout)); err != nil {
			return
		}
		for {
			_, message, err := conn.ReadMessage()
			if err != nil {
				return
			}
			if err := s.headunit.SetCatalog(message); err != nil {
				s.state.LogDebugf("ws/headunit: catalog rejected: %v", err)
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
			if err := s.wsPing(conn); err != nil {
				return
			}
		case cmd, ok := <-cmdCh:
			if !ok {
				return
			}
			if err := s.wsBytesWrite(conn, cmd); err != nil {
				return
			}
		}
	}
}

// --- shared WebSocket helpers (listen-only streams) ---

// wsReaderDone starts a goroutine that discards inbound messages and closes the
// returned channel when the peer goes away, so a write loop can detect closure.
func (s *Server) wsReaderDone(conn *websocket.Conn) <-chan struct{} {
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
	return done
}

func (s *Server) wsPing(conn *websocket.Conn) error {
	if err := conn.SetWriteDeadline(time.Now().Add(writeWait)); err != nil {
		return err
	}
	return conn.WriteMessage(websocket.PingMessage, nil)
}

// wsBytesWrite writes a pre-serialized JSON payload as a text message.
func (s *Server) wsBytesWrite(conn *websocket.Conn, payload []byte) error {
	if err := conn.SetWriteDeadline(time.Now().Add(writeWait)); err != nil {
		return err
	}
	return conn.WriteMessage(websocket.TextMessage, payload)
}

func (s *Server) wsJSONWrite(conn *websocket.Conn, v any) error {
	jsonData, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if err := conn.SetWriteDeadline(time.Now().Add(writeWait)); err != nil {
		return err
	}
	return conn.WriteMessage(websocket.TextMessage, jsonData)
}
