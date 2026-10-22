//go:build !darwin

package main

// #198 的 root 降權與擁有者修復只在 macOS（.pkg 會被 sudo installer、MDM 以 root 執行）。
func dropRoot(args []string) error           { return nil }
func refuseForeignOwned(args []string) error { return nil }
func ownershipLine() string                  { return "" }
