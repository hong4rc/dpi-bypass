//go:build !windows

package autostart

import "errors"

func Supported() bool { return false }
func Enabled() bool   { return false }
func Set(bool) error  { return errors.New("start at login is only supported on Windows") }
