//go:build windows

package main

import (
	"embed"
	"log"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	app := NewApp()
	if err := wails.Run(&options.App{Title: "跃传 HopSend", Width: 1360, Height: 900, MinWidth: 1080, MinHeight: 740, AssetServer: &assetserver.Options{Assets: assets}, BackgroundColour: &options.RGBA{R: 244, G: 247, B: 250, A: 255}, SingleInstanceLock: &options.SingleInstanceLock{UniqueId: "dve-fct.hopsend.desktop"}, OnStartup: app.startup, OnBeforeClose: app.beforeClose, Bind: []interface{}{app}}); err != nil {
		log.Fatal(err)
	}
}
