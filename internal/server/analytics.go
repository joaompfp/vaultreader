// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"
)

// AnalyticsConfig adds an Umami tracking tag to the SPA shell (never to
// /share/ pages, whose recipients did not opt in to anything).
type AnalyticsConfig struct {
	UmamiScript    string `json:"umami_script"`     // "/site/root/path.js" or "https://host/path.js"
	UmamiWebsiteID string `json:"umami_website_id"` // the Umami website UUID
}

// Both values are spliced into HTML and the CSP header unescaped, so they
// are restricted to characters that cannot break out of an attribute.
var (
	uuidRe        = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	scriptPathRe  = regexp.MustCompile(`^/[A-Za-z0-9._~-][A-Za-z0-9._~/-]*$`)
	scriptHTTPSRe = regexp.MustCompile(`^(https://[A-Za-z0-9.-]+(:[0-9]{1,5})?)(/[A-Za-z0-9._~/-]*)?$`)
)

func validateAnalytics(a *AnalyticsConfig) error {
	if a == nil {
		return nil
	}
	if a.UmamiScript == "" || a.UmamiWebsiteID == "" {
		return fmt.Errorf("analytics needs both umami_script and umami_website_id")
	}
	if !uuidRe.MatchString(a.UmamiWebsiteID) {
		return fmt.Errorf("analytics.umami_website_id %q is not a UUID", a.UmamiWebsiteID)
	}
	if !scriptPathRe.MatchString(a.UmamiScript) && !scriptHTTPSRe.MatchString(a.UmamiScript) {
		return fmt.Errorf("analytics.umami_script %q must be a site-root path (\"/…\") or an https:// URL", a.UmamiScript)
	}
	return nil
}

// remoteOrigin is "https://host[:port]" for an https script, "" for a
// same-origin path (already covered by 'self').
func (a *AnalyticsConfig) remoteOrigin() string {
	if a == nil {
		return ""
	}
	if m := scriptHTTPSRe.FindStringSubmatch(a.UmamiScript); m != nil {
		return m[1]
	}
	return ""
}

// withAnalytics inserts the tracking tag just before </head>. It runs
// after withBasePath, so a leading-/ script stays site-root and is never
// given the base_path prefix.
func withAnalytics(page []byte, a *AnalyticsConfig) []byte {
	if a == nil {
		return page
	}
	tag := `<script defer src="` + a.UmamiScript + `" data-website-id="` + a.UmamiWebsiteID + `"></script>` + "\n</head>"
	return bytes.Replace(page, []byte("</head>"), []byte(tag), 1)
}

// shellCSP is spaCSP with a remote analytics origin added to script-src
// and connect-src (the script loads from it and posts events back to it).
func shellCSP(a *AnalyticsConfig) string {
	origin := a.remoteOrigin()
	if origin == "" {
		return spaCSP
	}
	csp := strings.Replace(spaCSP, "script-src 'self' 'unsafe-inline' 'unsafe-eval';",
		"script-src 'self' 'unsafe-inline' 'unsafe-eval' "+origin+";", 1)
	return strings.Replace(csp, "connect-src 'self';", "connect-src 'self' "+origin+";", 1)
}
