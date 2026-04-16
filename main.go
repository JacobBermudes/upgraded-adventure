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
