package main

import "os"

// main is the process entry point.
//
// coverage: ignore - process entry point; Run holds all the logic and is tested.
func main() { os.Exit(Run(os.Args[1:], os.Stdout)) }
