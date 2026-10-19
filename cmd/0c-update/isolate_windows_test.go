package main

func isolateResidentEngine() func() {
	startResident = func(string) error { return nil }
	return func() {}
}
