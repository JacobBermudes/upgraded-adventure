package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"

	"github.com/gin-gonic/gin"
)

type CryptoPayRequest struct {
	FromCcy string  `json:"from_ccy" binding:"required"`
	Amount  float64 `json:"amount" binding:"required"`
}

type CMCResponse struct {
	Data []struct {
		Quote map[string]struct {
			Price float64 `json:"price"`
		} `json:"quote"`
	} `json:"data"`
}

func (h *APIHandler) CryptoPay(c *gin.Context) {
	androidIDValue, exists := c.Get("android_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	var req CryptoPayRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body"})
		return
	}

	convertedAmount := cryptoAmount(req.Amount, req.FromCcy)
	if convertedAmount == 0 {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch crypto rates"})
		return
	}

	params := map[string]interface{}{
		"fromCcy":   req.FromCcy,
		"toCcy":     "TRX",
		"amount":    convertedAmount,
		"direction": "from",
		"type":      "float",
		"toAddress": os.Getenv("CRYPTOWALLET"),
	}

	resp, err := h.FFClient.CreateOrder(params)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}

	insertQuery := `INSERT INTO user_payments (android_id, order_id, amount, token, type) 
    VALUES ($1, $2, $3, $4, $5)`

	_, err = h.DB.ExecContext(c.Request.Context(), insertQuery,
		androidIDValue.(string),
		resp.Data.ID,
		req.Amount,
		resp.Data.Token,
		"ffio",
	)

	if err != nil {
		fmt.Printf("DB error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Internal database error"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"pay_address": resp.Data.Address,
	})
}

func (h *APIHandler) CryptoPayStatus(c *gin.Context) {
	androidIDValue, exists := c.Get("android_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}
	androidID := androidIDValue.(string)

	paymentsQuery := `SELECT order_id, token, amount FROM user_payments 
	WHERE android_id = $1 and status = 'NEW' and type = 'ffio'`
	rows, err := h.DB.QueryContext(c.Request.Context(), paymentsQuery, androidID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Database error"})
		return
	}
	defer rows.Close()

	hasUpdates := false

	for rows.Next() {
		var orderID, orderToken string
		var amount float64
		if err := rows.Scan(&orderID, &orderToken, &amount); err != nil {
			continue
		}

		resp, err := h.FFClient.GetOrder(orderID, orderToken)
		if err != nil {
			continue
		}

		switch resp.Data.Status {
		case "DONE":
			updateUserQuery := `UPDATE users SET balance = balance + $1 WHERE android_id = $2`
			_, dbErr := h.DB.ExecContext(c.Request.Context(), updateUserQuery, amount, androidID)

			if dbErr == nil {
				markDoneQuery := `UPDATE user_payments SET status = 'DONE' WHERE order_id = $1`
				h.DB.ExecContext(c.Request.Context(), markDoneQuery, orderID)
				fmt.Printf("Balance topuped for %s by %f", androidID, amount)
				hasUpdates = true
			} else {
				fmt.Printf("❌ Fail to top up: %v\n", dbErr)
			}

		case "EXPIRED", "EMERGENCY":
			markFailQuery := `UPDATE user_payments SET status = $1 WHERE order_id = $2`
			h.DB.ExecContext(c.Request.Context(), markFailQuery, resp.Data.Status, orderID)
		}
	}

	if hasUpdates {
		c.JSON(http.StatusCreated, gin.H{"status": "updated", "message": "Balance was topped up"}) // 201
	} else {
		c.JSON(http.StatusOK, gin.H{"status": "no_changes", "message": "No new payments completed"}) // 200
	}
}

func cryptoAmount(amount float64, fromCcy string) float64 {
	u, _ := url.Parse("https://pro-api.coinmarketcap.com/v2/tools/price-conversion")
	q := u.Query()
	q.Set("amount", fmt.Sprintf("%f", amount))
	q.Set("symbol", "RUB")
	q.Set("convert", fromCcy)
	u.RawQuery = q.Encode()

	fmt.Printf("Req CMC: %s\n", u.String())

	req, err := http.NewRequest("GET", u.String(), nil)
	if err != nil {
		fmt.Printf("❌ Error http.NewRequest: %v\n", err)
		return 0
	}

	apiKey := os.Getenv("X-CMC_PRO_API_KEY")
	if apiKey == "" {
		fmt.Println("X-CMC_PRO_API_KEY empty!")
	}

	req.Header.Add("X-CMC_PRO_API_KEY", apiKey)
	req.Header.Add("Accept", "application/json")

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Printf("Network error: %v\n", err)
		return 0
	}
	defer res.Body.Close()

	body, _ := io.ReadAll(res.Body)

	fmt.Printf("Resp CMC (Status %d): %s\n", res.StatusCode, string(body))

	if res.StatusCode != 200 {
		return 0
	}

	var response CMCResponse
	if err := json.Unmarshal(body, &response); err != nil {
		fmt.Printf("Error unmarshal JSON: %v\n", err)
		return 0
	}

	if len(response.Data) == 0 {
		fmt.Println("Data empty")
		return 0
	}

	cryptoData, exists := response.Data[0].Quote[fromCcy]
	if !exists {
		fmt.Printf("Coin not found! %s\n", fromCcy)
		return 0
	}

	finalAmount := cryptoData.Price * 1.05
	fmt.Printf("✅ Done: %f RUB = %f %s (+ 5%%)\n", amount, finalAmount, fromCcy)

	return finalAmount
}
