// Command cert-manager-webhook-allinkl is a cert-manager ACME DNS-01 solver
// for domains hosted at All-Inkl (all-inkl.com).
//
// cert-manager ships no in-tree provider for All-Inkl and no community
// webhook existed, so this fills that gap: it runs as a Kubernetes aggregated
// API server that cert-manager calls to place and remove _acme-challenge TXT
// records through the KAS API.
package main

import (
	"fmt"
	"log"
	"os"

	"github.com/cert-manager/cert-manager/pkg/acme/webhook/cmd"

	"github.com/wenisch-tech/cert-manager-webhook-allinkl/internal/solver"
	"github.com/wenisch-tech/cert-manager-webhook-allinkl/internal/version"
)

func main() {
	// Parsed before the webhook framework installs its own flag set, which
	// would otherwise reject an unknown --version.
	if len(os.Args) > 1 && (os.Args[1] == "--version" || os.Args[1] == "-version") {
		fmt.Printf("cert-manager-webhook-allinkl %s (commit %s, built %s)\n",
			version.Version, version.Commit, version.BuildDate)
		return
	}

	groupName := os.Getenv("GROUP_NAME")
	if groupName == "" {
		log.Fatal("GROUP_NAME must be set (the API group this webhook serves, e.g. acme.example.com)")
	}

	cmd.RunWebhookServer(groupName, solver.New())
}
