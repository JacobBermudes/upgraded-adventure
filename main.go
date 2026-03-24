package main

import (
	"context"
	"database/sql"
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

func initDB() *sql.DB {

	psql_user := os.Getenv("PSQL_USER")
	psql_pass := os.Getenv("PSQL_PASS")
	psql_db := os.Getenv("PSQL_DB")

	dsn := "host=localhost port=5432 user=" + psql_user + " password=" + psql_pass + " dbname=" + psql_db + " sslmode=disable"

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		log.Fatalf("Driver init fail %v", err)
	}

	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)

	if err := db.Ping(); err != nil {
		log.Fatalf("FAIL TO CONNECT TO DB: %v", err)
	}
	log.Println("psql connected successfully")

	createConfigsTable := `
	CREATE TABLE IF NOT EXISTS user_configs (
		android_id VARCHAR(255) REFERENCES users(android_id) ON DELETE CASCADE,
		server_id VARCHAR(50) REFERENCES servers(id) ON DELETE CASCADE,
		config_text TEXT NOT NULL,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		PRIMARY KEY (android_id, server_id)
	);`
	_, err = db.Exec(createConfigsTable)
	if err != nil {
		log.Fatalf("Fail to create config db: %v", err)
	}

	createUsersTable := `
	CREATE TABLE IF NOT EXISTS users (
		android_id VARCHAR(255) PRIMARY KEY,
		is_premium BOOLEAN DEFAULT FALSE,
		balance INTEGER DEFAULT 0,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	);`
	if _, err = db.Exec(createUsersTable); err != nil {
		log.Fatalf("Fail to create users db: %v", err)
	}

	createServersTable := `
	CREATE TABLE IF NOT EXISTS servers (
		id VARCHAR(50) PRIMARY KEY,
		name VARCHAR(100) NOT NULL,
		ip VARCHAR(50) NOT NULL,
		login VARCHAR(100) NOT NULL,
		password VARCHAR(255) NOT NULL,
		is_premium BOOLEAN DEFAULT FALSE
	);`
	if _, err = db.Exec(createServersTable); err != nil {
		log.Fatalf("Fail to create servers db: %v", err)
	}

	return db
}

func main() {
	r := gin.Default()

	api := &APIHandler{
		RDB: initRedis(),
		DB:  initDB(),
	}

	//	LEGACY
	r.POST("/handshake", api.Legacy_AuthMiddleware(), api.Legacy_handshake)
	r.GET("/servers", api.Legacy_AuthMiddleware(), api.Legacy_getConfigs)
	//  LEGACY FINISH

	v1 := r.Group("/v1")
	{
		v1.POST("/auth", api.Auth)

		secG := v1.Group("/")
		secG.Use(api.AuthMiddleware())
		{
			secG.GET("/servers", api.GetServers)
			secG.GET("/config", api.GetConfig)
		}
	}

	r.Run(":9090")
}
