//go:build !windows

package plugins

import (
	stdplugin "plugin"
)

func openPlugin(path string) (Plugin, error) {
	handle, err := stdplugin.Open(path)
	if err != nil {
		return nil, err
	}
	symbol, err := handle.Lookup(SymbolName)
	if err != nil {
		return nil, err
	}
	return resolvePluginSymbol(symbol)
}

func resolvePluginSymbol(symbol any) (Plugin, error) {
	switch v := symbol.(type) {
	case Plugin:
		return v, nil
	case *Plugin:
		return *v, nil
	case func() Plugin:
		return v(), nil
	case *func() Plugin:
		return (*v)(), nil
	default:
		return nil, ErrInvalidPluginSymbol
	}
}
