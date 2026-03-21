package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
	"github.com/golang-jwt/jwt/v5"
)

type APIHandler struct {
	RDB *redis.Client
}

type AuthRequest struct {
	AndroidID string `json:"android_id" binding:"required"`
	Timestamp int64  `json:"timestamp" binding:"required"`
	Signature string `json:"signature" binding:"required"`
}

func (h *APIHandler) Auth(c *gin.Context) {
	var req AuthRequest

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request format"})
		return
	}

	now := time.Now().Unix()
	if now-req.Timestamp > 300 || now-req.Timestamp < -5 {
		fmt.Printf("Request timestamp out of range: %d (now: %d)\n", req.Timestamp, now)
		c.JSON(http.StatusUnauthorized, gin.H{"error": "request expired"})
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
		"exp":        time.Now().Add(time.Hour * 24 * 30).Unix(),
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

func (h *APIHandler) Legacy_handshake(c *gin.Context) {
	aid := c.GetHeader("X-Device-Id")

	if aid == "" {
		var req HandshakeRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(400, gin.H{"error": "invalid request"})
			return
		}
		aid = req.DeviceID
	}

	err := h.RDB.HSet(c, "a_id:"+aid, "token", aid, "is_premium", false, "c_at", time.Now().Format("2006-01-02")).Err()
	if err != nil {
		fmt.Printf("HSET error: %v\n", err)
		c.JSON(500, gin.H{"error": "redis error"})
		return
	}

	response := hsresp{
		Token: "a_id:" + aid,
	}
	c.JSON(200, response)
}

func (h *APIHandler) Legacy_getConfigs(c *gin.Context) {
	var cfs []Config

	token := "a_id:" + c.GetHeader("X-Device-Id")

	am_ips := strings.Split(os.Getenv("AMN_IPS"), ",")
	am_nms := strings.Split(os.Getenv("SNMS"), ",")

	for iter, am_ip := range am_ips {
		c_exist, _ := h.RDB.HExists(c, token, "am:"+am_ip).Result()

		if c_exist {
			cf_str, err := h.RDB.HGet(c, token, "am:"+am_ip).Result()
			if err != nil {
				fmt.Printf("Fail fetch cf str for am:%s", am_ip)
				continue
			}
			cfobj := Config{
				Name:   am_nms[iter],
				Config: cf_str,
			}
			cfs = append(cfs, cfobj)

			continue
		}

		req_url := "http://" + am_ip + ":8080"
		admin_panel := &http.Client{Timeout: 10 * time.Second}

		sr_req, _ := http.NewRequest("GET", req_url+"/api/servers", nil)
		sr_req.SetBasicAuth(os.Getenv("CONTROL_USNM"), os.Getenv("CONTROL_PASD"))
		sr_res, err := admin_panel.Do(sr_req)
		if err != nil {
			panic(fmt.Errorf("sreq send error: %v", err))
		}
		defer sr_res.Body.Close()

		var amsrs []S_con
		sid := ""
		sr_r_j, err := io.ReadAll(sr_res.Body)
		if err != nil {
			panic(fmt.Errorf("sreq read error: %v", err))
		}
		if err := json.Unmarshal(sr_r_j, &amsrs); err != nil {
			panic(fmt.Errorf("sreq parse error: %v", err))
		}
		if len(amsrs) > 0 {
			sid = amsrs[0].Sid
		}
		if sid == "" {
			panic("Empty sid")
		}

		payload := CrcsReq{
			Name:           token,
			ApplyISettings: true,
			ISettings: Is{
				I1: os.Getenv("I_GBG"),
			},
		}
		jsonData, err := json.Marshal(payload)
		if err != nil {
			fmt.Printf("Obj parse fail: %v\n", err)
			return
		}
		crcs_req, _ := http.NewRequest("POST", req_url+"/api/servers/"+sid+"/clients", bytes.NewBuffer(jsonData))
		crcs_req.Header.Set("Content-Type", "application/json")
		crcs_req.SetBasicAuth(os.Getenv("CONTROL_USNM"), os.Getenv("CONTROL_PASD"))
		crcsresp, err := admin_panel.Do(crcs_req)
		if err != nil {
			fmt.Printf("Client create req fail: %v\n", err)
			return
		}
		defer crcsresp.Body.Close()

		var cfrep CrcsResp
		cfres, err := io.ReadAll(crcsresp.Body)
		if err != nil {
			panic(fmt.Errorf("clid resp read error: %v", err))
		}
		if err := json.Unmarshal(cfres, &cfrep); err != nil {
			fmt.Println("clid resp parse fail:", err)
			return
		}
		h.RDB.HSet(c, token, "am:id:"+am_ip, cfrep.Client.Clid).Err()

		getcfgurl := req_url + "/api/servers/" + sid + "/clients/" + cfrep.Client.Clid + "/config-both"
		getcfg_req, _ := http.NewRequest("GET", getcfgurl, bytes.NewBuffer(jsonData))
		getcfg_req.SetBasicAuth(os.Getenv("CONTROL_USNM"), os.Getenv("CONTROL_PASD"))
		getcfg_resp, err := admin_panel.Do(getcfg_req)
		if err != nil {
			fmt.Printf("Client create req fail: %v\n", err)
			return
		}
		defer getcfg_resp.Body.Close()

		var concfg Getcfg
		getcfgbody, err := io.ReadAll(getcfg_resp.Body)
		if err != nil {
			panic(fmt.Errorf("conf resp read error: %v", err))
		}
		if err = json.Unmarshal(getcfgbody, &concfg); err != nil {
			fmt.Println("conf resp parse fail:", err)
			return
		}

		err = h.RDB.HSet(c, token, "am:"+am_ip, concfg.Clncfg).Err()
		if err != nil {
			cfobj := Config{
				Name:   am_nms[iter],
				Config: concfg.Clncfg,
			}
			cfs = append(cfs, cfobj)
		}
	}

	response := gin.H{
		"configs": cfs,
	}
	c.JSON(200, response)
}

func (h *APIHandler) Legacy_AuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		signature := c.GetHeader("X-Signature")
		timestamp := c.GetHeader("X-Timestamp")
		deviceId := c.GetHeader("X-Device-Id")

		if signature == "" || timestamp == "" {
			fmt.Println("Empty headers, skipping...")
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing headers"})
			return
		}

		dataToSign := timestamp + deviceId

		h := hmac.New(sha256.New, []byte(os.Getenv("SECRET_KEY")))
		h.Write([]byte(dataToSign))

		expectedSignature := hex.EncodeToString(h.Sum(nil))

		if !hmac.Equal([]byte(signature), []byte(expectedSignature)) {
			fmt.Printf("❌ Mismatch!\nData: %s\nGot: %s\nExp: %s\n",
				dataToSign, signature, expectedSignature)
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid signature"})
			return
		}

		c.Next()
	}
}
