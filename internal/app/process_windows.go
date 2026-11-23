//go:build windows

package app

import (
	"os/exec"
	"syscall"
)

func hideDiagnosticWindow(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
}
