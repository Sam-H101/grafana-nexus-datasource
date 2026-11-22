// Package models parses data source settings and query models. It does no I/O.
package models

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
)

const (
	DefaultTimeoutSeconds = 30
	MaxTimeoutSeconds     = 300
)

// Settings is the validated configuration of one data source instance.
type Settings struct {
	// URL is the base URL of Nexus Repository, without a trailing slash. It
	// may include a context path (https://host/nexus).
	URL string
	// Username and Password are sent with HTTP Basic authentication. They
	// may also be the two halves of a user token. Both empty means anonymous
	// access, which only works where Nexus allows it.
	Username string
	Password string
	Timeout  time.Duration
}

type jsonSettings struct {
	Username       string `json:"username"`
	TimeoutSeconds int    `json:"timeoutSeconds"`
}

// ErrInvalidSettings wraps every validation failure so callers can report it
// as a configuration problem.
var ErrInvalidSettings = errors.New("invalid data source settings")

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidSettings, fmt.Sprintf(format, a...))
}

// LoadSettings reads and validates instance settings. The URL is Grafana's
// standard data source URL field; the password is secureJsonData.password.
func LoadSettings(src backend.DataSourceInstanceSettings) (*Settings, error) {
	var js jsonSettings
	if len(src.JSONData) > 0 {
		if err := json.Unmarshal(src.JSONData, &js); err != nil {
			return nil, invalid("could not parse settings JSON")
		}
	}

	baseURL, err := normalizeURL(src.URL)
	if err != nil {
		return nil, err
	}

	s := &Settings{
		URL:      baseURL,
		Username: strings.TrimSpace(js.Username),
		// A password may legitimately start or end with a space.
		Password: src.DecryptedSecureJSONData["password"],
	}
	switch {
	case s.Username == "" && s.Password != "":
		return nil, invalid("a password is set without a user name; enter the Nexus user name too")
	case s.Username != "" && s.Password == "":
		return nil, invalid("enter the password of the Nexus user %q", s.Username)
	case strings.ContainsAny(s.Username, ":\r\n"):
		// A colon cannot be carried in the user half of Basic authentication.
		return nil, invalid("the user name must not contain a colon or a line break")
	}

	switch {
	case js.TimeoutSeconds == 0:
		s.Timeout = DefaultTimeoutSeconds * time.Second
	case js.TimeoutSeconds < 0 || js.TimeoutSeconds > MaxTimeoutSeconds:
		return nil, invalid("the request timeout must be between 1 and %d seconds", MaxTimeoutSeconds)
	default:
		s.Timeout = time.Duration(js.TimeoutSeconds) * time.Second
	}
	return s, nil
}

func normalizeURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", invalid("the Nexus URL is required, for example https://nexus.example.com")
	}
	u, perr := url.Parse(raw)
	if perr != nil {
		return "", invalid("the Nexus URL is not a valid URL")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", invalid("the Nexus URL must start with http:// or https://")
	}
	if u.Host == "" {
		return "", invalid("the Nexus URL has no host")
	}
	if u.User != nil {
		// Credentials in the URL would end up in logs and error messages.
		return "", invalid("put the user name and password in their own fields, not in the URL")
	}
	if u.RawQuery != "" {
		return "", invalid("the Nexus URL must not have a query string")
	}
	// People paste the address of a page (https://host/#browse/browse) or of
	// the API; keep the base only.
	u.Fragment, u.RawFragment = "", ""
	u.Path = strings.TrimRight(u.Path, "/")
	for _, suffix := range []string{"/service/rest/v1", "/service/rest", "/service", "/repository"} {
		u.Path = strings.TrimSuffix(u.Path, suffix)
	}
	u.RawPath = ""
	return u.String(), nil
}
