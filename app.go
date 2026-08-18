package main

import (
	"context"
	_ "embed"
)

//go:embed LICENSE
var licenseText string

// App struct
type App struct {
	ctx context.Context
}

// NewApp creates a new App application struct
func NewApp() *App {
	return &App{}
}

// startup is called when the app starts. The context is saved
// so we can call the runtime methods
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	// Undo was removed in v1.4.0. Whatever it left in the state directory is
	// unreadable dead weight now, so it goes on the way in rather than sitting
	// there for someone to find and wonder whether it is safe to delete.
	clearRemovedUndoState()
}

// GetLicenseText returns the full text of the app's licence, embedded at
// build time so the About overlay can never drift from the shipped LICENSE.
func (a *App) GetLicenseText() string {
	return licenseText
}
