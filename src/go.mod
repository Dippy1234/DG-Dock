module dgdock

// Dependencies are vendored in ./vendor, so the project builds offline with plain `go build`.

go 1.24

require golang.org/x/image v0.24.0

require (
	github.com/jchv/go-webview2 v0.0.0-20260205173254-56598839c808
	github.com/jchv/go-winloader v0.0.0-20250406163304-c1995be93bd1
	golang.org/x/sys v0.30.0
	golang.org/x/text v0.22.0 // indirect
)

replace golang.org/x/image => ./third_party/image

replace golang.org/x/sys => ./third_party/sys

replace golang.org/x/text => ./third_party/text

replace github.com/jchv/go-webview2 => ./third_party/go-webview2

replace github.com/jchv/go-winloader => ./third_party/go-winloader
