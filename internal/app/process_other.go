//go:build !windows

package app

import "os/exec"

func hideDiagnosticWindow(*exec.Cmd) {}
