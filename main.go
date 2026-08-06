package main

import (
	"embed"
	"log"

	"github.com/wailsapp/wails/v3/pkg/application"

	"drivewise/backend/analyzer"
	"drivewise/backend/cleaner"
	"drivewise/backend/migrator"
	"drivewise/backend/sysinfo"
)

// Wails uses Go's `embed` package to embed the frontend files into the binary.
// Any files in the frontend/dist folder will be embedded into the binary and
// made available to the frontend.
// See https://pkg.go.dev/embed for more information.

//go:embed all:frontend/dist
var assets embed.FS

// main function serves as the application's entry point. It initializes the application,
// creates a window, registers backend services, and runs the application.
func main() {
	app := application.New(application.Options{
		Name:        "DriveWise",
		Description: "C 盘智能清理工具",
		Services: []application.Service{
			application.NewService(&cleaner.CacheService{}),
			application.NewService(&cleaner.RogueService{}),
			application.NewService(&analyzer.AnalyzerService{}),
			application.NewService(&migrator.MigratorService{}),
			application.NewService(&sysinfo.SysService{}),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
	})

	// 创建主窗口
	app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:            "DriveWise - C 盘智能清理工具",
		Width:            1200,
		Height:           760,
		MinWidth:         960,
		MinHeight:        600,
		BackgroundColour: application.NewRGB(248, 250, 252),
		URL:              "/",
	})

	// Run the application. This blocks until the application has been exited.
	err := app.Run()
	if err != nil {
		log.Fatal(err)
	}
}
