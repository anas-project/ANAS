// REQUIREMENTS: CONSOLE-R-008 CONSOLE-R-113
// Package webui exposes the immutable, build-time console bundles embedded in
// anasd. Callers select a closed asset name rather than passing request paths
// into an embedded filesystem.
package webui

import (
	"embed"
	"errors"
)

// Asset names are closed so an HTTP path can never become an embed.FS path.
type Asset string

const (
	MainIndex      Asset = "main-index"
	MainJavaScript Asset = "main-javascript"
	MainStyles     Asset = "main-styles"
	RecoveryIndex  Asset = "recovery-index"
	RecoveryScript Asset = "recovery-javascript"
	RecoveryStyles Asset = "recovery-styles"
)

var ErrAssetNotFound = errors.New("embedded console asset was not found")

type Content struct {
	Body        []byte
	ContentType string
}

type assetSpec struct {
	path        string
	contentType string
}

var assetSpecs = map[Asset]assetSpec{
	MainIndex:      {path: "dist/main/index.html", contentType: "text/html; charset=utf-8"},
	MainJavaScript: {path: "dist/main/assets/main.js", contentType: "text/javascript; charset=utf-8"},
	MainStyles:     {path: "dist/main/assets/main.css", contentType: "text/css; charset=utf-8"},
	RecoveryIndex:  {path: "dist/emergency/index.html", contentType: "text/html; charset=utf-8"},
	RecoveryScript: {path: "dist/emergency/assets/emergency.js", contentType: "text/javascript; charset=utf-8"},
	RecoveryStyles: {path: "dist/emergency/assets/emergency.css", contentType: "text/css; charset=utf-8"},
}

//go:embed dist/main/index.html dist/main/assets/main.js dist/main/assets/main.css
//go:embed dist/emergency/index.html dist/emergency/assets/emergency.js dist/emergency/assets/emergency.css
var files embed.FS

func Read(asset Asset) (Content, error) {
	spec, ok := assetSpecs[asset]
	if !ok {
		return Content{}, ErrAssetNotFound
	}
	body, err := files.ReadFile(spec.path)
	if err != nil {
		return Content{}, errors.Join(ErrAssetNotFound, err)
	}
	return Content{Body: body, ContentType: spec.contentType}, nil
}
