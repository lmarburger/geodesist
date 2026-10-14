package geodesist

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"
	"time"
)

type AmpliFiClient struct {
	baseURL   string
	password  string
	client    *http.Client
	infoToken string
}

func NewAmpliFiClient(routerAddr, password string) (*AmpliFiClient, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create cookie jar: %w", err)
	}

	return &AmpliFiClient{
		baseURL:  routerAddr,
		password: password,
		client: &http.Client{
			Timeout: 10 * time.Second,
			Jar:     jar,
		},
	}, nil
}

func (c *AmpliFiClient) ensureLogin(ctx context.Context) error {
	// Check if we already have cookies
	u, _ := url.Parse(c.baseURL)
	if len(c.client.Jar.Cookies(u)) > 0 {
		return nil
	}

	// Get login page to retrieve CSRF token
	resp, err := c.get(ctx, "/login.php")
	if err != nil {
		return fmt.Errorf("failed to get login page: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read login page: %w", err)
	}

	// Extract CSRF token from HTML - using single quotes as seen in the HTML
	tokenRe := regexp.MustCompile(`<input[^>]+name='token'[^>]+value='([^']+)'`)
	matches := tokenRe.FindSubmatch(body)
	if len(matches) < 2 {
		// Fallback to more flexible pattern
		tokenRe = regexp.MustCompile(`<input[^>]*name=['"]token['"][^>]*value=['"]([^'"]+)['"]`)
		matches = tokenRe.FindSubmatch(body)
		if len(matches) < 2 {
			return fmt.Errorf("failed to find CSRF token in login page")
		}
	}
	csrfToken := string(matches[1])

	// Login with credentials
	form := url.Values{}
	form.Set("token", csrfToken)
	form.Set("password", c.password)

	resp, err = c.postForm(ctx, "/login.php", form)
	if err != nil {
		return fmt.Errorf("failed to login: %w", err)
	}
	defer resp.Body.Close()

	// AmpliFi might redirect after login, so accept 302 as well
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusFound {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("login failed with status %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

func (c *AmpliFiClient) getInfoToken(ctx context.Context) (string, error) {
	if c.infoToken != "" {
		return c.infoToken, nil
	}

	resp, err := c.get(ctx, "/info.php")
	if err != nil {
		return "", fmt.Errorf("failed to get info page: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read info page: %w", err)
	}

	// Extract token from JavaScript variable
	tokenRe := regexp.MustCompile(`var token='([0-9a-f]+)'`)
	matches := tokenRe.FindSubmatch(body)
	if len(matches) < 2 {
		return "", fmt.Errorf("failed to find token in info page")
	}

	c.infoToken = string(matches[1])
	return c.infoToken, nil
}

func (c *AmpliFiClient) GetMetrics(ctx context.Context) ([]byte, error) {
	// Ensure we're logged in
	if err := c.ensureLogin(ctx); err != nil {
		c.resetAuth()
		return nil, fmt.Errorf("authentication failed: %w", err)
	}

	// Get info token
	token, err := c.getInfoToken(ctx)
	if err != nil {
		c.resetAuth()
		return nil, fmt.Errorf("failed to get info token: %w", err)
	}

	// Request full metrics
	form := url.Values{}
	form.Set("token", token)
	form.Set("do", "full")

	resp, err := c.postForm(ctx, "/info-async.php", form)
	if err != nil {
		c.resetAuth()
		return nil, fmt.Errorf("failed to get metrics: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		c.resetAuth()
		return nil, fmt.Errorf("metrics request failed with status: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		c.resetAuth()
		return nil, fmt.Errorf("failed to read metrics: %w", err)
	}

	// An expired or invalid session gets HTTP 200 with an HTML page that
	// redirects to login.php, so the status alone does not reveal it. Without a
	// reset, the dead cookie keeps ensureLogin from ever logging in again.
	if !json.Valid(body) {
		c.resetAuth()
		return nil, fmt.Errorf("session rejected: router returned non-JSON metrics response")
	}

	return body, nil
}

func (c *AmpliFiClient) resetAuth() {
	c.infoToken = ""
	// Clear cookies by creating a new jar
	jar, _ := cookiejar.New(nil)
	c.client.Jar = jar
}

func (c *AmpliFiClient) get(ctx context.Context, path string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return nil, err
	}
	return c.client.Do(req)
}

func (c *AmpliFiClient) postForm(ctx context.Context, path string, form url.Values) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return c.client.Do(req)
}
