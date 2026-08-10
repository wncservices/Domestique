package garmin

import (
	"crypto/hmac"
	"crypto/sha1" // #nosec G505 -- verifying the OAuth1 signature the code produces.
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"testing"
	"time"
)

const (
	testEmail    = "rider@example.com"
	testPassword = "hunter2"
	testCSRF     = "csrf-token-abc"
	testTicket   = "ST-12345-abcde-cas"
	testKey      = "consumer-key"
	testSecret   = "consumer-secret"
)

// fakeConnect stands in for Garmin: the SSO pages, the OAuth1 exchange and the
// OAuth2 exchange, each checking what the client sent.
type fakeConnect struct {
	server *httptest.Server

	mfa         bool
	wrongPass   bool
	oauth1Calls int
	oauth2Calls int
	// lastAuth is the Authorization header of the most recent signed request.
	lastAuth string
	lastURL  string
}

func newFakeConnect(t *testing.T) (*Client, *fakeConnect) {
	t.Helper()
	fake := &fakeConnect{}

	mux := http.NewServeMux()

	mux.HandleFunc("/sso/signin", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			// The service parameter is not decoration: without it the real
			// SSO refuses the request.
			if r.URL.Query().Get("service") == "" {
				t.Error("sign-in page requested without a service parameter")
			}
			fmt.Fprintf(w, `<html><form><input name="_csrf" value="%s"/></form></html>`, testCSRF)
			return
		}

		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if got := r.PostForm.Get("_csrf"); got != testCSRF {
			t.Errorf("csrf = %q, want the one from the page", got)
		}

		switch {
		case fake.mfa:
			fmt.Fprint(w, `<html>Enter your verificationCode</html>`)
		case fake.wrongPass || r.PostForm.Get("password") != testPassword:
			fmt.Fprint(w, `<html>Invalid username or password</html>`)
		default:
			fmt.Fprintf(w, `<html><a href="https://sso.garmin.com/sso/embed?ticket=%s">go</a></html>`,
				testTicket)
		}
	})

	mux.HandleFunc("/oauth-service/oauth/preauthorized", func(w http.ResponseWriter, r *http.Request) {
		fake.oauth1Calls++
		fake.lastAuth = r.Header.Get("Authorization")
		fake.lastURL = "http://" + r.Host + r.URL.RequestURI()

		if r.URL.Query().Get("ticket") != testTicket {
			t.Errorf("ticket = %q, want %q", r.URL.Query().Get("ticket"), testTicket)
		}
		fmt.Fprint(w, "oauth_token=tok-1&oauth_token_secret=sec-1")
	})

	mux.HandleFunc("/oauth-service/oauth/exchange/user/2.0", func(w http.ResponseWriter, r *http.Request) {
		fake.oauth2Calls++
		fake.lastAuth = r.Header.Get("Authorization")
		if !strings.Contains(r.Header.Get("Authorization"), `oauth_token="tok-1"`) {
			t.Errorf("OAuth2 exchange did not present the OAuth1 token: %q",
				r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"access_token":"bearer-1","expires_in":3600}`)
	})

	fake.server = httptest.NewServer(mux)
	t.Cleanup(fake.server.Close)

	c := New()
	c.SSOBase = fake.server.URL + "/sso"
	c.APIBase = fake.server.URL
	c.WebBase = fake.server.URL
	c.SetConsumer(testKey, testSecret)
	return c, fake
}

func TestLoginExchangesThePasswordForTokens(t *testing.T) {
	c, fake := newFakeConnect(t)

	if err := c.Login(testEmail, testPassword); err != nil {
		t.Fatalf("Login failed: %v", err)
	}

	session := c.Session()
	if session.OAuth1Token != "tok-1" || session.OAuth1Secret != "sec-1" {
		t.Errorf("session = %+v, want the OAuth1 pair", session)
	}
	if session.ObtainedAt.IsZero() {
		t.Error("ObtainedAt is zero; nothing can warn about expiry without it")
	}
	if fake.oauth1Calls != 1 {
		t.Errorf("oauth1 exchanges = %d, want 1", fake.oauth1Calls)
	}
}

// MFA is not a wrong password, and telling a rider to check their password
// when the account wants a code sends them round in circles.
func TestLoginDistinguishesMFAFromBadCredentials(t *testing.T) {
	t.Run("mfa", func(t *testing.T) {
		c, fake := newFakeConnect(t)
		fake.mfa = true
		if err := c.Login(testEmail, testPassword); !errors.Is(err, ErrMFARequired) {
			t.Errorf("error = %v, want ErrMFARequired", err)
		}
	})

	t.Run("bad password", func(t *testing.T) {
		c, fake := newFakeConnect(t)
		fake.wrongPass = true
		if err := c.Login(testEmail, testPassword); !errors.Is(err, ErrBadCredentials) {
			t.Errorf("error = %v, want ErrBadCredentials", err)
		}
	})
}

func TestLoginRequiresBothFields(t *testing.T) {
	c, _ := newFakeConnect(t)
	if err := c.Login("", testPassword); err == nil {
		t.Error("an empty email was accepted")
	}
	if err := c.Login(testEmail, ""); err == nil {
		t.Error("an empty password was accepted")
	}
}

// The bearer is short-lived and every call needs one, so it must be cached —
// and it must not be cached past its expiry.
func TestBearerIsCachedThenRefreshed(t *testing.T) {
	c, fake := newFakeConnect(t)
	now := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	c.Now = func() time.Time { return now }

	if err := c.Login(testEmail, testPassword); err != nil {
		t.Fatal(err)
	}

	for range 3 {
		if _, err := c.bearerToken(); err != nil {
			t.Fatal(err)
		}
	}
	if fake.oauth2Calls != 1 {
		t.Errorf("oauth2 exchanges = %d, want 1: the bearer is not cached", fake.oauth2Calls)
	}

	// Past the hour the fake grants.
	now = now.Add(2 * time.Hour)
	if _, err := c.bearerToken(); err != nil {
		t.Fatal(err)
	}
	if fake.oauth2Calls != 2 {
		t.Errorf("oauth2 exchanges = %d, want 2: an expired bearer was reused", fake.oauth2Calls)
	}
}

// Resuming is the normal path: a stored session, no password, no SSO.
func TestResumeSkipsTheSSOFlow(t *testing.T) {
	c, fake := newFakeConnect(t)
	c.Resume(Session{OAuth1Token: "tok-1", OAuth1Secret: "sec-1"})

	if _, err := c.bearerToken(); err != nil {
		t.Fatalf("bearerToken after Resume: %v", err)
	}
	if fake.oauth1Calls != 0 {
		t.Errorf("the ticket exchange ran %d times; Resume must not sign in again", fake.oauth1Calls)
	}
	if fake.oauth2Calls != 1 {
		t.Errorf("oauth2 exchanges = %d, want 1", fake.oauth2Calls)
	}
}

func TestBearerWithoutASessionFails(t *testing.T) {
	c, _ := newFakeConnect(t)
	if _, err := c.bearerToken(); err == nil {
		t.Error("a bearer was issued without a session")
	}
}

// Without the consumer pair nothing can be signed, and saying so beats a
// signature the server rejects for no stated reason.
func TestMissingConsumerIsNamed(t *testing.T) {
	c, _ := newFakeConnect(t)
	c.consumer = consumerKey{}
	t.Setenv(EnvConsumerKey, "")
	t.Setenv(EnvConsumerSecret, "")

	err := c.Login(testEmail, testPassword)
	if !errors.Is(err, ErrNoConsumer) {
		t.Errorf("error = %v, want ErrNoConsumer", err)
	}
	if !strings.Contains(err.Error(), EnvConsumerKey) {
		t.Errorf("error = %q, want it to name the variable to set", err)
	}
}

func TestConsumerComesFromTheEnvironment(t *testing.T) {
	c, _ := newFakeConnect(t)
	c.consumer = consumerKey{}
	t.Setenv(EnvConsumerKey, "from-env")
	t.Setenv(EnvConsumerSecret, "secret-from-env")

	if err := c.loadConsumer(); err != nil {
		t.Fatal(err)
	}
	if c.consumer.Key != "from-env" {
		t.Errorf("consumer key = %q, want the environment's", c.consumer.Key)
	}
}

// The signature is the part with no useful error message when it is wrong, so
// verify it the way the server would rather than trusting it by inspection.
func TestOAuth1SignatureVerifies(t *testing.T) {
	c, fake := newFakeConnect(t)
	if err := c.Login(testEmail, testPassword); err != nil {
		t.Fatal(err)
	}

	params := parseAuthHeader(t, fake.lastAuth)
	signature := params["oauth_signature"]
	delete(params, "oauth_signature")

	parsed, err := url.Parse(fake.lastURL)
	if err != nil {
		t.Fatal(err)
	}
	for key, values := range parsed.Query() {
		params[key] = values[0]
	}

	// Encoded independently of the code under test. Reusing percentEncode and
	// encodeParams here would make this a tautology: a wrong encoder would
	// produce a wrong signature and a wrong expectation, and they would agree.
	base := strings.Join([]string{
		"GET",
		rfcEncode(parsed.Scheme + "://" + parsed.Host + parsed.Path),
		rfcEncode(normalise(params)),
	}, "&")

	mac := hmac.New(sha1.New, []byte(rfcEncode(testSecret)+"&"))
	if _, err := mac.Write([]byte(base)); err != nil {
		t.Fatal(err)
	}
	want := base64.StdEncoding.EncodeToString(mac.Sum(nil))

	if signature != want {
		t.Errorf("signature = %q, want %q\nbase string: %s", signature, want, base)
	}
}

// RFC 5849 encoding is not url.QueryEscape, and the difference is invisible
// until a signature fails.
func TestPercentEncodeFollowsTheRFC(t *testing.T) {
	for input, want := range map[string]string{
		"a b":         "a%20b",
		"~-._":        "~-._",
		"a+b":         "a%2Bb",
		"ST-1/2":      "ST-1%2F2",
		"héllo":       "h%C3%A9llo",
		"":            "",
		"a=b&c":       "a%3Db%26c",
		"UPPER lower": "UPPER%20lower",
	} {
		if got := percentEncode(input); got != want {
			t.Errorf("percentEncode(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestSessionExpiry(t *testing.T) {
	obtained := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s := Session{ObtainedAt: obtained}

	if want := obtained.AddDate(1, 0, 0); !s.TokenExpiry().Equal(want) {
		t.Errorf("TokenExpiry = %v, want %v", s.TokenExpiry(), want)
	}
	if s.Expired(obtained.AddDate(0, 6, 0)) {
		t.Error("a six-month-old token reported expired")
	}
	if !s.Expired(obtained.AddDate(1, 0, 1)) {
		t.Error("a token past a year reported live")
	}
}

// rfcEncode is RFC 5849 §3.6, written from the spec rather than reused from
// the package, so the two have to agree independently.
func rfcEncode(s string) string {
	var out strings.Builder
	for _, b := range []byte(s) {
		switch {
		case b >= 'A' && b <= 'Z', b >= 'a' && b <= 'z', b >= '0' && b <= '9',
			b == '-', b == '.', b == '_', b == '~':
			out.WriteByte(b)
		default:
			fmt.Fprintf(&out, "%%%02X", b)
		}
	}
	return out.String()
}

// normalise is RFC 5849 §3.4.1.3.2: encode each name and value, sort, join.
func normalise(params map[string]string) string {
	pairs := make([]string, 0, len(params))
	for name, value := range params {
		pairs = append(pairs, rfcEncode(name)+"="+rfcEncode(value))
	}
	sort.Strings(pairs)
	return strings.Join(pairs, "&")
}

func parseAuthHeader(t *testing.T, header string) map[string]string {
	t.Helper()
	if !strings.HasPrefix(header, "OAuth ") {
		t.Fatalf("Authorization = %q, want an OAuth header", header)
	}

	out := map[string]string{}
	for _, part := range strings.Split(strings.TrimPrefix(header, "OAuth "), ", ") {
		name, value, ok := strings.Cut(part, "=")
		if !ok {
			t.Fatalf("unparseable header part %q", part)
		}
		unquoted := strings.Trim(value, `"`)
		decoded, err := url.QueryUnescape(unquoted)
		if err != nil {
			t.Fatal(err)
		}
		out[name] = decoded
	}

	// Sorted output is what makes a broken header readable in a diff.
	names := make([]string, 0, len(out))
	for name := range out {
		names = append(names, name)
	}
	sort.Strings(names)
	return out
}

// A Cloudflare block is not a wrong password, and must not be reported as
// one: it says nothing about the account, and sending someone to reset a
// password that was fine is the same mistake as mislabelling an MFA prompt.
func TestCloudflareBlockIsItsOwnError(t *testing.T) {
	const blockPage = `<!DOCTYPE html><html><head><title>Attention Required! | Cloudflare</title></head>
<body><div class="cf-wrapper">Sorry, you have been blocked</div></body></html>`

	for _, at := range []string{"embed", "signin", "post"} {
		t.Run(at, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				blockHere := (at == "embed" && strings.Contains(r.URL.Path, "/embed")) ||
					(at == "signin" && strings.Contains(r.URL.Path, "/signin") && r.Method == http.MethodGet) ||
					(at == "post" && r.Method == http.MethodPost)
				if blockHere {
					w.WriteHeader(http.StatusForbidden)
					_, _ = io.WriteString(w, blockPage)
					return
				}
				_, _ = io.WriteString(w, `<form><input name="_csrf" value="token-1" /></form>`)
			}))
			defer server.Close()

			client := New()
			client.SSOBase, client.APIBase, client.WebBase = server.URL, server.URL, server.URL
			client.SetConsumer("k", "s")

			err := client.Login("rider@example.com", "pw")
			if !errors.Is(err, ErrBlocked) {
				t.Errorf("Login error = %v, want ErrBlocked", err)
			}
			if errors.Is(err, ErrBadCredentials) {
				t.Error("a block was reported as a bad password")
			}
		})
	}
}

// ...and a genuine rejection is still a rejection. Without this the block
// detector could swallow everything and nobody would notice.
func TestPlainRejectionIsStillBadCredentials(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			_, _ = io.WriteString(w, `<html><body>Invalid username or password</body></html>`)
			return
		}
		_, _ = io.WriteString(w, `<form><input name="_csrf" value="token-1" /></form>`)
	}))
	defer server.Close()

	client := New()
	client.SSOBase, client.APIBase, client.WebBase = server.URL, server.URL, server.URL
	client.SetConsumer("k", "s")

	if err := client.Login("rider@example.com", "pw"); !errors.Is(err, ErrBadCredentials) {
		t.Errorf("Login error = %v, want ErrBadCredentials", err)
	}
}

// The widget load is what sets the cookies the rest of the flow carries.
func TestLoginLoadsTheWidgetFirst(t *testing.T) {
	var order []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		order = append(order, r.Method+" "+r.URL.Path)
		if r.Method == http.MethodPost {
			_, _ = io.WriteString(w, `<a href="embed?ticket=ST-1-abc"></a>`)
			return
		}
		_, _ = io.WriteString(w, `<form><input name="_csrf" value="token-1" /></form>`)
	}))
	defer server.Close()

	client := New()
	client.SSOBase, client.APIBase, client.WebBase = server.URL, server.URL, server.URL
	client.SetConsumer("k", "s")
	_ = client.Login("rider@example.com", "pw")

	if len(order) == 0 || !strings.HasSuffix(order[0], "/embed") {
		t.Errorf("request order = %v, want the widget loaded first", order)
	}
}

// Garmin has rejected a password with 200-and-an-error-page and with 401,
// both observed within a day. Either has to read as "check what you typed",
// not as "Garmin is having a bad day".
func TestRejectedCredentialsInEitherDialect(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusUnauthorized} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost {
					w.WriteHeader(status)
					_, _ = io.WriteString(w, `<html><body>Invalid username or password</body></html>`)
					return
				}
				_, _ = io.WriteString(w, `<form><input name="_csrf" value="token-1" /></form>`)
			}))
			defer server.Close()

			client := New()
			client.SSOBase, client.APIBase, client.WebBase = server.URL, server.URL, server.URL
			client.SetConsumer("k", "s")

			if err := client.Login("rider@example.com", "pw"); !errors.Is(err, ErrBadCredentials) {
				t.Errorf("Login error = %v, want ErrBadCredentials", err)
			}
		})
	}
}
