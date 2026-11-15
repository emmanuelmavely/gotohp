package main

import (
	"embed"
	"encoding/json"

	"app/core"
)

// build/buildinfo.json holds the commit and build date. The release workflow
// writes it before building; local builds keep the committed empty values.
//
//go:embed build/windows/info.json build/buildinfo.json CHANGELOG.md
var versionInfo embed.FS

// getAppVersion returns the version from the embedded info.json. The release
// workflow sets it from the pushed tag.
func getAppVersion() string {
	return core.GetVersion(versionInfo)
}

// buildInfo describes where a binary came from.
type buildInfo struct {
	Commit string `json:"commit"`
	Date   string `json:"date"`
}

func getBuildInfo() buildInfo {
	var info buildInfo
	if data, err := versionInfo.ReadFile("build/buildinfo.json"); err == nil {
		_ = json.Unmarshal(data, &info)
	}
	return info
}

// getChangelog returns the CHANGELOG.md the binary was built with.
func getChangelog() string {
	data, _ := versionInfo.ReadFile("CHANGELOG.md")
	return string(data)
}
