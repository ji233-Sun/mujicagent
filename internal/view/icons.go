package view

import (
	"embed"

	"github.com/egoist/mygo/ui"
)

// Original Lucide SVGs and their license are bundled with the application.
//
//go:embed assets/lucide
var lucideAssets embed.FS

func lucideIcon(name string) *ui.SVG {
	data, err := lucideAssets.ReadFile("assets/lucide/" + name + ".svg")
	if err != nil {
		panic(err)
	}
	return ui.MustParseSVG(data)
}

var (
	plus            = lucideIcon("plus")
	chat            = lucideIcon("message-square")
	settle          = lucideIcon("archive-arrow-down")
	restore         = lucideIcon("archive-arrow-up")
	folder          = lucideIcon("folder")
	folderPlus      = lucideIcon("folder-plus")
	gear            = lucideIcon("settings")
	downloadIcon    = lucideIcon("download")
	spark           = lucideIcon("sparkles")
	chevron         = lucideIcon("chevron-right")
	arrow           = lucideIcon("arrow-up")
	stopIcon        = lucideIcon("square")
	closeIcon       = lucideIcon("x")
	checkIcon       = lucideIcon("check")
	agentIcon       = lucideIcon("bot")
	appearanceIcon  = lucideIcon("palette")
	terminalIcon    = lucideIcon("terminal")
	fileEditIcon    = lucideIcon("file-pen-line")
	thinkingIcon    = lucideIcon("brain")
	searchIcon      = lucideIcon("search")
	planIcon        = lucideIcon("list-checks")
	activityIcon    = lucideIcon("layers")
	shieldIcon      = lucideIcon("shield")
	shieldCheckIcon = lucideIcon("shield-check")
	ruleIcon        = lucideIcon("key-round")
	failureIcon     = lucideIcon("circle-x")
	fastIcon        = lucideIcon("zap")
	ultrafastIcon   = lucideIcon("gauge")
)

// A segmented folder outline distinguishes the virtual project from real directories.
var noProjectFolder = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
<path d="M2 8V5a2 2 0 0 1 2-2h2 M9 3l2 3h3 M18 6h2a2 2 0 0 1 2 2 M22 12v3 M22 18a2 2 0 0 1-2 2h-2 M14 20h-3 M7 20H4a2 2 0 0 1-2-2 M2 14v-2"/>
</svg>`))
