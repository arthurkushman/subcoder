// Package remote is a small client for the SubCoder HTTP server, used by the
// attach command.
package remote

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/arthurkushman/subcoder/internal/session"
)

type Client struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

func New(baseURL string) *Client {
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		Token:   os.Getenv("SUBCODER_SERVER_TOKEN"),
		HTTP:    http.DefaultClient,
	}
}

func (c *Client) request(ctx context.Context, method, path string, body any) (*http.Response, error) {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, reader)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		defer resp.Body.Close()
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("server: %s: %s", resp.Status, strings.TrimSpace(string(data)))
	}
	return resp, nil
}

func (c *Client) Health(ctx context.Context) error {
	resp, err := c.request(ctx, http.MethodGet, "/health", nil)
	if err != nil {
		return err
	}
	return resp.Body.Close()
}

func (c *Client) Sessions(ctx context.Context) ([]session.Summary, error) {
	resp, err := c.request(ctx, http.MethodGet, "/sessions", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var payload struct {
		Sessions []session.Summary `json:"sessions"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}
	return payload.Sessions, nil
}

func (c *Client) CreateSession(ctx context.Context, directory string) (*session.Session, error) {
	resp, err := c.request(ctx, http.MethodPost, "/sessions", map[string]any{"directory": directory})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	sess := &session.Session{}
	if err := json.NewDecoder(resp.Body).Decode(sess); err != nil {
		return nil, err
	}
	return sess, nil
}

func (c *Client) LoadSession(ctx context.Context, id string) (*session.Session, error) {
	resp, err := c.request(ctx, http.MethodGet, "/sessions/"+id, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	sess := &session.Session{}
	if err := json.NewDecoder(resp.Body).Decode(sess); err != nil {
		return nil, err
	}
	return sess, nil
}

func (c *Client) Interrupt(ctx context.Context, id string) error {
	resp, err := c.request(ctx, http.MethodPost, "/sessions/"+id+"/interrupt", map[string]any{})
	if err != nil {
		return err
	}
	return resp.Body.Close()
}

func (c *Client) Compact(ctx context.Context, id string) error {
	resp, err := c.request(ctx, http.MethodPost, "/sessions/"+id+"/compact", map[string]any{})
	if err != nil {
		return err
	}
	return resp.Body.Close()
}

func (c *Client) Reply(ctx context.Context, sessionID, requestID, reply string) error {
	resp, err := c.request(ctx, http.MethodPost, "/sessions/"+sessionID+"/permissions/"+requestID, map[string]any{"reply": reply})
	if err != nil {
		return err
	}
	return resp.Body.Close()
}

// Send runs a prompt on the server and streams session events until the
// stream closes or ctx is canceled.
func (c *Client) Send(ctx context.Context, sessionID, text string) (<-chan session.Event, <-chan error) {
	events := make(chan session.Event, 64)
	errs := make(chan error, 1)

	go func() {
		defer close(events)
		defer close(errs)

		resp, err := c.request(ctx, http.MethodPost, "/sessions/"+sessionID+"/messages", map[string]any{"text": text})
		if err != nil {
			errs <- err
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusNoContent || resp.Body == nil {
			return
		}

		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 32*1024), 8<<20)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if data == "" {
				continue
			}
			var event session.Event
			if err := json.Unmarshal([]byte(data), &event); err != nil {
				continue
			}
			select {
			case events <- event:
			case <-ctx.Done():
				errs <- ctx.Err()
				return
			}
		}
		if err := scanner.Err(); err != nil && !errors.Is(err, context.Canceled) {
			errs <- err
		}
	}()

	return events, errs
}
