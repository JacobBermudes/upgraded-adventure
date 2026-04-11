package main

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	fixedfloat "surfboost/pkg"
	"time"

	"github.com/gin-gonic/gin"
	_ "github.com/lib/pq"
)

func initDB() *sql.DB {

	psql_user := os.Getenv("PSQL_USER")
	psql_pass := os.Getenv("PSQL_PASS")
	psql_db := os.Getenv("PSQL_DB")

	dsn := fmt.Sprintf("host=db port=5432 user=%s password=%s dbname=%s sslmode=disable",
		psql_user, psql_pass, psql_db)

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

	createUsersTable := `
	CREATE TABLE IF NOT EXISTS users (
		android_id VARCHAR(255) PRIMARY KEY,
		is_premium BOOLEAN DEFAULT FALSE,
		balance INTEGER DEFAULT 0,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		prem_finish TIMESTAMP
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

	createPaymentTable := `
	CREATE TABLE IF NOT EXISTS user_payments (
		order_id VARCHAR(255) PRIMARY KEY,
		android_id VARCHAR(255),
		token VARCHAR(255) NOT NULL,
		type VARCHAR(50) DEFAULT 'RUB',
		amount DECIMAL(10, 2) NOT NULL,
		status VARCHAR(50) DEFAULT 'NEW',
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	);`
	_, err = db.Exec(createPaymentTable)
	if err != nil {
		log.Fatalf("Fail to create payment db: %v", err)
	}

	return db
}

func main() {
	r := gin.Default()

	ffClient := fixedfloat.NewClient(
		os.Getenv("FF_API_KEY"),
		os.Getenv("FF_API_SECRET"),
	)

	api := &APIHandler{
		DB:       initDB(),
		FFClient: ffClient,
	}

	v1 := r.Group("/v1")
	{
		v1.POST("/auth", api.Auth)

		secG := v1.Group("/")
		secG.Use(api.AuthMiddleware())
		{
			secG.GET("/data", api.GetData)
			secG.GET("/servers", api.GetServers)
			secG.GET("/config", api.GetConfig)

			secG.POST("/cryptopay", api.CryptoPay)
			secG.GET("/cryptopay/status", api.CryptoPayStatus)
		}
	}

	r.Run(":9090")
}
