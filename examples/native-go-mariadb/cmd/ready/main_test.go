package main

import "testing"

func TestReadinessRequiresApplicationCredentials(t *testing.T) {
	for _, key := range []string{"DB_USERNAME", "DB_PASSWORD", "DB_DATABASE"} {
		t.Run(key, func(t *testing.T) {
			t.Setenv("DB_USERNAME", "app")
			t.Setenv("DB_PASSWORD", "private")
			t.Setenv("DB_DATABASE", "app")
			t.Setenv(key, "")
			if ready() {
				t.Fatal("accepted missing application credentials")
			}
		})
	}
}
