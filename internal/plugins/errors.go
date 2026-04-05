package plugins

import "fmt"

var ErrInvalidPluginSymbol = fmt.Errorf("plugin symbol %q did not resolve to a compatible Plugin", SymbolName)
