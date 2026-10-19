//go:build windows

package main

// Windows 由 TSF DLL 依 engine.json 選擇引擎版本並傳入 -data；引擎本身不轉交。
func redirectEngine(string) {}
