package core

import "testing"

func TestReadSeedSources(t *testing.T) {
	sources, err := ReadSeedSources("../seed/catalog.sql")
	if err != nil {
		t.Fatal(err)
	}
	if len(sources) != 111 {
		t.Fatalf("got %d unique source URLs", len(sources))
	}
	if sources["https://www.phila.gov/services/mental-physical-health/find-free-food-or-meals/"] != "food" {
		t.Fatal("source IDs not preserved")
	}
	if _, err = ReadSeedSources("missing.sql"); err == nil {
		t.Fatal("missing seed accepted")
	}
}
