//go:build windows && !dev

package main

// devBuild is false for the public release: no watching the Downloads folder for newer
// exes (that's only for trying out test builds).
const devBuild = false
