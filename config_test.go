package main

import "testing"

// setTestConfig mutates the package-level config for one test and restores
// the previous value on cleanup. Tests using it must not run in parallel.
func setTestConfig(t *testing.T, mutate func(*appConfig)) {
	t.Helper()
	original := conf
	mutate(&conf)
	t.Cleanup(func() { conf = original })
}
