package garmin

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1" // #nosec G505 -- OAuth1 specifies HMAC-SHA1; the server verifies it, so the choice is not ours.
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
)

// Environment variables holding the OAuth1 consumer Connect's own clients use.
//
// Not hardcoded, and deliberately not fetched at runtime from the public
// bucket the Python reference implementation reads. Both would be worse:
// baking scraped credentials into a source-available repository invites them
// to be treated as ours to publish, and downloading a signing key from a
// third-party bucket on every deploy is a supply-chain dependency nobody
// reviewed. An operator who wants this feature supplies the pair.
const (
	EnvConsumerKey = "GARMIN_OAUTH_CONSUMER_KEY"
	// #nosec G101 -- this is the *name* of an environment variable, not a
	// credential. The value it names is deliberately not in this repository.
	EnvConsumerSecret = "GARMIN_OAUTH_CONSUMER_SECRET"
)

// ErrNoConsumer means the OAuth1 consumer pair is not configured.
var ErrNoConsumer = fmt.Errorf(
	"garmin: no OAuth1 consumer configured — set %s and %s", EnvConsumerKey, EnvConsumerSecret)

func (c *Client) loadConsumer() error {
	if c.consumer.Key != "" && c.consumer.Secret != "" {
		return nil
	}

	key, secret := os.Getenv(EnvConsumerKey), os.Getenv(EnvConsumerSecret)
	if key == "" || secret == "" {
		return ErrNoConsumer
	}
	c.consumer = consumerKey{Key: key, Secret: secret}
	return nil
}

// ConsumerFromEnv returns the consumer pair the environment carries.
//
// One of two places it can come from — an admin can also paste it into the UI,
// which is stored encrypted and wins over this. Callers decide between them;
// this only reports what the environment has.
func ConsumerFromEnv() (key, secret string, ok bool) {
	key, secret = os.Getenv(EnvConsumerKey), os.Getenv(EnvConsumerSecret)
	return key, secret, key != "" && secret != ""
}

// SetConsumer supplies the OAuth1 consumer directly, for tests and for callers
// that hold it somewhere other than the environment.
func (c *Client) SetConsumer(key, secret string) {
	c.consumer = consumerKey{Key: key, Secret: secret}
}

// signOAuth1 builds an OAuth1 Authorization header.
//
// HMAC-SHA1 because that is what the endpoint verifies; the algorithm is not
// a choice this package gets to make. Written out rather than pulled in as a
// dependency: it is forty lines, and the Go OAuth1 libraries all want to own
// the HTTP client too.
func (c *Client) signOAuth1(method, rawURL, token, tokenSecret string) (string, error) {
	if c.consumer.Key == "" {
		return "", ErrNoConsumer
	}

	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}

	nonce, err := randomNonce()
	if err != nil {
		return "", err
	}

	params := map[string]string{
		"oauth_consumer_key":     c.consumer.Key,
		"oauth_nonce":            nonce,
		"oauth_signature_method": "HMAC-SHA1",
		"oauth_timestamp":        strconv.FormatInt(c.now().Unix(), 10),
		"oauth_version":          "1.0",
	}
	if token != "" {
		params["oauth_token"] = token
	}

	// The signature covers the query string as well, so those parameters go
	// into the base string alongside the oauth_ ones — omitting them is the
	// classic way to get a signature the server rejects with no explanation.
	signing := make(map[string]string, len(params))
	for k, v := range params {
		signing[k] = v
	}
	for k, values := range parsed.Query() {
		if len(values) > 0 {
			signing[k] = values[0]
		}
	}

	base := strings.Join([]string{
		strings.ToUpper(method),
		percentEncode(scheme(parsed) + "://" + parsed.Host + parsed.Path),
		percentEncode(encodeParams(signing)),
	}, "&")

	key := percentEncode(c.consumer.Secret) + "&" + percentEncode(tokenSecret)
	mac := hmac.New(sha1.New, []byte(key))
	if _, err := mac.Write([]byte(base)); err != nil {
		return "", err
	}
	params["oauth_signature"] = base64.StdEncoding.EncodeToString(mac.Sum(nil))

	// Header parameters are conventionally sorted; the server does not care,
	// but a stable header is far easier to compare when something breaks.
	names := make([]string, 0, len(params))
	for name := range params {
		names = append(names, name)
	}
	sort.Strings(names)

	var out strings.Builder
	out.WriteString("OAuth ")
	for i, name := range names {
		if i > 0 {
			out.WriteString(", ")
		}
		fmt.Fprintf(&out, "%s=%q", name, percentEncode(params[name]))
	}
	return out.String(), nil
}

func scheme(u *url.URL) string {
	if u.Scheme == "" {
		return "https"
	}
	return u.Scheme
}

// encodeParams builds the normalised parameter string: sorted by key, each
// part percent-encoded, joined with & and =.
func encodeParams(params map[string]string) string {
	names := make([]string, 0, len(params))
	for name := range params {
		names = append(names, name)
	}
	sort.Strings(names)

	parts := make([]string, 0, len(names))
	for _, name := range names {
		parts = append(parts, percentEncode(name)+"="+percentEncode(params[name]))
	}
	return strings.Join(parts, "&")
}

// percentEncode is RFC 5849's encoding, which is not url.QueryEscape: a space
// is %20 rather than +, and - . _ ~ are left alone. Getting this wrong
// produces a signature mismatch and no clue as to why.
func percentEncode(s string) string {
	const unreserved = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-._~"

	var out strings.Builder
	for _, b := range []byte(s) {
		if strings.IndexByte(unreserved, b) >= 0 {
			out.WriteByte(b)
			continue
		}
		fmt.Fprintf(&out, "%%%02X", b)
	}
	return out.String()
}

func randomNonce() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", errors.New("garmin: could not generate a nonce")
	}
	return hex.EncodeToString(raw), nil
}
