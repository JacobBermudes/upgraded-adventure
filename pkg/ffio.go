package fixedfloat

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type Client struct {
	Key        string
	Secret     string
	BaseURL    string
	httpClient *http.Client
}

type FFResponse struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
}

type CreateOrderResponse struct {
	FFResponse
	Data struct {
		ID    string `json:"id"`
		Token string `json:"token"`
		From  struct {
			Address string `json:"address"`
		} `json:"from"`
	} `json:"data"`
}

type OrderStatusResponse struct {
	FFResponse
	Data struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	} `json:"data"`
}

func NewClient(key, secret string) *Client {
	return &Client{
		Key:     key,
		Secret:  secret,
		BaseURL: "https://ff.io/api/v2",
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

func (c *Client) call(method string, payload interface{}) ([]byte, error) {
	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal payload: %w", err)
	}

	h := hmac.New(sha256.New, []byte(c.Secret))
	h.Write(jsonPayload)
	signature := hex.EncodeToString(h.Sum(nil))

	reqURL := fmt.Sprintf("%s/%s", c.BaseURL, method)
	req, err := http.NewRequest("POST", reqURL, bytes.NewBuffer(jsonPayload))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-KEY", c.Key)
	req.Header.Set("X-API-SIGN", signature)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("API error (status %d): %s", resp.StatusCode, string(body))
	}

	return body, nil
}

func (c *Client) CreateOrder(params map[string]interface{}) (*CreateOrderResponse, error) {
	res, err := c.call("create", params)
	if err != nil {
		return nil, err
	}

	var data CreateOrderResponse
	if err := json.Unmarshal(res, &data); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	if data.Code != 0 {
		return nil, fmt.Errorf("API error: %s", data.Msg)
	}

	return &data, nil
}

func (c *Client) GetOrder(id, token string) (*OrderStatusResponse, error) {
	payload := map[string]interface{}{
		"id":    id,
		"token": token,
	}
	res, err := c.call("order", payload)
	if err != nil {
		return nil, err
	}

	var data OrderStatusResponse
	if err := json.Unmarshal(res, &data); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	if data.Code != 0 {
		return nil, fmt.Errorf("API error: %s", data.Msg)
	}

	return &data, nil
}
