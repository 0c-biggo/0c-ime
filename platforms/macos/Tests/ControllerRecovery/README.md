# Controller recovery regression

Run `make -C platforms/macos test-controller-recovery` on an Apple Silicon Mac. The harness compiles the production input controller and connects it to an isolated engine socket. It checks queued composition recovery and Caps Lock state synchronization after a warm IPC session fails. A second engine set to return to Zhuyin each time (`mode_memory=native`) with right Shift as the switch key checks that turning Caps Lock on and off returns to the mode from before it was turned on (#219). It does not install or register an input method.
