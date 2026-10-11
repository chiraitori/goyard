package discord

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type RESTClient struct {
	token      string
	baseURL    string
	httpClient *http.Client
}

func NewRESTClient(token string) *RESTClient {
	return &RESTClient{
		token:   token,
		baseURL: "https://discord.com/api/v9",
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

func (c *RESTClient) SetBaseURL(u string) {
	c.baseURL = strings.TrimRight(u, "/")
}

func (c *RESTClient) SetHTTPClient(client *http.Client) {
	c.httpClient = client
}

// EmbedField represents a Discord embed field
type EmbedField struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Inline bool   `json:"inline"`
}

// EmbedFooter represents a Discord embed footer
type EmbedFooter struct {
	Text string `json:"text"`
}

// Embed represents a Discord embed
type Embed struct {
	Title       string       `json:"title,omitempty"`
	Description string       `json:"description,omitempty"`
	Color       int          `json:"color,omitempty"`
	Footer      *EmbedFooter `json:"footer,omitempty"`
	Fields      []EmbedField `json:"fields,omitempty"`
}

// SendMessage sends content or embeds to channelID with up to 2 retries on 429
func (c *RESTClient) SendMessage(ctx context.Context, channelID string, content string, embed *Embed) error {
	payload := make(map[string]any)
	if content != "" {
		payload["content"] = content
	}
	if embed != nil {
		payload["embeds"] = []any{embed}
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	url := fmt.Sprintf("%s/channels/%s/messages", c.baseURL, channelID)
	return c.doWithRetry(ctx, http.MethodPost, url, body, nil)
}

// CreateDM creates or gets a DM channel with recipientID
func (c *RESTClient) CreateDM(ctx context.Context, recipientID string) (string, error) {
	payload := map[string]any{
		"recipient_id": recipientID,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	url := fmt.Sprintf("%s/users/@me/channels", c.baseURL)
	var respData struct {
		ID string `json:"id"`
	}
	err = c.doWithRetry(ctx, http.MethodPost, url, body, &respData)
	if err != nil {
		return "", err
	}
	return respData.ID, nil
}

func (c *RESTClient) doWithRetry(ctx context.Context, method, url string, body []byte, out any) error {
	retries := 0
	for {
		req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bot "+c.token)
		req.Header.Set("Content-Type", "application/json")

		resp, err := c.httpClient.Do(req)
		if err != nil {
			return err
		}

		if resp.StatusCode == http.StatusTooManyRequests && retries < 2 {
			retries++
			retryAfterSec := 1.0
			if retryHeader := resp.Header.Get("Retry-After"); retryHeader != "" {
				if v, err := strconv.ParseFloat(retryHeader, 64); err == nil && v > 0 {
					retryAfterSec = v
				}
			}
			resp.Body.Close()
			select {
			case <-time.After(time.Duration(retryAfterSec * float64(time.Second))):
				continue
			case <-ctx.Done():
				return ctx.Err()
			}
		}

		defer resp.Body.Close()
		if resp.StatusCode >= 400 {
			respBytes, _ := io.ReadAll(resp.Body)
			return fmt.Errorf("discord REST error status %d: %s", resp.StatusCode, string(respBytes))
		}

		if out != nil {
			return json.NewDecoder(resp.Body).Decode(out)
		}
		return nil
	}
}
