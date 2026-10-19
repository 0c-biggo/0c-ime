import Foundation

let root = FileManager.default.temporaryDirectory.appendingPathComponent("0c-launcher-test-\(UUID().uuidString)")
try FileManager.default.createDirectory(at: root, withIntermediateDirectories: true)
defer { try? FileManager.default.removeItem(at: root) }
let updater = SettingsLauncher.updater(home: root)
precondition(updater.path.hasSuffix("Library/Application Support/0c/bin/0c-update"))
precondition(SettingsLauncher.open(updater: updater) != nil, "missing updater must explain how to recover")
try FileManager.default.createDirectory(at: updater.deletingLastPathComponent(), withIntermediateDirectories: true)
// 空白、引號、$ 都是路徑字元，不透過 shell 展開呼叫參數。
try "#!/bin/sh\n[ \"$#\" = 1 ] && [ \"$1\" = open-settings ] || exit 42\n".write(to: updater, atomically: true, encoding: .utf8)
try FileManager.default.setAttributes([.posixPermissions: 0o755], ofItemAtPath: updater.path)
precondition(SettingsLauncher.open(updater: updater) == nil)
try "#!/bin/sh\necho 'receipt verification failed' >&2\nexit 1\n".write(to: updater, atomically: true, encoding: .utf8)
try FileManager.default.setAttributes([.posixPermissions: 0o755], ofItemAtPath: updater.path)
precondition(SettingsLauncher.open(updater: updater)?.contains("receipt verification failed") == true)
print("settings launcher: stable path, argument forwarding, missing updater, verification failure passed")
