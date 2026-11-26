//go:build windows

package main

import "github.com/DVE-FCT/HopSend/internal/app"

func (a *App) BrowseDeviceShares(id string) (app.BrowseSharesResult, error) {
	if err := a.Ready(); err != nil {
		return app.BrowseSharesResult{}, err
	}
	device, err := a.findDevice(id)
	if err != nil {
		return app.BrowseSharesResult{}, err
	}
	return app.BrowseShares(a.ctx, device.request().Agent), nil
}

func (a *App) BrowseDeviceEntries(id, share, path, cursor string, limit int) (app.BrowsePageResult, error) {
	if err := a.Ready(); err != nil {
		return app.BrowsePageResult{}, err
	}
	device, err := a.findDevice(id)
	if err != nil {
		return app.BrowsePageResult{}, err
	}
	return app.BrowseEntries(a.ctx, device.request().Agent, share, path, cursor, limit), nil
}
