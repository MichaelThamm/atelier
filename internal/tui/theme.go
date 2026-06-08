package tui

// Catppuccin Mocha (dark) + Latte (light) — the default Atelier theme.
//
// Palette and role assignments live in this file; nowhere else in the TUI
// should `lipgloss.Color` / `lipgloss.AdaptiveColor` appear inline. Adding
// a new theme later means swapping the constants below and the role-styles
// at the bottom of this file pick up the change automatically.
//
// Colour names follow the Catppuccin reference (https://catppuccin.com/).

import "github.com/charmbracelet/lipgloss"

// Mocha (dark) — the project's primary aesthetic.
const (
	mochaBase     = "#1e1e2e"
	mochaMantle   = "#181825"
	mochaSurface0 = "#313244"
	mochaSurface2 = "#585b70"
	mochaOverlay1 = "#7f849c"
	mochaText     = "#cdd6f4"
	mochaMauve    = "#cba6f7"
	mochaBlue     = "#89b4fa"
	mochaGreen    = "#a6e3a1"
	mochaPeach    = "#fab387"
	mochaYellow   = "#f9e2af"
	mochaRed      = "#f38ba8"
	mochaMaroon   = "#eba0ac"
)

// Latte (light) — same shape, different luminance. Used by AdaptiveColor
// when the user's terminal background is light.
const (
	latteBase     = "#eff1f5"
	latteMantle   = "#e6e9ef"
	latteSurface0 = "#ccd0da"
	latteSurface2 = "#acb0be"
	latteOverlay1 = "#8c8fa1"
	latteText     = "#4c4f69"
	latteMauve    = "#8839ef"
	latteBlue     = "#1e66f5"
	latteGreen    = "#40a02b"
	latteYellow   = "#df8e1d"
	lattePeach    = "#fe640b"
	latteRed      = "#d20f39"
	latteMaroon   = "#e64553"
)

func adaptive(dark, light string) lipgloss.AdaptiveColor {
	return lipgloss.AdaptiveColor{Dark: dark, Light: light}
}

// --- semantic colour roles ---------------------------------------------------

var (
	// colorPrimary is the focus / current-selection accent. Used for the
	// active cursor row background and for module headers in the plan view.
	colorPrimary = adaptive(mochaMauve, latteMauve)

	// colorSecondary is the supporting accent used where colorPrimary would
	// clash with itself (e.g. group headers in the left pane).
	colorSecondary = adaptive(mochaBlue, latteBlue)

	// colorText is the default body text colour. Most strings should let
	// the terminal foreground show through; this is here for places where
	// we set an explicit background and need a paired foreground.
	colorText = adaptive(mochaText, latteText)

	// colorMuted is the dim text colour used for descriptions, hints, type
	// bucket labels in the plan view.
	colorMuted = adaptive(mochaOverlay1, latteOverlay1)

	// colorFaint is even quieter than colorMuted — used for borders and
	// the "at default" left-pane marker.
	colorFaint = adaptive(mochaSurface2, latteSurface2)

	// colorBg is the terminal-default background tone. AdaptiveColor lets
	// reverse-video text pick a sensible foreground.
	colorBg = adaptive(mochaBase, latteBase)

	// colorBgTint is a subtle surface used for the status bar background
	// (one notch above the terminal default).
	colorBgTint = adaptive(mochaSurface0, latteSurface0)

	// colorBgMantle is a notch darker than colorBg — reserved for chrome
	// edges if we want to draw subtle outer borders.
	colorBgMantle = adaptive(mochaMantle, latteMantle)

	// Action / status semantics — used by the plan view markers, the left
	// pane variable markers, and status-bar severity colouring.
	colorSuccess   = adaptive(mochaGreen, latteGreen)
	colorWarning   = adaptive(mochaPeach, lattePeach)
	colorDanger    = adaptive(mochaRed, latteRed)
	colorReplace   = adaptive(mochaMaroon, latteMaroon)
	colorSensitive = adaptive(mochaYellow, latteYellow)
)

// --- composed styles ---------------------------------------------------------
//
// Each style below is the *only* place its visual treatment is defined. Pure
// renderers in view.go / plan_view.go reach for these by name — they never
// construct ad-hoc styles.

var (
	// stylePanel is the rounded border used for both left and right panes,
	// matching the modal frame aesthetic.
	stylePanel = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorFaint).
			Padding(0, 1)

	// stylePanelFocused highlights the active pane's border.
	stylePanelFocused = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(colorPrimary).
				Padding(0, 1)

	// styleCursorActive is the highlight applied to the cursor row in the
	// *focused* pane. The dark-on-primary contrast pops against either
	// terminal background.
	styleCursorActive = lipgloss.NewStyle().
				Foreground(colorBg).
				Background(colorPrimary).
				Bold(true)

	// styleCursorInactive is the cursor row in the unfocused pane: a
	// subtler tint so the user can still see where they were without it
	// competing for attention.
	styleCursorInactive = lipgloss.NewStyle().
				Foreground(colorPrimary).
				Bold(true)

	styleDescription = lipgloss.NewStyle().
				Foreground(colorMuted).
				Italic(true)

	styleHelp = lipgloss.NewStyle().
			Foreground(colorMuted)

	styleStatusBar = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorFaint).
			Foreground(colorMuted).
			Padding(0, 1)

	styleHeaderBar = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorPrimary).
			Foreground(colorText).
			Bold(true).
			Padding(0, 1)

	styleStatusError = lipgloss.NewStyle().Foreground(colorDanger).Bold(true)
	styleStatusBusy  = lipgloss.NewStyle().Foreground(colorPrimary).Bold(true)

	// Left-pane variable markers.
	styleMarkerAtDefault = lipgloss.NewStyle().Foreground(colorFaint)
	styleMarkerModified  = lipgloss.NewStyle().Foreground(colorWarning).Bold(true)
	styleMarkerRequired  = lipgloss.NewStyle().Foreground(colorDanger).Bold(true)
	styleMarkerExpr      = lipgloss.NewStyle().Foreground(colorSecondary).Bold(true)

	// Left-pane section headers (multi-module grouping).
	styleSectionHeader = lipgloss.NewStyle().Foreground(colorSecondary).Bold(true)

	// styleUnpinnedTag is the dim "unpinned" affordance shown beside a remote
	// module that carries no git pin. Faint + italic so it reads as meta, never
	// as a ref token (ADR-0019 amendment).
	styleUnpinnedTag = lipgloss.NewStyle().Foreground(colorFaint).Italic(true)

	// Right-pane editor accents.
	styleVarHeader    = lipgloss.NewStyle().Foreground(colorText).Bold(true)
	styleSensitiveTag = lipgloss.NewStyle().Foreground(colorSensitive).Italic(true)
	styleRequiredTag  = lipgloss.NewStyle().Foreground(colorDanger).Italic(true)
	styleWiredTag     = lipgloss.NewStyle().Foreground(colorSecondary).Bold(true)
	styleWiredExpr    = lipgloss.NewStyle().Foreground(colorText)

	// Plan view.
	stylePlanSummary  = lipgloss.NewStyle().Foreground(colorText).Bold(true).Underline(true)
	stylePlanModule   = lipgloss.NewStyle().Foreground(colorSecondary).Bold(true)
	stylePlanType     = lipgloss.NewStyle().Foreground(colorMuted)
	stylePlanResource = lipgloss.NewStyle().Foreground(colorText)
	stylePlanAdd      = lipgloss.NewStyle().Foreground(colorSuccess).Bold(true)
	stylePlanChange   = lipgloss.NewStyle().Foreground(colorWarning).Bold(true)
	stylePlanDelete   = lipgloss.NewStyle().Foreground(colorDanger).Bold(true)
	stylePlanReplace  = lipgloss.NewStyle().Foreground(colorReplace).Bold(true)
)
