package main

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/luandnh/go-websocket-demo/ws"
)

func main() {
	engine := gin.New()
	engine.Use(gin.Recovery())
	engine.GET("/", func(c *gin.Context) {
		c.JSON(http.StatusOK, map[string]interface{}{
			"service": "Go Websocket Demo",
			"version": "v1.0",
			"time":    time.Now().Unix(),
		})
	})
	ws.WsHandler(engine)
	engine.Run()
}
