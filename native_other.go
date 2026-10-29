//go:build !windows

package main

const nativeAvailable = false

func nativeStart() {}
func nativeStop()  {}

func nativeRebind()                   {}
func hotkeyStatus() map[string]string { return map[string]string{} }
