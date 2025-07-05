package geodesist

import (
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
		baseURL:  fmt.Sprintf(routerAddr),
		password: password,
		client: &http.Client{
			Timeout: 10 * time.Second,
			Jar:     jar,
		},
	}, nil
}

func (c *AmpliFiClient) ensureLogin() error {
	// Check if we already have cookies
	u, _ := url.Parse(c.baseURL)
	if len(c.client.Jar.Cookies(u)) > 0 {
		return nil
	}

	// Get login page to retrieve CSRF token
	resp, err := c.client.Get(c.baseURL + "/login.php")
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

	resp, err = c.client.Post(
		c.baseURL+"/login.php",
		"application/x-www-form-urlencoded",
		strings.NewReader(form.Encode()),
	)
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

func (c *AmpliFiClient) getInfoToken() (string, error) {
	if c.infoToken != "" {
		return c.infoToken, nil
	}

	resp, err := c.client.Get(c.baseURL + "/info.php")
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

func (c *AmpliFiClient) GetMetrics() ([]byte, error) {
	// Ensure we're logged in
	if err := c.ensureLogin(); err != nil {
		c.resetAuth()
		return nil, fmt.Errorf("authentication failed: %w", err)
	}

	// Get info token
	token, err := c.getInfoToken()
	if err != nil {
		c.resetAuth()
		return nil, fmt.Errorf("failed to get info token: %w", err)
	}

	// Request full metrics
	form := url.Values{}
	form.Set("token", token)
	form.Set("do", "full")

	resp, err := c.client.Post(
		c.baseURL+"/info-async.php",
		"application/x-www-form-urlencoded",
		strings.NewReader(form.Encode()),
	)
	if err != nil {
		c.resetAuth()
		return nil, fmt.Errorf("failed to get metrics: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		c.resetAuth()
		return nil, fmt.Errorf("metrics request failed with status: %d", resp.StatusCode)
	}

	return io.ReadAll(resp.Body)
}

func (c *AmpliFiClient) resetAuth() {
	c.infoToken = ""
	// Clear cookies by creating a new jar
	jar, _ := cookiejar.New(nil)
	c.client.Jar = jar
}

// Test authentication by attempting to login
func (c *AmpliFiClient) TestAuth() error {
	return c.ensureLogin()
}
