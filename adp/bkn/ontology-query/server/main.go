// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package main

import "github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/app"

func main() {
	application := app.Boot(app.Options{})
	application.Run()
}
