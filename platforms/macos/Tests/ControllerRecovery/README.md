# Controller recovery regression

Run `make -C platforms/macos test-controller-recovery` on an Apple Silicon Mac. The harness compiles the production input controller and connects it to an isolated engine socket. It checks queued composition recovery and Caps Lock state synchronization after a warm IPC session fails. It does not install or register an input method.
