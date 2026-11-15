//go:build !cli

package gui

import (
	"context"
	"net/http"
	"net/url"
	"time"

	"app/core"
)

// AppInfo describes the running build.
type AppInfo struct {
	// Version is the app version without a leading "v".
	Version string
	// Commit and BuildDate are filled in by the release workflow; local
	// builds leave them empty.
	Commit    string
	BuildDate string
	// Repo is the GitHub repository releases are published to.
	Repo string
}

// AppService gives the frontend the app's version, its changelog and the
// GitHub update check.
type AppService struct {
	info      AppInfo
	changelog string
}

func NewAppService(info AppInfo, changelog string) *AppService {
	if info.Repo == "" {
		info.Repo = core.UpdateRepo
	}
	return &AppService{info: info, changelog: changelog}
}

// Info returns the version and build details.
func (s *AppService) Info() AppInfo {
	return s.info
}

// Changelog returns the CHANGELOG.md bundled with this build.
func (s *AppService) Changelog() string {
	return s.changelog
}

// CheckForUpdate asks GitHub for the latest release and compares it with the
// running version. It honours the proxy set in Settings. Nothing is
// downloaded.
func (s *AppService) CheckForUpdate() (core.UpdateInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return core.CheckForUpdate(ctx, updateClient(), s.info.Version)
}

// updateClient builds a client that uses the Settings proxy, if any.
func updateClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	proxy := (&core.ConfigManager{}).SessionUploadOptions().Api.Proxy
	if proxy != "" {
		if proxyURL, err := url.Parse(proxy); err == nil {
			transport.Proxy = http.ProxyURL(proxyURL)
		}
	}
	return &http.Client{Transport: transport, Timeout: 20 * time.Second}
}
