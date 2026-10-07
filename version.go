//go:build windows

package main

// Filled by build.ps1 via -X; never use startup time as the version.
var buildVersion = "开发版"

func windowTitle() string {
	return appTitle + " " + buildVersion
}
