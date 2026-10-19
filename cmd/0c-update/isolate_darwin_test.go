package main

func isolateResidentEngine() func() {
	spawnResident = func() error { return nil }
	return func() {}
}
