package ui

import (
	"github.com/charmbracelet/lipgloss"
	"github.com/stanlyzoolo/keepkit/internal/loader"
)

func StatusStyle(s loader.Status) lipgloss.Style {
	switch s {
	case loader.StatusActive:
		return StatusStyleActive
	case loader.StatusTrying:
		return StatusStyleTrying
	case loader.StatusInactive:
		return StatusStyleInactive
	default:
		return StatusStyleTrying
	}
}
