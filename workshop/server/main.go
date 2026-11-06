// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Command workshop is the community build of the Workshop service: health and
// readiness only. The business routes are assembled by the paid build
// (openbkn-ee workshop-ee), so a community deployment answers them with 404.
package main

import "github.com/openbkn-ai/bkn-foundry/workshop/server/app"

func main() {
	app.Boot().Run()
}
