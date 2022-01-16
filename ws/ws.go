package ws

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
	"golang.org/x/time/rate"
	"nhooyr.io/websocket"
)

type Subscriber struct {
	Id        string
	Message   chan []byte
	closeSlow func()
}

type ListSubscribers struct {
	subscriberMessageBuffer int
	publishLimiter          *rate.Limiter
	subscribersMu           sync.Mutex
	subscribers             map[*Subscriber]struct{}
}

var ListSubs *ListSubscribers

// addSubscriber registers a subscriber.
func (as *ListSubscribers) addSubscriber(s *Subscriber) {
	as.subscribersMu.Lock()
	as.subscribers[s] = struct{}{}
	as.subscribersMu.Unlock()
}

// deleteSubscriber deletes the given subscriber.
func (as *ListSubscribers) deleteSubscriber(s *Subscriber) {
	as.subscribersMu.Lock()
	delete(as.subscribers, s)
	as.subscribersMu.Unlock()
}

type Handler struct {
}

func WsHandler(engine *gin.Engine) {
	handler := &Handler{}
	ListSubs = &ListSubscribers{
		subscriberMessageBuffer: 16,
		subscribers:             make(map[*Subscriber]struct{}),
		publishLimiter:          rate.NewLimiter(rate.Every(time.Millisecond*100), 8),
	}
	Group := engine.Group("ws")
	{
		Group.GET(":id", handler.SubscribeHandler)
		Group.POST("", handler.PublishToAll)
		Group.POST(":id", handler.PublishToOne)
	}
}

func (h *Handler) SubscribeHandler(c *gin.Context) {
	wsConn, err := websocket.Accept(c.Writer, c.Request, nil)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, map[string]interface{}{
			"message": "subscribe error",
		})
		return
	}
	defer wsConn.Close(websocket.StatusInternalError, "")

	err = h.subscribe(c, wsConn)
	if errors.Is(err, context.Canceled) {
		return
	}
	if websocket.CloseStatus(err) == websocket.StatusNormalClosure ||
		websocket.CloseStatus(err) == websocket.StatusGoingAway {
		return
	}
	if err != nil {
		log.Error(err.Error())
		return
	}
}

func (h *Handler) subscribe(c *gin.Context, wsConn *websocket.Conn) error {
	ctx := wsConn.CloseRead(c)
	agentId := c.Param("id")
	if len(agentId) < 1 {
		return errors.New("id is missing")
	}
	s := &Subscriber{
		Id:      agentId,
		Message: make(chan []byte, ListSubs.subscriberMessageBuffer),
		closeSlow: func() {
			wsConn.Close(websocket.StatusPolicyViolation, "connection too slow to keep up with messages")
		},
	}
	ListSubs.addSubscriber(s)
	defer ListSubs.deleteSubscriber(s)

	for {
		select {
		case msg := <-s.Message:
			err := writeTimeout(ctx, time.Second*5, wsConn, msg)
			if err != nil {
				return err
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func writeTimeout(ctx context.Context, timeout time.Duration, c *websocket.Conn, msg []byte) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	return c.Write(ctx, websocket.MessageText, msg)
}

func (h *Handler) PublishToAll(c *gin.Context) {
	jsonBody := make(map[string]interface{})
	if err := c.BindJSON(&jsonBody); err != nil {
		c.JSON(http.StatusServiceUnavailable, map[string]interface{}{
			"message": "publish error",
		})
		return
	}
	message, _ := jsonBody["message"].(string)
	h.publish([]byte(message))
	c.Writer.WriteHeader(http.StatusAccepted)
}

func (h *Handler) PublishToOne(c *gin.Context) {
	jsonBody := make(map[string]interface{})
	if err := c.BindJSON(&jsonBody); err != nil {
		c.JSON(http.StatusServiceUnavailable, map[string]interface{}{
			"message": "publish error",
		})
		return
	}
	agentId := c.Param("id")
	if len(agentId) < 1 {
		c.JSON(http.StatusServiceUnavailable, map[string]interface{}{
			"message": "publish id error",
		})
		return
	}
	message, _ := jsonBody["message"].(string)
	h.publishOne(agentId, []byte(message))
	c.Writer.WriteHeader(http.StatusAccepted)
}

func (h *Handler) publish(msg []byte) {
	ListSubs.subscribersMu.Lock()
	defer ListSubs.subscribersMu.Unlock()
	ListSubs.publishLimiter.Wait(context.Background())
	for s := range ListSubs.subscribers {
		select {
		case s.Message <- msg:
		default:
			go s.closeSlow()
		}
	}
}

func (h *Handler) publishOne(id string, msg []byte) {
	ListSubs.subscribersMu.Lock()
	defer ListSubs.subscribersMu.Unlock()
	ListSubs.publishLimiter.Wait(context.Background())
	for s := range ListSubs.subscribers {
		if s.Id == id {
			select {
			case s.Message <- msg:
			default:
				go s.closeSlow()
			}
		}
	}
}
