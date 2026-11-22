package plugin

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Sam-H101/grafana-nexus-datasource/pkg/nexus"
	"github.com/grafana/grafana-plugin-sdk-go/backend"
)

const healthTimeout = 20 * time.Second

// Privileges a Nexus user needs for each area, as named in Nexus.
const (
	privBrowse = "nx-repository-view-*-*-browse"
	// privRepositoryAdmin reveals storage and policies in the repository list.
	privRepositoryAdmin = "nx-repository-admin-*-*-read"
	privSearch          = "nx-search-read"
	privBlobStores      = "nx-blobstores-read"
	privTasks           = "nx-tasks-read"
	privMetrics         = "nx-metrics-all"
)

// CheckHealth backs "Save & test". It checks that the server is reachable
// and is Nexus and that the credentials are accepted, says how many
// repositories the user can see, and lists the areas the user may not read.
func (d *Datasource) CheckHealth(ctx context.Context, _ *backend.CheckHealthRequest) (*backend.CheckHealthResult, error) {
	fail := func(err error) (*backend.CheckHealthResult, error) {
		p := d.explain(err)
		backend.Logger.Warn("Nexus health check failed", "status", int(p.status), "error", logSafe(p.message))
		return &backend.CheckHealthResult{Status: backend.HealthStatusError, Message: p.message}, nil
	}
	if d.settingsErr != nil {
		return &backend.CheckHealthResult{Status: backend.HealthStatusError, Message: d.explain(d.settingsErr).message}, nil
	}
	ctx, cancel := context.WithTimeout(ctx, healthTimeout)
	defer cancel()

	status, err := d.client.Status(ctx)
	var apiErr *nexus.APIError
	switch {
	case errors.As(err, &apiErr) && apiErr.Status == 503:
		return &backend.CheckHealthResult{
			Status:  backend.HealthStatusError,
			Message: "Nexus at " + d.settings.URL + " is reachable but cannot serve requests yet (HTTP 503). Try again when it has finished starting.",
		}, nil
	case errors.Is(err, nexus.ErrUnauthorized) && !d.client.HasCredentials(), errors.Is(err, nexus.ErrForbidden):
		// Nexus answers its status check without credentials, so a refusal
		// here comes from something else in front of it.
		return &backend.CheckHealthResult{
			Status:  backend.HealthStatusError,
			Message: "The server at " + d.settings.URL + " asked for a login before answering Nexus' status check, which Nexus itself never does. Check the URL. If a proxy in front of Nexus requires its own login, Grafana cannot get through it with a Nexus user.",
		}, nil
	case err != nil:
		return fail(err)
	}

	repos, err := d.client.Repositories(ctx)
	if err != nil {
		return fail(err)
	}

	where := "Nexus Repository"
	if status.Version != "" {
		where += " " + status.Version
	}
	if edition := editionName(status.Edition); edition != "" {
		where += " (" + edition + ")"
	}
	who := "The user " + d.settings.Username
	if !d.client.HasCredentials() {
		who = "Anonymous access"
	}
	msg := fmt.Sprintf("Connected to %s. %s can see %d repositor%s.", where, who, len(repos), pluralY(len(repos)))
	if len(repos) == 0 {
		if d.client.HasCredentials() {
			msg += " Give one of the user's roles the privilege " + privBrowse + "."
		} else {
			msg += " Enter a user name and password to see more."
		}
	}

	// The remaining areas each need their own privilege. Missing ones do not
	// fail the check: the other queries still work.
	var missing []string
	probe := func(what, privilege string, call func() error) {
		if err := call(); errors.Is(err, nexus.ErrForbidden) || errors.Is(err, nexus.ErrUnauthorized) {
			missing = append(missing, what+" ("+privilege+")")
		}
	}
	probe("component and asset search", privSearch, func() error {
		_, _, err := d.client.Components(ctx, nexus.SearchFilter{}, 1)
		return err
	})
	probe("blob stores", privBlobStores, func() error { _, err := d.client.BlobStores(ctx); return err })
	probe("tasks", privTasks, func() error { _, err := d.client.Tasks(ctx); return err })
	probe("health checks, usage, formats and metrics", privMetrics, func() error { _, err := d.client.HealthChecks(ctx); return err })
	if len(missing) > 0 {
		msg += " Not readable with this login: " + strings.Join(missing, "; ") + "."
	}
	return &backend.CheckHealthResult{Status: backend.HealthStatusOk, Message: msg}, nil
}

func editionName(edition string) string {
	switch strings.ToUpper(edition) {
	case "COMMUNITY":
		return "Community Edition"
	case "PRO":
		return "Pro"
	case "OSS":
		return "OSS"
	case "":
		return ""
	}
	return edition
}

func pluralY(n int) string {
	if n == 1 {
		return "y"
	}
	return "ies"
}
