// Command hearth generates and checks what an app on hearth publishes from its
// Protobuf service: its manifest.
//
//	hearth manifest diff OLD NEW     compare 2 manifests for breaking changes
//	hearth manifest validate FILE    check 1 manifest against the schema
package main

import (
	"os"
)

func main() {
	os.Exit(Run(os.Args[1:], os.Stdout, os.Stderr))
}
