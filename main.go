package main

import (
	"context"
	"log"
	"os"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
)

func initRedis() *redis.Client {
    client := redis.NewClient(&redis.Options{
        Addr:     "redis:6379",
        DB:       0,
        Password: os.Getenv("REDIS_PASS"),
    })

    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()

    if err := client.Ping(ctx).Err(); err != nil {
        log.Fatalf("Could not connect to Redis: %v", err)
    }

    return client
}

func main() {
	r := gin.Default()

	api := &APIHandler{
		RDB: initRedis(),
	}

	//	LEGACY
	r.POST("/handshake", api.Legacy_AuthMiddleware(), api.Legacy_handshake)
	r.GET("/servers", api.Legacy_AuthMiddleware(), api.Legacy_getConfigs)
	//  LEGACY FINISH

    v1 := r.Group("/v1")
    {
        v1.POST("/auth", api.Auth)
    }

	r.Run(":9090")
}
