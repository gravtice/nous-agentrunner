package runnerd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

type guestClient struct {
	baseURL string
	http    *http.Client
}

type guestHTTPError struct {
	Path   string
	Status int
	Body   string
}

func (e *guestHTTPError) Error() string {
	return fmt.Sprintf("guest %s status %d: %s", e.Path, e.Status, e.Body)
}

func (c *guestClient) health(ctx context.Context) error {
	req, _ := http.NewRequestWithContext(ctx, "GET", c.baseURL+"/health", nil)
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("guest health status %d", resp.StatusCode)
	}
	return nil
}

func (c *guestClient) postJSON(ctx context.Context, path string, in any, out any) error {
	b, err := json.Marshal(in)
	if err != nil {
		return err
	}
	req, _ := http.NewRequestWithContext(ctx, "POST", c.baseURL+path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return &guestHTTPError{Path: path, Status: resp.StatusCode, Body: string(body)}
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(body, out)
}

func (c *guestClient) getJSON(ctx context.Context, path string, out any) error {
	req, _ := http.NewRequestWithContext(ctx, "GET", c.baseURL+path, nil)
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return &guestHTTPError{Path: path, Status: resp.StatusCode, Body: string(body)}
	}
	return json.Unmarshal(body, out)
}

func (c *guestClient) delete(ctx context.Context, path string) error {
	req, _ := http.NewRequestWithContext(ctx, "DELETE", c.baseURL+path, nil)
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return &guestHTTPError{Path: path, Status: resp.StatusCode, Body: string(body)}
	}
	return nil
}
