//go:build windows

package main

import "fmt"

// restartSelf degrades honestly on Windows: there is no exec that replaces the
// running image, and spawning a child from a process that is about to exit
// hands the new keeptui a console the old one still owns. Printing the hint and
// exiting is the whole restart here — no spawn, no path resolution.
func restartSelf() {
	fmt.Println(restartHint)
}
