//+build !linux

package main

import "os/exec"

func setDeathSignal(cmd *exec.Cmd) {}
