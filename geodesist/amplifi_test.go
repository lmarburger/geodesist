package geodesist

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

const loginRedirectPage = `<!DOCTYPE html>
<html lang='en'>
<head>
<title>Welcome to AmpliFi</title>
<meta http-equiv="refresh" content="0;URL='login.php'" />
</head>
</html>`

// fakeRouter mimics the AmpliFi web UI: a session cookie is issued by the
// login page, becomes valid after a correct password POST, and any request
// with an invalid session gets HTTP 200 with a redirect page.
type fakeRouter struct {
	mu       sync.Mutex
	password string
	next     int
	valid    map[string]bool
	logins   int
	renamed  map[string]string
}

func newFakeRouter(password string) *fakeRouter {
	return &fakeRouter{password: password, valid: map[string]bool{}, renamed: map[string]string{}}
}

func (f *fakeRouter) expireSessions() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.valid = map[string]bool{}
}

func (f *fakeRouter) authed(r *http.Request) bool {
	c, err := r.Cookie("PHPSESSID")
	return err == nil && f.valid[c.Value]
}

func (f *fakeRouter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()

	switch {
	case r.URL.Path == "/login.php" && r.Method == http.MethodGet:
		f.next++
		http.SetCookie(w, &http.Cookie{Name: "PHPSESSID", Value: fmt.Sprint(f.next)})
		fmt.Fprint(w, `<form><input type='hidden' name='token' value='csrf'></form>`)
	case r.URL.Path == "/login.php" && r.Method == http.MethodPost:
		c, err := r.Cookie("PHPSESSID")
		if err == nil && r.FormValue("token") == "csrf" && r.FormValue("password") == f.password {
			f.valid[c.Value] = true
			f.logins++
		}
		w.WriteHeader(http.StatusOK)
	case !f.authed(r):
		fmt.Fprint(w, loginRedirectPage)
	case r.URL.Path == "/info.php":
		fmt.Fprint(w, `<script>var token='abc123';</script>`)
	case r.URL.Path == "/info-async.php" && r.FormValue("token") != "abc123":
		fmt.Fprint(w, loginRedirectPage)
	case r.URL.Path == "/info-async.php" && r.FormValue("do") == "renamedevice":
		f.renamed[r.FormValue("mac")] = r.FormValue("name")
		fmt.Fprint(w, `{"result": "OK"}`)
	case r.URL.Path == "/info-async.php":
		fmt.Fprint(w, `[{}, {}, {}, {}, {}]`)
	default:
		http.NotFound(w, r)
	}
}

func TestGetMetricsRecoversFromExpiredSession(t *testing.T) {
	router := newFakeRouter("secret")
	srv := httptest.NewServer(router)
	defer srv.Close()

	client, err := NewAmpliFiClient(srv.URL, "secret")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := client.GetMetrics(context.Background()); err != nil {
		t.Fatalf("first fetch: %v", err)
	}

	router.expireSessions()

	if _, err := client.GetMetrics(context.Background()); err == nil {
		t.Fatal("fetch with an expired session succeeded; want an error for the HTML redirect page")
	}

	body, err := client.GetMetrics(context.Background())
	if err != nil {
		t.Fatalf("fetch after expiry: %v", err)
	}
	if string(body) != `[{}, {}, {}, {}, {}]` {
		t.Fatalf("body = %q", body)
	}
	if router.logins != 2 {
		t.Fatalf("logins = %d, want 2", router.logins)
	}
}

func TestRenameDevice(t *testing.T) {
	router := newFakeRouter("secret")
	srv := httptest.NewServer(router)
	defer srv.Close()

	client, err := NewAmpliFiClient(srv.URL, "secret")
	if err != nil {
		t.Fatal(err)
	}
	if err := client.RenameDevice(context.Background(), "12:7e:85:2e:fe:4c", "Trish’s iPad"); err != nil {
		t.Fatal(err)
	}
	if got := router.renamed["12:7e:85:2e:fe:4c"]; got != "Trish’s iPad" {
		t.Fatalf("router name = %q", got)
	}
}
