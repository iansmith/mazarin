package host

import (
	"fmt"

	"mazzy/mazarin/fsclient"
	"mazzy/mazarin/linksurface"
	"mazzy/mazarin/mazdl"
	"mazzy/mazarin/mazhost"
)

// LoadLinkSurfacePlugin loads /<name>.maz for an L3+ plugin. Host
// builds the LinkSurfaceInit with Device + Allocator + RecvChan;
// plugin registers TxChan via MazarinShepherd. Returns an error if
// the plugin failed to load, lacked a MazarinShepherd export,
// returned an error from MazarinShepherd, or did not register a
// TxChan.
func LoadLinkSurfacePlugin(fc fsclient.FSClient, name string, dev linksurface.Device, alloc *Allocator) (*linksurface.LinkSurfaceInit, error) {
	init := NewLinkSurfaceInit(dev, alloc)
	ForceLinkSurfaceItab(init)

	path := "/" + name + ".maz"
	mazMain, shepherdAddr, mErr := mazhost.LoadMazBootstrap(fc, path, nil)
	if mErr != nil {
		return nil, fmt.Errorf("LoadMazBootstrap(%s): %v", path, mErr)
	}
	if shepherdAddr == 0 {
		return nil, fmt.Errorf("%s: no MazarinShepherd export", path)
	}

	shepherdInit := mazdl.Funcval[func(any) error](shepherdAddr)
	if err := shepherdInit(init); err != nil {
		return nil, fmt.Errorf("%s MazarinShepherd: %w", path, err)
	}
	if init.TxChan == nil {
		return nil, fmt.Errorf("%s: MazarinShepherd returned but did not register TxChan", path)
	}

	go mazhost.RunMaz(mazMain)
	return init, nil
}
