package plugin

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"strings"
	"syscall"

	"github.com/Sam-H101/grafana-nexus-datasource/pkg/models"
	"github.com/Sam-H101/grafana-nexus-datasource/pkg/nexus"
	"github.com/grafana/grafana-plugin-sdk-go/backend"
)

// problem is an error translated for a user, with the status and error
// source Grafana should record.
type problem struct {
	status     backend.Status
	message    string
	downstream bool
}

// denied is a 403 for a known area: it names the privilege that is missing.
type denied struct {
	what      string
	privilege string
}

func (e *denied) Error() string { return "nexus: forbidden: " + e.what }
func (e *denied) Unwrap() error { return nexus.ErrForbidden }

// needs turns a 403 into a denied error naming the privilege to grant.
func needs(err error, what, privilege string) error {
	if errors.Is(err, nexus.ErrForbidden) {
		return &denied{what: what, privilege: privilege}
	}
	return err
}

// explain turns any error from settings, queries or the Nexus client into a
// message for the panel or the health check. Messages never contain the
// password (the URL may not carry credentials) or raw stack traces.
func (d *Datasource) explain(err error) problem {
	var (
		apiErr  *nexus.APIError
		deny    *denied
		netErr  net.Error
		dnsErr  *net.DNSError
		opErr   *net.OpError
		urlErr  *url.Error
		unkCA   x509.UnknownAuthorityError
		hostErr x509.HostnameError
		certErr x509.CertificateInvalidError
		recErr  tls.RecordHeaderError
		verErr  *tls.CertificateVerificationError
	)
	hasCredentials := d.client != nil && d.client.HasCredentials()
	switch {
	case errors.Is(err, models.ErrInvalidSettings):
		return problem{backend.StatusBadRequest, "Configuration problem: " + strings.TrimPrefix(err.Error(), models.ErrInvalidSettings.Error()+": "), true}
	case errors.Is(err, models.ErrInvalidQuery):
		msg := strings.TrimPrefix(err.Error(), models.ErrInvalidQuery.Error()+": ")
		return problem{backend.StatusBadRequest, strings.ToUpper(msg[:1]) + msg[1:] + ".", true}
	case errors.Is(err, nexus.ErrUnauthorized):
		if hasCredentials {
			return problem{backend.StatusUnauthorized, "Nexus rejected the user name or password. Check both in the data source settings, and that the user is active.", true}
		}
		return problem{backend.StatusUnauthorized, "This Nexus requires a login. Enter a user name and password in the data source settings.", true}
	case errors.As(err, &deny):
		return problem{backend.StatusForbidden, fmt.Sprintf("The Nexus user is not allowed to read %s. Give one of its roles the privilege %s.", deny.what, deny.privilege), true}
	case errors.Is(err, nexus.ErrForbidden):
		return problem{backend.StatusForbidden, "The Nexus user is not allowed to read this. Check the privileges of its roles.", true}
	case errors.Is(err, nexus.ErrNoUsage):
		return problem{backend.StatusBadRequest, "This Nexus does not report usage history. It is available from Nexus Repository 3.77.", true}
	case errors.As(err, &apiErr):
		return problem{backend.StatusBadRequest, "Nexus: " + strings.TrimSuffix(apiErr.Message, ".") + ".", true}
	case errors.Is(err, nexus.ErrNotNexus):
		return problem{backend.StatusBadGateway, "The server at " + d.url() + " did not answer like Nexus Repository 3. Check the URL: use the address you open Nexus at in a browser, including any path prefix such as /nexus.", true}
	case errors.Is(err, nexus.ErrResponseTooLarge):
		return problem{backend.StatusBadRequest, "The answer from Nexus is too large. Narrow the search or lower the limit.", true}
	case errors.Is(err, context.DeadlineExceeded):
		secs := 0
		if d.settings != nil {
			secs = int(d.settings.Timeout.Seconds())
		}
		return problem{backend.StatusTimeout, fmt.Sprintf("Nexus did not answer within %d seconds. Narrow the search or lower the limit, or raise the request timeout in the data source settings.", secs), true}
	case errors.Is(err, context.Canceled):
		return problem{backend.StatusTimeout, "The query was cancelled.", true}
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF), errors.Is(err, syscall.ECONNRESET):
		return problem{backend.StatusBadGateway, "Nexus at " + d.url() + " closed the connection before answering. It may be restarting.", true}
	case errors.As(err, &unkCA), errors.As(err, &hostErr), errors.As(err, &certErr), errors.As(err, &verErr), errors.As(err, &recErr):
		return problem{backend.StatusBadGateway, "TLS error connecting to Nexus: " + innermost(err) + ". Check the URL scheme and the TLS settings.", true}
	case strings.Contains(err.Error(), "malformed HTTP response"):
		// Typically TLS alert bytes: an http:// URL pointing at an HTTPS port.
		return problem{backend.StatusBadGateway, "The server at " + d.url() + " did not answer with HTTP. If Nexus is served over HTTPS, change the URL to https://.", true}
	case strings.Contains(err.Error(), "server gave HTTP response to HTTPS client"):
		return problem{backend.StatusBadGateway, "Nexus at " + d.url() + " answered with plain HTTP. Change the URL to http://, or put Nexus behind HTTPS.", true}
	case errors.As(err, &dnsErr):
		return problem{backend.StatusBadGateway, "Cannot reach Nexus at " + d.url() + ": host not found." + proxyHint, true}
	case errors.As(err, &opErr), errors.As(err, &netErr), errors.As(err, &urlErr):
		return problem{backend.StatusBadGateway, "Cannot reach Nexus at " + d.url() + ": " + innermost(err) + "." + proxyHint, true}
	default:
		return problem{backend.StatusInternal, err.Error(), false}
	}
}

// proxyHint reminds users of a hosted Grafana that a private Nexus needs a
// route to it.
const proxyHint = " If Nexus is on a private network and Grafana is hosted elsewhere (for example Grafana Cloud), turn on Private data source connect in the data source settings."

// logSafe shortens a user-facing message for logs.
func logSafe(msg string) string {
	if i := strings.IndexByte(msg, '\n'); i >= 0 {
		msg = msg[:i]
	}
	if len(msg) > 300 {
		msg = msg[:300] + "…"
	}
	return msg
}

func (d *Datasource) url() string {
	if d.settings == nil {
		return "the configured URL"
	}
	return d.settings.URL
}

// innermost strips url.Error's "Get \"...\":" prefix and similar wrapping.
func innermost(err error) string {
	for {
		u := errors.Unwrap(err)
		if u == nil {
			return err.Error()
		}
		err = u
	}
}

func (p problem) response() backend.DataResponse {
	src := backend.ErrorSourcePlugin
	if p.downstream {
		src = backend.ErrorSourceDownstream
	}
	return backend.ErrDataResponseWithSource(p.status, src, p.message)
}
