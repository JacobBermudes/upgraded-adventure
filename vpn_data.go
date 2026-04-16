package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"

	fixedfloat "surfboost/pkg"
)

type APIHandler struct {
	DB       *sql.DB
	FFClient *fixedfloat.Client
}

type AuthRequest struct {
	AndroidID string `json:"android_id" binding:"required"`
	Timestamp int64  `json:"timestamp" binding:"required"`
	Signature string `json:"signature" binding:"required"`
}

type ServerListResponse struct {
	ID         string `json:"ID"`
	ServerName string `json:"Name"`
}

type ConnectConfig struct {
	Clncfg string `json:"clean_config"`
}

type AvailableServers struct {
	Sid string `json:"id"`
}

type ClientCreateRequest struct {
	Name           string `json:"name"`
	ApplyISettings bool   `json:"apply_i_settings"`
	ISettings      struct {
		I1 string `json:"i1"`
	} `json:"i_settings"`
}

type CrcsResp struct {
	Client Clops `json:"client"`
}
type Clops struct {
	Clid string `json:"id"`
}

type CreatedClient struct {
	Client struct {
		Id string `json:"id"`
	} `json:"client"`
}

func (h *APIHandler) Auth(c *gin.Context) {
	var req AuthRequest

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request format"})
		return
	}

	now := time.Now().Unix()
	diff := float64(now - req.Timestamp)

	if math.Abs(diff) > 300 {
		fmt.Printf("Request timestamp out of range. Server: %d, Client: %d, Diff: %.0f\n", now, req.Timestamp, diff)
		c.JSON(http.StatusUnauthorized, gin.H{"error": "time out of sync"})
		return
	}

	apiSecret := os.Getenv("API_SECRET")
	if apiSecret == "" {
		apiSecret = "im_fool"
	}

	message := fmt.Sprintf("%s.%d", req.AndroidID, req.Timestamp)

	mac := hmac.New(sha256.New, []byte(apiSecret))
	mac.Write([]byte(message))
	expectedSignature := hex.EncodeToString(mac.Sum(nil))

	if !hmac.Equal([]byte(req.Signature), []byte(expectedSignature)) {
		fmt.Printf("❌ Signature mismatch!\nMessage: %s\nGot: %s\nExpected: %s\n",
			message, req.Signature, expectedSignature)
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid signature"})
		return
	}

	jwtSecret := os.Getenv("JWT_SECRET")
	if jwtSecret == "" {
		jwtSecret = "extra_fool"
	}

	claims := jwt.MapClaims{
		"android_id": req.AndroidID,
		"exp":        time.Now().Add(time.Hour * 24).Unix(),
		"iat":        time.Now().Unix(),
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, err := token.SignedString([]byte(jwtSecret))

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate token"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"token": tokenString,
	})
}

func (h *APIHandler) GetData(c *gin.Context) {
	androidIDValue, _ := c.Get("android_id")
	androidID, _ := androidIDValue.(string)

	var balance float64
	var premFinish sql.NullTime

	query := `SELECT balance, prem_finish FROM users WHERE android_id = $1`

	err := h.DB.QueryRowContext(c.Request.Context(), query, androidID).Scan(&balance, &premFinish)
	if err != nil {
		if err == sql.ErrNoRows {
			// Create new user with default values
			insertQuery := `INSERT INTO users (android_id) VALUES ($1)`
			if _, err := h.DB.ExecContext(c.Request.Context(), insertQuery, androidID); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Database error creating user"})
				return
			}
			c.JSON(http.StatusCreated, gin.H{
				"Balance":   0,
				"IsPremium": false,
			})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "database error fetching user data"})
		return
	}

	isPremium := premFinish.Valid && premFinish.Time.After(time.Now())

	c.JSON(http.StatusOK, gin.H{
		"Balance":   balance,
		"IsPremium": isPremium,
	})
}

func (h *APIHandler) GetServers(c *gin.Context) {
	androidID, _ := c.Get("android_id")

	fmt.Printf("Android %v requests server list\n", androidID)
	ctx := c.Request.Context()

	var isPremium bool
	userQuery := `SELECT is_premium FROM users WHERE android_id = $1`

	err := h.DB.QueryRowContext(ctx, userQuery, androidID).Scan(&isPremium)
	if err == sql.ErrNoRows {
		isPremium = false
	} else if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Database error fetching user"})
		return
	}

	var serverQuery string
	if isPremium {
		serverQuery = `SELECT id, name FROM servers`
	} else {
		serverQuery = `SELECT id, name FROM servers WHERE is_premium = false`
	}

	rows, err := h.DB.QueryContext(ctx, serverQuery)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch servers"})
		return
	}
	defer rows.Close()

	var servers []ServerListResponse

	for rows.Next() {
		var srv ServerListResponse
		if err := rows.Scan(&srv.ID, &srv.ServerName); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Error reading server data"})
			return
		}
		servers = append(servers, srv)
	}

	if err := rows.Err(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Database error during iteration"})
		return
	}

	if len(servers) == 0 {
		c.JSON(http.StatusOK, []ServerListResponse{})
		return
	}

	c.JSON(http.StatusOK, servers)
}

func (h *APIHandler) GetConfig(c *gin.Context) {
	server_id := c.Query("server")
	if server_id == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "server parameter is required"})
		return
	}

	androidIDValue, _ := c.Get("android_id")
	androidID, _ := androidIDValue.(string)

	var config string

	configQuery := `SELECT config_text FROM user_configs WHERE android_id = $1 AND server_id = $2`
	err := h.DB.QueryRowContext(c.Request.Context(), configQuery, androidID, server_id).Scan(&config)

	switch err {
	case nil:
		c.JSON(http.StatusOK, gin.H{"config": config})
		return
	case sql.ErrNoRows:
		var ip, login, pass string
		ipQuery := `SELECT ip, login, password FROM servers WHERE id = $1`

		err := h.DB.QueryRowContext(c.Request.Context(), ipQuery, server_id).Scan(&ip, &login, &pass)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Database error fetching ip"})
			return
		}

		req_url := "http://" + ip + ":8080"
		admin_panel := &http.Client{Timeout: 10 * time.Second}

		sr_req, _ := http.NewRequest("GET", req_url+"/api/servers", nil)
		sr_req.SetBasicAuth(login, pass)
		sr_res, err := admin_panel.Do(sr_req)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to connect to server"})
			return
		}
		defer sr_res.Body.Close()

		var availableServers []AvailableServers
		sid := ""
		sr_r_j, err := io.ReadAll(sr_res.Body)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to read server response"})
			return
		}
		if err := json.Unmarshal(sr_r_j, &availableServers); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to parse server response"})
			return
		}
		if len(availableServers) > 0 {
			sid = availableServers[0].Sid
		}

		payload := ClientCreateRequest{
			Name:           androidID,
			ApplyISettings: os.Getenv("I_GBG") != "",
			ISettings: struct {
				I1 string `json:"i1"`
			}{
				I1: os.Getenv("I_GBG"),
			},
		}
		jsonData, err := json.Marshal(payload)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to prepare request"})
			return
		}
		createClientReq, _ := http.NewRequest("POST", req_url+"/api/servers/"+sid+"/clients", bytes.NewBuffer(jsonData))
		createClientReq.Header.Set("Content-Type", "application/json")
		createClientReq.SetBasicAuth(login, pass)
		createClientResp, err := admin_panel.Do(createClientReq)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create client on server"})
			return
		}
		defer createClientResp.Body.Close()

		var createdClient CreatedClient
		createRespBody, err := io.ReadAll(createClientResp.Body)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to read client response"})
			return
		}
		if err := json.Unmarshal(createRespBody, &createdClient); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to parse client response"})
			return
		}

		getcfgurl := req_url + "/api/servers/" + sid + "/clients/" + createdClient.Client.Id + "/config-both"
		getcfg_req, _ := http.NewRequest("GET", getcfgurl, bytes.NewBuffer(jsonData))
		getcfg_req.SetBasicAuth(login, pass)
		getcfg_resp, err := admin_panel.Do(getcfg_req)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch client config"})
			return
		}
		defer getcfg_resp.Body.Close()

		var concfg ConnectConfig
		getcfgbody, err := io.ReadAll(getcfg_resp.Body)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to read client config"})
			return
		}
		if err = json.Unmarshal(getcfgbody, &concfg); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to parse client config"})
			return
		}

		insertQuery := `
			INSERT INTO user_configs (android_id, server_id, config_text)
			VALUES ($1, $2, $3)
			ON CONFLICT (android_id, server_id) 
			DO UPDATE SET 
				config_text = EXCLUDED.config_text, 
				created_at = CURRENT_TIMESTAMP;
		`

		_, err = h.DB.ExecContext(c.Request.Context(), insertQuery, androidID, server_id, concfg.Clncfg)

		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save generated config"})
			return
		}

		c.JSON(http.StatusOK, gin.H{"config": concfg.Clncfg})
	}
}
