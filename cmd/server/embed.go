package main

import (
	"embed"
	"io/fs"
	"log"
)

//go:embed all:dashboard_out
var dashboardStaticFS embed.FS

// getDashboardFS returns a filesystem rooted at the dashboard output.
// Returns nil if dashboard_out doesn't exist (development mode).
func getDashboardFS() fs.FS {
	// Try to open the embedded directory
	f, err := dashboardStaticFS.Open("dashboard_out")
	if err != nil {
		log.Println("[Dashboard] Embedded dashboard not found (development mode)")
		return nil
	}
	f.Close()

	fsys, err := fs.Sub(dashboardStaticFS, "dashboard_out")
	if err != nil {
		log.Printf("[Dashboard] Failed to create sub-filesystem: %v", err)
		return nil
	}
	return fsys
}