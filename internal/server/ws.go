package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"codeberg.org/Dehydration/lentitud/internal/hub"
	"codeberg.org/Dehydration/lentitud/internal/protocol"

	"github.com/gorilla/websocket"
)

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	if s.shutdown.Load() {
		http.Error(w, "server is shutting down", http.StatusServiceUnavailable)
		return
	}

	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		s.log.Warn("websocket upgrade failed", "error", err, "remote", r.RemoteAddr)
		return
	}
	defer conn.Close()

	s.serve(conn, r.RemoteAddr)
}

func (s *Server) serve(conn *websocket.Conn, remote string) {
	c := hub.NewClient(conn, s.cfg.SendBuffer)
	defer c.Close()

	conn.SetReadLimit(int64(s.cfg.MaxMessageBytes) + 4096)
	conn.SetReadDeadline(time.Now().Add(s.cfg.JoinTimeout))

	mt, data, err := conn.ReadMessage()
	if err != nil {
		s.log.Debug("join read failed", "error", err, "remote", remote)
		return
	}
	if mt != websocket.TextMessage {
		s.fail(conn, "expected a text join message")
		return
	}

	var join protocol.Envelope
	if err := json.Unmarshal(data, &join); err != nil || join.Type != protocol.TypeJoin {
		s.fail(conn, "the first message must be a join message")
		return
	}
	if err := protocol.ValidateNick(join.Nick); err != nil {
		s.fail(conn, "invalid nickname: "+err.Error())
		return
	}
	if s.hub.Len() >= s.cfg.MaxClients {
		s.fail(conn, "server full")
		return
	}
	if err := s.hub.Add(c, join.Nick); err != nil {
		s.fail(conn, "nickname already in use")
		return
	}

	writerDone := make(chan struct{})
	go func() {
		s.writePump(c)
		close(writerDone)
	}()

	defer func() {
		s.hub.Remove(c.Nick)
		c.Stop()
		select {
		case <-writerDone:
		case <-time.After(s.cfg.WriteTimeout):
			c.Close()
			<-writerDone
		}
		c.Close()
		s.broadcastSystem(nil, fmt.Sprintf("%s left", join.Nick))
		s.log.Info("client disconnected", "nick", join.Nick, "remote", remote)
	}()

	s.send(c, protocol.New(protocol.TypeWelcome, join.Nick, ""))
	s.broadcastSystem(c, fmt.Sprintf("%s joined", join.Nick))
	s.log.Info("client connected", "nick", join.Nick, "remote", remote)

	conn.SetReadDeadline(time.Now().Add(s.cfg.PongWait))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(s.cfg.PongWait))
	})
	conn.SetCloseHandler(func(int, string) error {
		return nil
	})

	limiter := rateLimiter{}

	for {
		mt, data, err := conn.ReadMessage()
		if err != nil {
			if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				s.log.Debug("client read failed", "nick", join.Nick, "error", err)
			}
			return
		}
		if mt != websocket.TextMessage {
			s.sendError(c, "expected a text message")
			continue
		}

		var env protocol.Envelope
		if err := json.Unmarshal(data, &env); err != nil {
			s.sendError(c, "invalid JSON")
			continue
		}

		switch env.Type {
		case protocol.TypeMessage:
			if !limiter.allow(time.Now(), s.cfg.RateLimitPerSecond) {
				s.sendError(c, "rate limit exceeded")
				continue
			}
			if err := protocol.ValidateText(env.Text, s.cfg.MaxMessageBytes); err != nil {
				s.sendError(c, err.Error())
				continue
			}
			out := protocol.New(protocol.TypeMessage, join.Nick, env.Text)
			data, err := json.Marshal(out)
			if err != nil {
				s.log.Error("failed to marshal message", "error", err)
				s.sendError(c, "failed to encode message")
				continue
			}
			s.sendData(c, data)
			s.hub.Broadcast(c, data)
		default:
			s.sendError(c, "unsupported message type")
		}
	}
}

func (s *Server) writePump(c *hub.Client) {
	ticker := time.NewTicker(s.cfg.PingInterval)
	defer ticker.Stop()

	for {
		select {
		case msg := <-c.Send():
			if err := s.writeMessage(c, websocket.TextMessage, msg); err != nil {
				c.Close()
				return
			}
		case <-ticker.C:
			if err := s.writeMessage(c, websocket.PingMessage, nil); err != nil {
				c.Close()
				return
			}
		case <-c.Done():
			for {
				select {
				case msg := <-c.Send():
					if err := s.writeMessage(c, websocket.TextMessage, msg); err != nil {
						return
					}
				default:
					_ = s.writeMessage(c, websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
					return
				}
			}
		}
	}
}

func (s *Server) writeMessage(c *hub.Client, messageType int, msg []byte) error {
	if err := c.Conn.SetWriteDeadline(time.Now().Add(s.cfg.WriteTimeout)); err != nil {
		return err
	}
	return c.Conn.WriteMessage(messageType, msg)
}

func (s *Server) send(c *hub.Client, env protocol.Envelope) {
	data, err := json.Marshal(env)
	if err != nil {
		s.log.Error("failed to marshal message", "error", err)
		return
	}
	s.sendData(c, data)
}

func (s *Server) sendData(c *hub.Client, data []byte) {
	if !c.Enqueue(data) {
		c.Close()
	}
}

func (s *Server) sendError(c *hub.Client, text string) {
	s.send(c, protocol.New(protocol.TypeError, "", text))
}

// broadcastSystem sends a system message to every client. A non-nil sender is
// excluded from the broadcast.
func (s *Server) broadcastSystem(sender *hub.Client, text string) {
	data, err := json.Marshal(protocol.New(protocol.TypeSystem, "", text))
	if err != nil {
		s.log.Error("failed to marshal system message", "error", err)
		return
	}
	s.hub.Broadcast(sender, data)
}

func (s *Server) fail(conn *websocket.Conn, text string) {
	data, err := json.Marshal(protocol.New(protocol.TypeError, "", text))
	if err != nil {
		return
	}
	_ = conn.SetWriteDeadline(time.Now().Add(s.cfg.WriteTimeout))
	_ = conn.WriteMessage(websocket.TextMessage, data)
	_ = conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.ClosePolicyViolation, text))
}

type rateLimiter struct {
	windowStart time.Time
	count       int
}

func (l *rateLimiter) allow(now time.Time, limit int) bool {
	if l.windowStart.IsZero() || now.Sub(l.windowStart) >= time.Second {
		l.windowStart = now
		l.count = 0
	}
	l.count++
	return l.count <= limit
}
