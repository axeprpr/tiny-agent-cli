//go:build windows

package plugins

import "fmt"

var openPlugin = func(path string) (Plugin, error) {
	return nil, fmt.Errorf("plugins are not supported on this platform: %s", path)
}
