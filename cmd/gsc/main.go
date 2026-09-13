// Command gsc is a local-first, read-only Google Search Console CLI.
package main

import "os"

func main() {
	os.Exit(Execute(os.Args[1:]))
}
